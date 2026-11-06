// Package releaseflow plans offline and applies reviewed changes in one edit.
package releaseflow

import (
	"context"
	"io"
	"time"

	pub "google.golang.org/api/androidpublisher/v3"
	"playops/internal/publisher"
)

type Manifest struct {
	App                     string                  `json:"app" yaml:"app"`
	Profile                 string                  `json:"profile" yaml:"profile"`
	Bundle                  string                  `json:"bundle,omitempty" yaml:"bundle,omitempty"`
	VersionCode             int64                   `json:"versionCode,omitempty" yaml:"versionCode,omitempty"`
	Track                   string                  `json:"track" yaml:"track"`
	FromTrack               string                  `json:"fromTrack,omitempty" yaml:"fromTrack,omitempty"`
	Name                    string                  `json:"name,omitempty" yaml:"name,omitempty"`
	Status                  string                  `json:"status" yaml:"status"`
	Rollout                 float64                 `json:"rollout,omitempty" yaml:"rollout,omitempty"`
	Notes                   []Note                  `json:"notes,omitempty" yaml:"notes,omitempty"`
	RequiredLocales         []string                `json:"requiredLocales,omitempty" yaml:"requiredLocales,omitempty"`
	RequiredImageTypes      []string                `json:"requiredImageTypes,omitempty" yaml:"requiredImageTypes,omitempty"`
	Listings                []ListingChange         `json:"listings,omitempty" yaml:"listings,omitempty"`
	Images                  []ImageChange           `json:"images,omitempty" yaml:"images,omitempty"`
	Symbols                 []Symbol                `json:"symbols,omitempty" yaml:"symbols,omitempty"`
	ReplaceExisting         bool                    `json:"replaceExisting" yaml:"replaceExisting"`
	Review                  publisher.CommitOptions `json:"review" yaml:"review"`
	UploadCertificateSHA256 string                  `json:"uploadCertificateSha256,omitempty" yaml:"uploadCertificateSha256,omitempty"`
	Preflight               PreflightOptions        `json:"preflight" yaml:"preflight"`
}
type Note struct {
	Language string `json:"language" yaml:"language"`
	Text     string `json:"text" yaml:"text"`
}
type ListingChange struct {
	Locale           string  `json:"locale" yaml:"locale"`
	File             string  `json:"file,omitempty" yaml:"file,omitempty"`
	Title            *string `json:"title,omitempty" yaml:"title,omitempty"`
	ShortDescription *string `json:"shortDescription,omitempty" yaml:"shortDescription,omitempty"`
	FullDescription  *string `json:"fullDescription,omitempty" yaml:"fullDescription,omitempty"`
	Video            *string `json:"video,omitempty" yaml:"video,omitempty"`
}
type Listing struct {
	Title            string `json:"title"`
	ShortDescription string `json:"shortDescription"`
	FullDescription  string `json:"fullDescription"`
	Video            string `json:"video"`
}
type ImageChange struct {
	Locale               string         `json:"locale" yaml:"locale"`
	Type                 string         `json:"type" yaml:"type"`
	Mode                 string         `json:"mode" yaml:"mode"`                                   // replace, preserve, inherit
	Files                []string       `json:"files,omitempty" yaml:"files,omitempty"`             // final ordered set
	SourceFiles          []string       `json:"sourceFiles,omitempty" yaml:"sourceFiles,omitempty"` // baseline for positional replacements
	Replace              map[int]string `json:"replace,omitempty" yaml:"replace,omitempty"`
	InheritFrom          string         `json:"inheritFrom,omitempty" yaml:"inheritFrom,omitempty"`
	InheritanceConfirmed bool           `json:"inheritanceConfirmed,omitempty" yaml:"inheritanceConfirmed,omitempty"`
}
type Symbol struct {
	Type     string `json:"type" yaml:"type"`
	File     string `json:"file" yaml:"file"`
	Optional bool   `json:"optional" yaml:"optional"`
}
type PreflightOptions struct {
	Required   bool   `json:"required" yaml:"required"`
	Bundletool string `json:"bundletool,omitempty" yaml:"bundletool,omitempty"` // jar or executable
	Java       string `json:"java,omitempty" yaml:"java,omitempty"`
	Jarsigner  string `json:"jarsigner,omitempty" yaml:"jarsigner,omitempty"`
	Keytool    string `json:"keytool,omitempty" yaml:"keytool,omitempty"`
}
type Image struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}
type Snapshot struct {
	FormatVersion int                 `json:"formatVersion"`
	App           string              `json:"app"`
	Profile       string              `json:"profile"`
	CapturedAt    time.Time           `json:"capturedAt"`
	Track         *pub.Track          `json:"track"`
	SourceTrack   *pub.Track          `json:"sourceTrack,omitempty"`
	Listings      map[string]*Listing `json:"listings"`
	Images        map[string][]Image  `json:"images"`
}
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Diff struct {
	Resource string `json:"resource"`
	Before   any    `json:"before"`
	After    any    `json:"after"`
}
type PlannedImages struct {
	Locale string `json:"locale"`
	Type   string `json:"type"`
	Files  []File `json:"files"`
}
type Plan struct {
	SourceManifest      string              `json:"sourceManifest,omitempty"`
	FormatVersion       int                 `json:"formatVersion"`
	Digest              string              `json:"digest"`
	Manifest            Manifest            `json:"manifest"`
	Baseline            Snapshot            `json:"baseline"`
	Inputs              []File              `json:"inputs"`
	TargetTrack         *pub.Track          `json:"targetTrack"`
	TargetListings      map[string]*Listing `json:"targetListings"`
	TargetImages        []PlannedImages     `json:"targetImages"`
	Diff                []Diff              `json:"diff"`
	RemovedVersionCodes []int64             `json:"removedVersionCodes"`
	Preflight           []Check             `json:"preflight"`
}
type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}
type Outcome struct {
	EditID       string   `json:"editId"`
	Committed    bool     `json:"committed"`
	ReviewStatus string   `json:"reviewStatus"`
	PlanDigest   string   `json:"planDigest"`
	Warnings     []string `json:"warnings"`
}

type API interface {
	publisher.API
	CommitWithOptions(context.Context, string, string, publisher.CommitOptions) error
	Listings(context.Context, string, string) ([]*pub.Listing, error)
	PutListing(context.Context, string, string, string, *pub.Listing) error
	Images(context.Context, string, string, string, string) ([]*pub.Image, error)
	DeleteImages(context.Context, string, string, string, string) error
	UploadImage(context.Context, string, string, string, string, io.Reader) (*pub.Image, error)
	Symbols(context.Context, string, string, int64, string, io.Reader) error
}
type Service struct {
	API API
	Now func() time.Time
}

func key(locale, kind string) string { return locale + "/" + kind }
func remoteListing(v *Listing, locale string) *pub.Listing {
	return &pub.Listing{Language: locale, Title: v.Title, ShortDescription: v.ShortDescription, FullDescription: v.FullDescription, Video: v.Video}
}
func localListing(v *pub.Listing) *Listing {
	return &Listing{Title: v.Title, ShortDescription: v.ShortDescription, FullDescription: v.FullDescription, Video: v.Video}
}
