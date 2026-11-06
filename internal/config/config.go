package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"playops/internal/fault"
)

type Profile struct {
	App           string `json:"app,omitempty"`
	Credentials   string `json:"credentials,omitempty"`
	ReportsBucket string `json:"reportsBucket,omitempty"`
	Auth          string `json:"auth,omitempty"`
}
type Config struct {
	Profiles map[string]Profile `json:"profiles"`
}

func Path(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if p := os.Getenv("PLAYOPS_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "playops", "config.json"), nil
}
func Load(path string) (Config, error) {
	c := Config{Profiles: map[string]Profile{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fault.Input("Cannot read config file")
	}
	if json.Unmarshal(b, &c) != nil {
		return c, fault.Input("Config file must contain valid JSON")
	}
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	return c, nil
}
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".playops-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func ValidateProfile(name string) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`).MatchString(name) {
		return fault.Input("Invalid profile name")
	}
	return nil
}
func ValidateApp(app string) error {
	if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`).MatchString(app) {
		return fault.Input("Provide a valid package name with --app or config set app")
	}
	return nil
}
func Bucket(value string) (string, error) {
	value = strings.TrimSuffix(strings.TrimPrefix(value, "gs://"), "/")
	if value == "" || strings.ContainsAny(value, "/\\\n\r\t ") {
		return "", fault.Input("reports-bucket must be a bucket name or gs://bucket")
	}
	return value, nil
}
