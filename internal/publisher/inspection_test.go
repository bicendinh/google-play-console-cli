package publisher_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"playops/internal/fault"
	"playops/internal/publisher"
	"playops/internal/testutil"
)

func TestInspectionCleanupReceipt(t *testing.T) {
	for _, mode := range []string{"success", "read-failed", "delete-failed"} {
		t.Run(mode, func(t *testing.T) {
			client := testutil.Google(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case "POST":
					fmt.Fprint(w, `{"id":"temporary"}`)
				case "DELETE":
					if mode == "delete-failed" {
						w.WriteHeader(403)
						fmt.Fprint(w, `{"error":{"code":403,"message":"forbidden"}}`)
					} else {
						w.WriteHeader(204)
					}
				default:
					t.Errorf("unexpected request %s", r.Method)
				}
			})
			receipt, e := publisher.Inspect(context.Background(), client, "com.example.app", func(string) error {
				if mode == "read-failed" {
					return fault.Input("Cannot read")
				}
				return nil
			})
			if receipt.EditID != "temporary" || !receipt.EditOpened || receipt.Committed || receipt.Discarded != (mode != "delete-failed") {
				t.Fatalf("wrong receipt %+v", receipt)
			}
			if (e == nil) != (mode == "success") {
				t.Fatalf("wrong error %v", e)
			}
			if e != nil {
				details := fault.From(e).Details.(map[string]any)
				if details["discarded"] != receipt.Discarded {
					t.Fatal("lost cleanup evidence")
				}
			}
		})
	}
}
