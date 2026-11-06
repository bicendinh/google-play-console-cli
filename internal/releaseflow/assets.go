package releaseflow

import (
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"

	"playops/internal/fault"
)

func ValidateImage(path, kind string) error {
	f, err := os.Open(path)
	if err != nil {
		return fault.Input("Cannot open image: %s", path)
	}
	defer f.Close()
	c, format, err := image.DecodeConfig(f)
	if err != nil || (format != "png" && format != "jpeg") {
		return fault.Input("Image must be a valid PNG or JPEG: %s", path)
	}
	if c.Width < 1 || c.Height < 1 || c.Width > 3840 || c.Height > 3840 {
		return fault.Input("Image dimensions outside supported limits: %s", path)
	}
	switch kind {
	case "featureGraphic":
		if c.Width != 1024 || c.Height != 500 {
			return fault.Input("featureGraphic must be 1024x500")
		}
	case "icon":
		if c.Width != 512 || c.Height != 512 || format != "png" {
			return fault.Input("icon must be a 512x512 PNG")
		}
	case "tvBanner":
		if c.Width != 1280 || c.Height != 720 {
			return fault.Input("tvBanner must be 1280x720")
		}
	default:
		if !strings.HasSuffix(kind, "Screenshots") {
			return fault.Input("Unsupported image type")
		}
		if c.Width < 320 || c.Height < 320 || c.Width > 2*c.Height || c.Height > 2*c.Width {
			return fault.Input("Screenshots require dimensions 320–3840 and aspect ratio at most 2:1")
		}
	}
	if format == "png" && kind != "icon" {
		_, _ = f.Seek(0, 0)
		header := make([]byte, 26)
		if _, err = f.Read(header); err != nil || header[24] != 8 || header[25] != 2 {
			return fault.Input("Screenshot/graphic PNG must be 24-bit RGB without an alpha channel")
		}
	}
	_, _ = f.Seek(0, 0)
	if _, _, err = image.Decode(f); err != nil {
		return fault.Input("Image data is truncated or invalid: %s", path)
	}
	return nil
}
func imageCount(kind string, count int) error {
	if strings.HasSuffix(kind, "Screenshots") {
		min := 1
		if kind == "phoneScreenshots" {
			min = 2
		}
		if count < min || count > 8 {
			return fault.Input("%s requires %d–8 images", kind, min)
		}
	} else if count != 1 {
		return fault.Input("%s requires exactly one image", kind)
	}
	return nil
}
