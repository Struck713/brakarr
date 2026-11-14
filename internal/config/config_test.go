package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Struck713/brakarr/internal/arr"
)

// TestNormalize checks the cleanup applied to user input before validation:
//   - text fields are trimmed, and encoder/log level are lower-cased,
//   - extensions are trimmed, lower-cased, given a leading dot, and blanks dropped,
//   - path maps lose trailing slashes and fully blank rows are dropped,
//   - services are trimmed, their type lower-cased, and missing IDs filled in.
func TestNormalize(t *testing.T) {
	c := Config{
		CrawlPath: " /media ", Encoder: " VAAPI ", LogLevel: "DEBUG",
		Extensions: []string{" MKV", ".Mp4", "  "},
		ArrPathMap: []arr.PathMap{{Local: "/media/", Remote: " /tv/ "}, {Local: " ", Remote: ""}},
		Services:   []Service{{Name: " Sonarr ", Type: " Sonarr ", URL: " http://s "}},
	}
	c.Normalize()

	if c.CrawlPath != "/media" || c.Encoder != "vaapi" || c.LogLevel != "debug" {
		t.Errorf("fields: %q %q %q", c.CrawlPath, c.Encoder, c.LogLevel)
	}
	if !reflect.DeepEqual(c.Extensions, []string{".mkv", ".mp4"}) {
		t.Errorf("extensions = %q", c.Extensions)
	}
	if !reflect.DeepEqual(c.ArrPathMap, []arr.PathMap{{Local: "/media", Remote: "/tv"}}) {
		t.Errorf("path map = %+v", c.ArrPathMap)
	}
	s := c.Services[0]
	if s.Name != "Sonarr" || s.Type != "sonarr" || s.URL != "http://s" || s.ID == "" {
		t.Errorf("service = %+v", s)
	}
}

// validConfig returns the defaults with a real crawl directory, which passes Validate.
func validConfig(t *testing.T) Config {
	t.Helper()
	c := Defaults()
	c.CrawlPath = t.TempDir()
	return c
}

// TestValidate starts from a valid config and breaks one field per case,
// checking each rule in Validate produces an error mentioning that field.
// It also checks the defaults pass, that VMAF samples are only checked when
// the VMAF check is on, and that all problems are reported together.
func TestValidate(t *testing.T) {
	valid := validConfig(t)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	svc := Service{ID: "a", Name: "Sonarr", Type: "sonarr", URL: "http://s", APIKey: "k"}
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"no crawl path", func(c *Config) { c.CrawlPath = "" }, "crawl path is required"},
		{"missing crawl dir", func(c *Config) { c.CrawlPath = filepath.Join(c.CrawlPath, "nope") }, "not a readable directory"},
		{"no extensions", func(c *Config) { c.Extensions = nil }, "at least one extension"},
		{"bad extension", func(c *Config) { c.Extensions = []string{".avi"} }, "extension .avi is not supported"},
		{"negative interval", func(c *Config) { c.CrawlInterval = -1 }, "crawl interval"},
		{"negative file age", func(c *Config) { c.MinFileAge = -1 }, "minimum file age"},
		{"no work dir", func(c *Config) { c.WorkDir = "" }, "work directory"},
		{"no db path", func(c *Config) { c.DBPath = "" }, "database path"},
		{"no listen addr", func(c *Config) { c.ListenAddr = "" }, "listen address"},
		{"no workers", func(c *Config) { c.Workers = 0 }, "workers"},
		{"unknown encoder", func(c *Config) { c.Encoder = "qsv" }, "unknown encoder"},
		{"bad nvenc device", func(c *Config) { c.Encoder, c.Device = "nvenc", "gpu" }, "nvenc device"},
		{"quality low", func(c *Config) { c.Quality = 0 }, "quality must be between"},
		{"quality high", func(c *Config) { c.Quality = 52 }, "quality must be between"},
		{"vmaf high", func(c *Config) { c.MinVMAF = 101 }, "minimum VMAF"},
		{"vmaf samples", func(c *Config) { c.VMAFSamples = 11 }, "VMAF samples"},
		{"log level", func(c *Config) { c.LogLevel = "loud" }, "log level"},
		{"path map half", func(c *Config) { c.ArrPathMap = []arr.PathMap{{Local: "/a"}} }, "path mapping"},
		{"service no name", func(c *Config) { s := svc; s.Name = ""; c.Services = []Service{s} }, "name is required"},
		{"service type", func(c *Config) { s := svc; s.Type = "lidarr"; c.Services = []Service{s} }, "unknown type"},
		{"service url", func(c *Config) { s := svc; s.URL = ""; c.Services = []Service{s} }, "URL is required"},
		{"service key", func(c *Config) { s := svc; s.APIKey = ""; c.Services = []Service{s} }, "API key is required"},
		{"duplicate name", func(c *Config) { s := svc; s.ID, s.Name = "b", "sonarr"; c.Services = []Service{svc, s} }, "name is already used"},
		{"duplicate id", func(c *Config) { s := svc; s.Name = "Other"; c.Services = []Service{svc, s} }, "duplicate service ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig(t)
			tt.mutate(&c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate = %v; want error containing %q", err, tt.want)
			}
		})
	}

	t.Run("samples ignored when vmaf off", func(t *testing.T) {
		c := validConfig(t)
		c.MinVMAF, c.VMAFSamples = 0, 0
		if err := c.Validate(); err != nil {
			t.Errorf("Validate = %v; want nil", err)
		}
	})
	t.Run("all errors reported", func(t *testing.T) {
		c := validConfig(t)
		c.Workers, c.Quality = 0, 0
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "workers") || !strings.Contains(err.Error(), "quality") {
			t.Errorf("Validate = %v; want both workers and quality errors", err)
		}
	})
}

// TestWriteReadRoundTrip checks that a config written with Write reads back
// identically, at the current version (so no rewrite is requested), and that
// Write creates missing parent directories.
func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	c := validConfig(t)
	c.CrawlInterval = Duration(90 * time.Second)
	c.Services = []Service{{ID: "a", Name: "Sonarr", Type: "sonarr", URL: "http://s", APIKey: "k", Enabled: true}}
	if err := Write(path, c); err != nil {
		t.Fatal(err)
	}
	got, rewrite, err := read(path)
	if err != nil {
		t.Fatal(err)
	}
	if rewrite {
		t.Error("freshly written file flagged for rewrite")
	}
	if !reflect.DeepEqual(got, c) {
		t.Errorf("round trip:\n got  %+v\n want %+v", got, c)
	}
}

// TestDuration checks Duration's text form: it marshals like time.Duration
// ("1m30s"), parses with surrounding whitespace, and rejects bad input.
func TestDuration(t *testing.T) {
	b, _ := Duration(90 * time.Second).MarshalText()
	if string(b) != "1m30s" {
		t.Errorf("MarshalText = %s", b)
	}
	var d Duration
	if err := d.UnmarshalText([]byte(" 5m ")); err != nil || d != Duration(5*time.Minute) {
		t.Errorf("UnmarshalText = %v, %v", d, err)
	}
	if err := d.UnmarshalText([]byte("soon")); err == nil {
		t.Error("UnmarshalText(soon) succeeded; want error")
	}
}
