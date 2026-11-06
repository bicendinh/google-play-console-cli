package releaseflow

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestAssetDimensionsAndAlpha(t *testing.T) {
	for _, v := range []struct {
		name, kind    string
		width, height int
		alpha         uint8
		valid         bool
	}{{"feature", "featureGraphic", 1024, 500, 255, true}, {"feature-alpha", "featureGraphic", 1024, 500, 128, false}, {"wrong-size", "featureGraphic", 320, 640, 255, false}, {"phone", "phoneScreenshots", 320, 640, 255, true}, {"too-tall", "phoneScreenshots", 320, 1000, 255, false}} {
		t.Run(v.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "asset.png")
			im := image.NewRGBA(image.Rect(0, 0, v.width, v.height))
			for i := 3; i < len(im.Pix); i += 4 {
				im.Pix[i] = v.alpha
			}
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = png.Encode(f, im); err != nil {
				t.Fatal(err)
			}
			_ = f.Close()
			err = ValidateImage(path, v.kind)
			if (err == nil) != v.valid {
				t.Fatalf("validation %v, expected valid=%v", err, v.valid)
			}
		})
	}
}
