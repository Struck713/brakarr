package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Struck713/brakarr/internal/arr"
	"github.com/Struck713/brakarr/internal/transcode"
)

// Duration is a time.Duration that serialises as text such as "5m".
type Duration time.Duration

// String returns the duration in time.Duration text form.
func (d Duration) String() string { return time.Duration(d).String() }

// MarshalText encodes the duration as text.
func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText parses a duration from text.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(strings.TrimSpace(string(b)))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Config is the application's user-editable configuration.
type Config struct {
	CrawlPath      string        `json:"crawlPath"`
	Extensions     []string      `json:"extensions"`
	CrawlInterval  Duration      `json:"crawlInterval"`
	MinFileAge     Duration      `json:"minFileAge"`
	WorkDir        string        `json:"workDir"`
	DBPath         string        `json:"dbPath"`
	Encoder        string        `json:"encoder"`
	Device         string        `json:"device"`
	Quality        int           `json:"quality"`
	MinVMAF        int           `json:"minVmaf"`
	VMAFSamples    int           `json:"vmafSamples"`
	Workers        int           `json:"workers"`
	ListenAddr     string        `json:"listenAddr"`
	SkipHardlinked bool          `json:"skipHardlinked"`
	LogLevel       string        `json:"logLevel"`
	Services       []Service     `json:"services"`
	ArrPathMap     []arr.PathMap `json:"arrPathMap"`
}

// Defaults returns the default configuration.
func Defaults() Config {
	return Config{
		Extensions:     []string{".mkv", ".mp4"},
		CrawlInterval:  Duration(10 * time.Minute),
		MinFileAge:     Duration(30 * time.Second),
		WorkDir:        "./work",
		DBPath:         "app.db",
		Encoder:        transcode.DefaultEncoder,
		Quality:        23,
		MinVMAF:        94,
		VMAFSamples:    3,
		Workers:        1,
		ListenAddr:     "127.0.0.1:1323",
		SkipHardlinked: true,
		LogLevel:       "info",
		Services:       []Service{},
		ArrPathMap:     []arr.PathMap{},
	}
}

// Level returns the slog level for the configured log level.
func (c *Config) Level() slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(c.LogLevel)); err != nil {
		return slog.LevelInfo
	}
	return l
}

// Normalize cleans up values such as whitespace and extension formatting.
func (c *Config) Normalize() {
	c.CrawlPath = strings.TrimSpace(c.CrawlPath)
	c.WorkDir = strings.TrimSpace(c.WorkDir)
	c.DBPath = strings.TrimSpace(c.DBPath)
	c.Encoder = strings.ToLower(strings.TrimSpace(c.Encoder))
	c.Device = strings.TrimSpace(c.Device)
	c.ListenAddr = strings.TrimSpace(c.ListenAddr)
	c.LogLevel = strings.ToLower(strings.TrimSpace(c.LogLevel))
	c.normalizeServices()

	exts := make([]string, 0, len(c.Extensions))
	for _, ext := range c.Extensions {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		exts = append(exts, ext)
	}
	c.Extensions = exts

	maps := make([]arr.PathMap, 0, len(c.ArrPathMap))
	for _, m := range c.ArrPathMap {
		m.Local = strings.TrimRight(strings.TrimSpace(m.Local), "/")
		m.Remote = strings.TrimRight(strings.TrimSpace(m.Remote), "/")
		if m.Local == "" && m.Remote == "" {
			continue
		}
		maps = append(maps, m)
	}
	c.ArrPathMap = maps
}

// Validate checks the configuration and returns all problems found.
func (c *Config) Validate() error {
	var errs []error
	if c.CrawlPath == "" {
		errs = append(errs, errors.New("crawl path is required"))
	} else if st, err := os.Stat(c.CrawlPath); err != nil || !st.IsDir() {
		errs = append(errs, fmt.Errorf("crawl path %q is not a readable directory", c.CrawlPath))
	}
	if len(c.Extensions) == 0 {
		errs = append(errs, errors.New("at least one extension is required"))
	}
	for _, ext := range c.Extensions {
		// Do not add .m4v: ffmpeg treats that extension as a raw MPEG-4 stream.
		// .avi is not suitable for HEVC.
		if ext != ".mkv" && ext != ".mp4" {
			errs = append(errs, fmt.Errorf("extension %s is not supported (use .mkv or .mp4)", ext))
		}
	}
	if c.CrawlInterval < 0 {
		errs = append(errs, errors.New("crawl interval must not be negative"))
	}
	if c.MinFileAge < 0 {
		errs = append(errs, errors.New("minimum file age must not be negative"))
	}
	if c.WorkDir == "" {
		errs = append(errs, errors.New("work directory is required"))
	}
	if c.DBPath == "" {
		errs = append(errs, errors.New("database path is required"))
	}
	if c.ListenAddr == "" {
		errs = append(errs, errors.New("listen address is required"))
	}
	if c.Workers < 1 {
		errs = append(errs, errors.New("workers must be at least 1"))
	}
	if _, err := transcode.NewEncoder(c.Encoder, c.Device); err != nil {
		errs = append(errs, err)
	}
	if c.Quality < 1 || c.Quality > 51 {
		errs = append(errs, errors.New("quality must be between 1 and 51"))
	}
	if c.MinVMAF < 0 || c.MinVMAF > 100 {
		errs = append(errs, errors.New("minimum VMAF must be between 0 and 100"))
	}
	if c.MinVMAF > 0 && (c.VMAFSamples < 1 || c.VMAFSamples > 10) {
		errs = append(errs, errors.New("VMAF samples must be between 1 and 10"))
	}
	var l slog.Level
	if err := l.UnmarshalText([]byte(c.LogLevel)); err != nil {
		errs = append(errs, fmt.Errorf("log level: %w", err))
	}
	errs = append(errs, c.validateServices(), c.validatePathMap())
	return errors.Join(errs...)
}

// file is the on-disk shape: the config tagged with its format version.
type file struct {
	Version int `json:"version"`
	Config
}

// Read loads a configuration from the file at path, migrating it from older
// versions as needed.
func Read(path string) (Config, error) {
	c, _, err := read(path)
	return c, err
}

// read loads the configuration at path and reports whether the file is missing
// or out of date and should be rewritten.
func read(path string) (Config, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Defaults(), true, nil
	}
	if err != nil {
		return Defaults(), false, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return Defaults(), false, fmt.Errorf("%s: %w", path, err)
	}
	from, err := migrate(m)
	if err != nil {
		return Defaults(), false, fmt.Errorf("%s: %w", path, err)
	}
	if b, err = json.Marshal(m); err != nil {
		return Defaults(), false, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	f := file{Config: Defaults()}
	if err := dec.Decode(&f); err != nil {
		return Defaults(), false, fmt.Errorf("%s: %w", path, err)
	}
	f.Config.Normalize()
	return f.Config, from < currentVersion, nil
}

// Write saves the configuration to the file at path.
func Write(path string, c Config) error {
	b, err := json.MarshalIndent(file{Version: currentVersion, Config: c}, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
