package publisher

import (
	"context"
	"encoding/json"
	"time"

	pub "google.golang.org/api/androidpublisher/v3"
	"playops/internal/fault"
)

type CommitOptions struct {
	InReview                string `json:"inReview" yaml:"inReview"`
	ChangesNotSentForReview bool   `json:"changesNotSentForReview" yaml:"changesNotSentForReview"`
}

func (o CommitOptions) Validate() error {
	if o.InReview != "" && o.InReview != "error" && o.InReview != "cancel" {
		return fault.Input("inReview must be error or cancel")
	}
	return nil
}
func ResolveActive(t *pub.Track, selected *pub.TrackRelease, replace bool) error {
	return resolveActive(t, selected, replace)
}
func Ambiguous(err error) bool                { return ambiguous(err) }
func EditError(err error, stage string) error { return editError(err, stage) }
func CloneTrack(t *pub.Track) *pub.Track {
	b, _ := json.Marshal(t)
	var clone pub.Track
	_ = json.Unmarshal(b, &clone)
	return &clone
}
func Same(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Transaction is the single owner of an edit. Callback helpers cannot commit.
// Snapshot reads and writes share the same cleanup and uncertainty rules.
func Transaction(ctx context.Context, api API, app string, commit func(context.Context, string) error, fn func(string) error) (id string, err error) {
	id, err = api.Begin(ctx, app)
	if err != nil {
		return "", EditError(err, "create")
	}
	committed, unknown := false, false
	defer func() {
		if committed || unknown {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if e := api.Discard(cleanup, app, id); e != nil {
			if err == nil {
				err = EditError(e, "discard")
			} else {
				f := fault.From(err)
				f.Retryable = false
				f.Details = map[string]any{"editId": id, "cleanup": "failed; edit may remain until expiry"}
				err = f
			}
		}
	}()
	if err = fn(id); err != nil {
		return id, EditError(err, "prepare edit")
	}
	if commit == nil {
		return id, nil
	}
	if err = api.Validate(ctx, app, id); err != nil {
		return id, EditError(err, "validate")
	}
	if err = commit(ctx, id); err != nil {
		if Ambiguous(err) {
			unknown = true
			f := fault.New("COMMIT_OUTCOME_UNKNOWN", "Commit outcome is unknown; inspect releases/Console before any further write. Do not replay automatically", 4)
			f.Details = map[string]any{"app": app, "editId": id}
			return id, f
		}
		return id, EditError(err, "commit")
	}
	committed = true
	return id, nil
}
