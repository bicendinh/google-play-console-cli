package releaseflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"os"
	"strings"

	"playops/internal/fault"
)

func AlphaPolicy(policy string) error {
	if policy != "" && policy != "auto" && policy != "never" && policy != "error" {
		return fault.Input("flattenAlpha/--flatten-alpha must be auto, never or error")
	}
	return nil
}

// ImageFile describes the upload bytes while retaining the original input hash.
// Originals are never modified. never/error both require a compliant source.
func ImageFile(path, kind, policy string) (File, error) {
	if err := AlphaPolicy(policy); err != nil {
		return File{}, err
	}
	source, err := HashFile(path)
	if err != nil {
		return File{}, err
	}
	data, err := imagePayload(path, kind, policy)
	if err != nil {
		return File{}, err
	}
	if data == nil {
		return source, nil
	}
	h := sha256.Sum256(data)
	return File{Path: source.Path, SHA256: hex.EncodeToString(h[:]), Size: int64(len(data)), SourceSHA256: source.SHA256, SourceSize: source.Size, Transform: "opaque-png-rgb-v1"}, nil
}
func imagePayload(path, kind, policy string) ([]byte, error) {
	err := ValidateImage(path, kind)
	if err == nil {
		return nil, nil
	}
	if (policy != "" && policy != "auto") || !strings.Contains(err.Error(), "24-bit RGB") {
		return nil, fault.Input("%s: %s", path, err.Error())
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	if len(b) < 26 || b[24] != 8 {
		return nil, fault.Input("PNG conversion requires 8-bit pixels: %s", path)
	}
	im, _, e := image.Decode(bytes.NewReader(b))
	if e != nil {
		return nil, fault.Input("Invalid PNG data: %s", path)
	}
	for y := im.Bounds().Min.Y; y < im.Bounds().Max.Y; y++ {
		for x := im.Bounds().Min.X; x < im.Bounds().Max.X; x++ {
			_, _, _, a := im.At(x, y).RGBA()
			if a != 65535 {
				return nil, fault.Input("PNG contains transparent pixels: %s", path)
			}
		}
	}
	rgb := image.NewNRGBA(im.Bounds())
	draw.Draw(rgb, rgb.Bounds(), im, im.Bounds().Min, draw.Src)
	var out bytes.Buffer
	if e = png.Encode(&out, rgb); e != nil {
		return nil, e
	}
	if out.Bytes()[25] != 2 {
		return nil, fault.Input("Could not encode 24-bit RGB PNG: %s", path)
	}
	return out.Bytes(), nil
}

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
	im, _, err := image.Decode(f)
	if err != nil {
		return fault.Input("Image data is truncated or invalid: %s", path)
	}
	if format == "png" && kind != "icon" && !fullyOpaque(im) {
		return fault.Input("PNG contains transparent pixels: %s", path)
	}
	return nil
}
func fullyOpaque(im image.Image) bool {
	if opaque, ok := im.(interface{ Opaque() bool }); ok {
		return opaque.Opaque()
	}
	for y := im.Bounds().Min.Y; y < im.Bounds().Max.Y; y++ {
		for x := im.Bounds().Min.X; x < im.Bounds().Max.X; x++ {
			_, _, _, a := im.At(x, y).RGBA()
			if a != 65535 {
				return false
			}
		}
	}
	return true
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
