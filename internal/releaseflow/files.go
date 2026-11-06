package releaseflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
	"playops/internal/fault"
)

func Read(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fault.Input("Cannot read input file: %s", path)
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if err = d.Decode(v); err != nil {
		return fault.Input("Invalid YAML/JSON document: %s", path)
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fault.Input("Expected one document in %s", path)
	}
	return nil
}
func ReadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fault.Input("Cannot read JSON file: %s", path)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return fault.Input("Invalid JSON document: %s", path)
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fault.Input("Expected one JSON document in %s", path)
	}
	return nil
}
func Write(path string, v any) error {
	if err := CheckOutput(path); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(b, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
func CheckOutput(path string) error {
	if path == "" {
		return fault.Input("--out is required")
	}
	// Never overwrite a manifest, source asset or previously reviewed plan.
	if _, err := os.Stat(path); err == nil {
		return fault.Input("Output already exists; choose a new path: %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}
func HashFile(path string) (File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return File{}, err
	}
	f, err := os.Open(absolute)
	if err != nil {
		return File{}, fault.Input("Cannot open input file: %s", absolute)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return File{}, fault.Input("Input must be a regular file: %s", absolute)
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return File{}, err
	}
	return File{Path: absolute, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}
func sum(v any) string {
	// Canonicalize objects, including interface-valued diffs: after loading JSON
	// those values become maps whose field order differs from Go structs.
	b, _ := json.Marshal(v)
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var canonical any
	if d.Decode(&canonical) != nil {
		return ""
	}
	b, _ = json.Marshal(canonical)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (p Plan) digest() string { p.Digest = ""; return sum(p) }
func LoadPlan(path string) (Plan, error) {
	var p Plan
	if err := ReadJSON(path, &p); err != nil {
		return p, err
	}
	if p.FormatVersion != 1 || p.Digest == "" || p.Digest != p.digest() {
		return p, fault.Input("Unsupported or modified plan; generate it again from the manifest and snapshot")
	}
	return p, nil
}
func LoadSnapshot(path string) (Snapshot, error) {
	var s Snapshot
	if err := ReadJSON(path, &s); err != nil {
		return s, err
	}
	if s.FormatVersion != 1 || s.Track == nil || s.Listings == nil || s.Images == nil {
		return s, fault.Input("Unsupported or incomplete snapshot")
	}
	return s, nil
}
func fileMap(files []File) map[string]File {
	m := map[string]File{}
	for _, f := range files {
		m[f.Path] = f
	}
	return m
}
func orderedFiles(m map[string]File) []File {
	keys := []string{}
	for path := range m {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	out := []File{}
	for _, path := range keys {
		out = append(out, m[path])
	}
	return out
}

// Copy and verify inputs before opening an edit. Uploads then use the private
// staged copies, so an input changing mid-operation cannot alter the payload.
func stage(p Plan) (map[string]string, func(), error) {
	dir, err := os.MkdirTemp("", "playops-apply-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	staged := map[string]string{}
	for i, expected := range p.Inputs {
		in, e := os.Open(expected.Path)
		if e != nil {
			cleanup()
			return nil, nil, fault.Conflict("Plan input disappeared: %s", expected.Path)
		}
		info, e := in.Stat()
		if e != nil || !info.Mode().IsRegular() {
			in.Close()
			cleanup()
			return nil, nil, fault.Conflict("Plan input is no longer a regular file")
		}
		outpath := filepath.Join(dir, fmt.Sprintf("%06d", i)+filepath.Ext(expected.Path))
		out, e := os.OpenFile(outpath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			in.Close()
			cleanup()
			return nil, nil, e
		}
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(out, h), in)
		_ = in.Close()
		closeErr := out.Close()
		if e != nil || closeErr != nil || n != expected.Size || hex.EncodeToString(h.Sum(nil)) != expected.SHA256 {
			cleanup()
			return nil, nil, fault.Conflict("Plan input changed: %s", expected.Path)
		}
		staged[expected.Path] = outpath
	}
	return staged, cleanup, nil
}
