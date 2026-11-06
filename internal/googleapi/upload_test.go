package googleapi_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"playops/internal/testutil"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResumableUploadRetriesOnlyTheSameChunk(t *testing.T) {
	var mu sync.Mutex
	initiations, firstChunk, lastChunk := 0, 0, 0
	client := testutil.Google(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/bundles") {
			initiations++
			if r.URL.Query().Get("uploadType") != "resumable" {
				t.Error("large upload must initiate a resumable session")
			}
			w.Header().Set("Location", "https://androidpublisher.googleapis.com/test-upload-session")
			fmt.Fprint(w, `{}`)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/test-upload-session" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.HasPrefix(r.Header.Get("Content-Range"), "bytes 0-") {
			firstChunk++
			if firstChunk == 1 {
				w.WriteHeader(500)
				fmt.Fprint(w, `{"error":{"code":500,"message":"transient chunk error"}}`)
				return
			}
			w.Header().Set("Range", "bytes=0-8388607")
			w.Header().Set("X-Http-Status-Code-Override", "308")
			w.WriteHeader(200)
			return
		}
		lastChunk++
		fmt.Fprint(w, `{"versionCode":123}`)
	})
	if client.HTTP.Timeout != 0 {
		t.Fatal("fixed request timeout caps uploads")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	code, err := client.Upload(ctx, "com.example.app", "existing-edit", bytes.NewReader(make([]byte, 9*1024*1024)))
	if err != nil || code != 123 {
		t.Fatalf("resumable upload: %d %v", code, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if initiations != 1 || firstChunk != 2 || lastChunk != 1 {
		t.Fatalf("session replayed or chunk lost: %d %d %d", initiations, firstChunk, lastChunk)
	}
}
func TestUploadHonorsCommandCancellation(t *testing.T) {
	client := testutil.Google(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := client.Upload(ctx, "com.example.app", "edit", bytes.NewReader([]byte("small"))); err == nil {
		t.Fatal("upload ignored whole-command cancellation")
	}
}
