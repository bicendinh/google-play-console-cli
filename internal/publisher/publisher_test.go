package publisher_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	pub "google.golang.org/api/androidpublisher/v3"

	"playops/internal/fault"
	"playops/internal/publisher"
	"playops/internal/testutil"
)

type play struct {
	mu          sync.Mutex
	tracks      map[string]*pub.Track
	pending     map[string]*pub.Track
	events      []string
	fail        string
	commitCount int
	review      bool
}

func (p *play) handler(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	stage := ""
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/edits") && r.Method == "POST":
		stage = "begin"
	case strings.HasSuffix(path, "/bundles"):
		stage = "upload"
	case strings.HasSuffix(path, ":validate"):
		stage = "validate"
	case strings.HasSuffix(path, ":commit"):
		stage = "commit"
	case strings.Contains(path, "/tracks/") && r.Method == "GET":
		stage = "read"
	case strings.Contains(path, "/tracks/") && r.Method == "PUT":
		stage = "update"
	case r.Method == "DELETE":
		stage = "discard"
	default:
		http.Error(w, "unexpected request", 500)
		return
	}
	p.events = append(p.events, stage)
	if p.fail == stage {
		code := 404
		message := "edit expired"
		if stage == "upload" {
			code = 400
			message = "Version code 123 has already been used."
		}
		w.WriteHeader(code)
		fmt.Fprintf(w, `{"error":{"code":%d,"message":%q}}`, code, message)
		return
	}
	switch stage {
	case "begin":
		b, _ := json.Marshal(p.tracks)
		_ = json.Unmarshal(b, &p.pending)
		fmt.Fprint(w, `{"id":"edit-1"}`)
	case "read":
		track := path[strings.LastIndex(path, "/")+1:]
		value := p.pending[track]
		if value == nil {
			value = &pub.Track{Track: track}
		}
		_ = json.NewEncoder(w).Encode(value)
	case "upload":
		_, _ = io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, `{"versionCode":123}`)
	case "update":
		var track pub.Track
		if err := json.NewDecoder(r.Body).Decode(&track); err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		p.pending[track.Track] = &track
		_ = json.NewEncoder(w).Encode(track)
	case "validate":
		fmt.Fprint(w, `{"id":"edit-1"}`)
	case "commit":
		p.commitCount++
		if r.URL.Query().Get("changesInReviewBehavior") != "ERROR_IF_IN_REVIEW" {
			http.Error(w, "unsafe commit", 400)
			return
		}
		if p.review {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"code":400,"message":"You already have changes in review"}}`)
			return
		}
		p.tracks = p.pending
		if p.fail == "commit-drop" {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		if p.fail == "commit-malformed" {
			fmt.Fprint(w, `{`)
			return
		}
		if p.fail == "commit-timeout" {
			<-r.Context().Done()
			return
		}
		if p.fail == "commit-500" {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"error":{"code":500,"message":"unknown server state"}}`)
			return
		}
		fmt.Fprint(w, `{"id":"edit-1"}`)
	case "discard":
		w.WriteHeader(204)
	}
}
func release(code int64, status string) *pub.TrackRelease {
	return &pub.TrackRelease{VersionCodes: []int64{code}, Status: status}
}
func TestDraftPublishPromoteAndRollout(t *testing.T) {
	p := &play{tracks: map[string]*pub.Track{"internal": {Track: "internal", Releases: []*pub.TrackRelease{release(100, "draft")}}, "production": {Track: "production", Releases: []*pub.TrackRelease{release(99, "completed")}}}}
	client := testutil.Google(t, p.handler)
	s := publisher.Service{API: client}
	ctx := context.Background()
	out, err := s.Execute(ctx, publisher.Request{Action: "upload", App: "com.example.app", Track: "internal", Name: "New build"}, strings.NewReader("aab"))
	if err != nil || out.Status != "draft" || !out.Committed {
		t.Fatalf("upload: %+v %v", out, err)
	}
	if len(p.tracks["internal"].Releases) != 2 || p.tracks["internal"].Releases[0].Status != "draft" {
		t.Fatal("unrelated draft lost")
	}
	_, err = s.Execute(ctx, publisher.Request{Action: "publish", App: "com.example.app", Track: "internal", VersionCode: 123}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.tracks["internal"].Releases[1].Status != "completed" {
		t.Fatal("draft not published")
	}
	_, err = s.Execute(ctx, publisher.Request{Action: "promote", App: "com.example.app", From: "internal", Track: "production", VersionCode: 123, Rollout: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.tracks["production"].Releases) != 2 {
		t.Fatal("fallback release lost")
	}
	for _, request := range []publisher.Request{{Action: "set", Rollout: 25}, {Action: "halt"}, {Action: "resume"}, {Action: "set", Rollout: 100, ReplaceExisting: true}} {
		request.App = "com.example.app"
		request.Track = "production"
		request.VersionCode = 123
		if _, err = s.Execute(ctx, request, nil); err != nil {
			t.Fatalf("%s: %v", request.Action, err)
		}
	}
	active := p.tracks["production"].Releases
	if len(active) != 1 || active[0].Status != "completed" || active[0].UserFraction != 0 {
		t.Fatalf("completion: %+v", active)
	}
	if p.tracks["internal"].Releases[1].Status != "completed" {
		t.Fatal("promote modified source track")
	}
}
func TestFailuresDoNotReplayOrCommit(t *testing.T) {
	for _, stage := range []string{"read", "upload", "update", "validate", "review", "commit-drop", "commit-malformed", "commit-timeout", "commit-500"} {
		t.Run(stage, func(t *testing.T) {
			p := &play{tracks: map[string]*pub.Track{}, fail: stage, review: stage == "review"}
			client := testutil.Google(t, p.handler)
			ctx := context.Background()
			if stage == "commit-timeout" {
				timed, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				ctx = timed
			}
			_, err := (publisher.Service{API: client}).Execute(ctx, publisher.Request{Action: "upload", App: "com.example.app", Track: "internal"}, strings.NewReader("aab"))
			p.mu.Lock()
			defer p.mu.Unlock()
			if err == nil {
				t.Fatal("expected error")
			}
			e := fault.From(err)
			unknown := strings.HasPrefix(stage, "commit-")
			if unknown {
				if e.Code != "COMMIT_OUTCOME_UNKNOWN" || p.commitCount != 1 || slices.Contains(p.events, "discard") {
					t.Fatalf("unknown commit: %+v, %v", e, p.events)
				}
			} else {
				if !slices.Contains(p.events, "discard") {
					t.Fatal("failed edit not discarded")
				}
				if stage != "review" && p.commitCount != 0 {
					t.Fatal("committed after failure")
				}
			}
			if e.Retryable {
				t.Fatal("write failure must not be automatically replayable")
			}
		})
	}
}
func TestExplicitReplacementAndValidation(t *testing.T) {
	p := &play{tracks: map[string]*pub.Track{"production": {Track: "production", Releases: []*pub.TrackRelease{release(99, "completed"), release(123, "draft")}}}}
	client := testutil.Google(t, p.handler)
	_, err := (publisher.Service{API: client}).Execute(context.Background(), publisher.Request{Action: "publish", App: "com.example.app", Track: "production", VersionCode: 123, Rollout: 100}, nil)
	if fault.From(err).Exit != 4 || p.commitCount != 0 {
		t.Fatal("must refuse implicit superseding")
	}
	if err = publisher.Check(publisher.Request{Action: "publish", Track: "wear:production", VersionCode: 123}); err == nil {
		t.Fatal("production must specify rollout")
	}
	if err = publisher.ValidateBundle(testutil.Bundle(t)); err != nil {
		t.Fatal(err)
	}
}
func TestReadTrackAlwaysDiscards(t *testing.T) {
	p := &play{tracks: map[string]*pub.Track{}}
	client := testutil.Google(t, p.handler)
	_, err := (publisher.Service{API: client}).ReadTrack(context.Background(), "com.example.app", "internal")
	if err != nil || p.commitCount != 0 || !slices.Contains(p.events, "discard") {
		t.Fatal("read leaked edit or committed")
	}
}
