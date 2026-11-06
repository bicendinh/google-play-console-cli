// Package googleapi owns authentication and official Google client adapters.
package googleapi

import (
	"context"
	"io"
	"net/http"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	pub "google.golang.org/api/androidpublisher/v3"
	gapi "google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	rep "google.golang.org/api/playdeveloperreporting/v1beta1"
	store "google.golang.org/api/storage/v1"

	"playops/internal/fault"
)

var Scopes = []string{pub.AndroidpublisherScope, rep.PlaydeveloperreportingScope, store.DevstorageReadOnlyScope}

func Credentials(ctx context.Context, path string) (*google.Credentials, error) {
	var creds *google.Credentials
	var err error
	if path != "" {
		var b []byte
		b, err = os.ReadFile(path)
		if err == nil {
			creds, err = google.CredentialsFromJSON(ctx, b, Scopes...)
		}
	} else {
		creds, err = google.FindDefaultCredentials(ctx, Scopes...)
	}
	if err != nil {
		return nil, fault.New("AUTH_CONFIGURATION", "Cannot load Google credentials; configure a service account file or ADC", 3)
	}
	return creds, nil
}

type Client struct {
	Publisher *pub.Service
	Reporting *rep.Service
	Storage   *store.Service
	HTTP      *http.Client
}

func New(ctx context.Context, path string) (*Client, error) {
	creds, err := Credentials(ctx, path)
	if err != nil {
		return nil, err
	}
	// The command context bounds the whole operation. A separate per-request
	// timeout can be configured by callers without capping an entire upload.
	h := &http.Client{Transport: &oauth2.Transport{Source: creds.TokenSource, Base: http.DefaultTransport}}
	return WithHTTP(ctx, h)
}
func WithHTTP(ctx context.Context, h *http.Client) (*Client, error) {
	p, err := pub.NewService(ctx, option.WithHTTPClient(h))
	if err != nil {
		return nil, err
	}
	r, err := rep.NewService(ctx, option.WithHTTPClient(h))
	if err != nil {
		return nil, err
	}
	s, err := store.NewService(ctx, option.WithHTTPClient(h))
	if err != nil {
		return nil, err
	}
	return &Client{Publisher: p, Reporting: r, Storage: s, HTTP: h}, nil
}
func (c *Client) Begin(ctx context.Context, app string) (string, error) {
	e, err := c.Publisher.Edits.Insert(app, &pub.AppEdit{}).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	if e.Id == "" {
		return "", fault.New("INVALID_RESPONSE", "Google did not return an edit ID; inspect Console before another write", 5)
	}
	return e.Id, nil
}
func (c *Client) Track(ctx context.Context, app, edit, track string) (*pub.Track, error) {
	return c.Publisher.Edits.Tracks.Get(app, edit, track).Context(ctx).Do()
}
func (c *Client) Update(ctx context.Context, app, edit string, track *pub.Track) error {
	_, err := c.Publisher.Edits.Tracks.Update(app, edit, track.Track, track).Context(ctx).Do()
	return err
}
func (c *Client) Upload(ctx context.Context, app, edit string, r io.Reader) (int64, error) {
	// The SDK retries chunks within the same upload session, never the edit or
	// commit. Small payloads may use a single multipart request.
	b, err := c.Publisher.Edits.Bundles.Upload(app, edit).Media(r, gapi.ContentType("application/octet-stream"), gapi.ChunkSize(8*1024*1024)).Context(ctx).Do()
	if err != nil {
		return 0, err
	}
	if b.VersionCode <= 0 {
		return 0, fault.New("INVALID_RESPONSE", "Google did not return a valid bundle version code", 5)
	}
	return b.VersionCode, nil
}
func (c *Client) Validate(ctx context.Context, app, edit string) error {
	_, err := c.Publisher.Edits.Validate(app, edit).Context(ctx).Do()
	return err
}
func (c *Client) Commit(ctx context.Context, app, edit string) error {
	_, err := c.Publisher.Edits.Commit(app, edit).ChangesInReviewBehavior("ERROR_IF_IN_REVIEW").Context(ctx).Do()
	return err
}
func (c *Client) Discard(ctx context.Context, app, edit string) error {
	return c.Publisher.Edits.Delete(app, edit).Context(ctx).Do()
}
func (c *Client) Releases(ctx context.Context, app, track string) ([]*pub.ReleaseSummary, error) {
	r, err := c.Publisher.Applications.Tracks.Releases.List("applications/" + app + "/tracks/" + track).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	if r.Releases == nil {
		return []*pub.ReleaseSummary{}, nil
	}
	return r.Releases, nil
}
