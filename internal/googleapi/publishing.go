package googleapi

import (
	"context"
	"io"

	pub "google.golang.org/api/androidpublisher/v3"
	gapi "google.golang.org/api/googleapi"
	"playops/internal/publisher"
)

func (c *Client) CommitWithOptions(ctx context.Context, app, edit string, o publisher.CommitOptions) error {
	if err := o.Validate(); err != nil {
		return err
	}
	behavior := "ERROR_IF_IN_REVIEW"
	if o.InReview == "cancel" {
		behavior = "CANCEL_IN_REVIEW_AND_SUBMIT"
	}
	_, err := c.Publisher.Edits.Commit(app, edit).ChangesInReviewBehavior(behavior).ChangesNotSentForReview(o.ChangesNotSentForReview).Context(ctx).Do()
	return err
}
func (c *Client) CheckEdit(ctx context.Context, app, edit string) error {
	_, err := c.Publisher.Edits.Get(app, edit).Context(ctx).Do()
	return err
}
func (c *Client) Listings(ctx context.Context, app, edit string) ([]*pub.Listing, error) {
	r, err := c.Publisher.Edits.Listings.List(app, edit).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	return r.Listings, nil
}
func (c *Client) PutListing(ctx context.Context, app, edit, locale string, v *pub.Listing) error {
	// The caller supplies a fully merged resource, including explicit empty fields.
	v.ForceSendFields = []string{"Title", "ShortDescription", "FullDescription", "Video"}
	_, err := c.Publisher.Edits.Listings.Update(app, edit, locale, v).Context(ctx).Do()
	return err
}
func (c *Client) Images(ctx context.Context, app, edit, locale, kind string) ([]*pub.Image, error) {
	r, err := c.Publisher.Edits.Images.List(app, edit, locale, kind).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	if r.Images == nil {
		return []*pub.Image{}, nil
	}
	return r.Images, nil
}
func (c *Client) DeleteImages(ctx context.Context, app, edit, locale, kind string) error {
	_, err := c.Publisher.Edits.Images.Deleteall(app, edit, locale, kind).Context(ctx).Do()
	return err
}
func (c *Client) UploadImage(ctx context.Context, app, edit, locale, kind string, r io.Reader) (*pub.Image, error) {
	v, err := c.Publisher.Edits.Images.Upload(app, edit, locale, kind).Media(r, gapi.ChunkSize(0)).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	return v.Image, nil
}
func (c *Client) Symbols(ctx context.Context, app, edit string, version int64, kind string, r io.Reader) error {
	_, err := c.Publisher.Edits.Deobfuscationfiles.Upload(app, edit, version, kind).Media(r, gapi.ContentType("application/octet-stream"), gapi.ChunkSize(8*1024*1024)).Context(ctx).Do()
	return err
}
