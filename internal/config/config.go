package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Struck713/brakarr/internal/arr"
)

type Duration time.Duration

func (d Duration) String() string { return time.Duration(d).String() }

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(strings.TrimSpace(string(b)))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

type ArrApp struct {
	URL    string `json:"url"`
	APIKey string `json:"apiKey"`
}

type Config struct {
	CrawlPath      string        `json:"crawlPath"`
	Extensions     []string      `json:"extensions"`
	CrawlInterval  Duration      `json:"crawlInterval"`
	MinFileAge     Duration      `json:"minFileAge"`
	WorkDir        string        `json:"workDir"`
	DBPath         string        `json:"dbPath"`
	VaapiDevice    string        `json:"vaapiDevice"`
	Quality        int           `json:"quality"`
	Workers        int           `json:"workers"`
	ListenAddr     string        `json:"listenAddr"`
	SkipHardlinked bool          `json:"skipHardlinked"`
	LogLevel       string        `json:"logLevel"`
	Sonarr         ArrApp        `json:"sonarr"`
	Radarr         ArrApp        `json:"radarr"`
	ArrPathMap     []arr.PathMap `json:"arrPathMap"`
}

func Defaults() Config {
	return Config{
		Extensions:     []string{".mkv", ".mp4"},
		CrawlInterval:  Duration(10 * time.Minute),
		MinFileAge:     Duration(30 * time.Second),
		WorkDir:        "./work",
		DBPath:         "app.db",
		VaapiDevice:    "/dev/dri/renderD128",
		Quality:        22,
		Workers:        1,
		ListenAddr:     "127.0.0.1:1323",
		SkipHardlinked: true,
		LogLevel:       "info",
		ArrPathMap:     []arr.PathMap{},
	}
}

func (c *Config) Level() slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(c.LogLevel)); err != nil {
		return slog.LevelInfo
	}
	return l
}

func (c *Config) Apps() []arr.App {
	var apps []arr.App
	if c.Sonarr.URL != "" {
		apps = append(apps, arr.Sonarr(c.Sonarr.URL, c.Sonarr.APIKey))
	}
	if c.Radarr.URL != "" {
		apps = append(apps, arr.Radarr(c.Radarr.URL, c.Radarr.APIKey))
	}
	return apps
}

func (c *Config) Normalize() {
	c.CrawlPath = strings.TrimSpace(c.CrawlPath)
	c.WorkDir = strings.TrimSpace(c.WorkDir)
	c.DBPath = strings.TrimSpace(c.DBPath)
	c.VaapiDevice = strings.TrimSpace(c.VaapiDevice)
	c.ListenAddr = strings.TrimSpace(c.ListenAddr)
	c.LogLevel = strings.ToLower(strings.TrimSpace(c.LogLevel))
	c.Sonarr = ArrApp{strings.TrimSpace(c.Sonarr.URL), strings.TrimSpace(c.Sonarr.APIKey)}
	c.Radarr = ArrApp{strings.TrimSpace(c.Radarr.URL), strings.TrimSpace(c.Radarr.APIKey)}

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
	if c.Quality < 0 || c.Quality > 51 {
		errs = append(errs, errors.New("quality must be between 0 and 51"))
	}
	var l slog.Level
	if err := l.UnmarshalText([]byte(c.LogLevel)); err != nil {
		errs = append(errs, fmt.Errorf("log level: %w", err))
	}
	for _, app := range []struct {
		name string
		ArrApp
	}{{"Sonarr", c.Sonarr}, {"Radarr", c.Radarr}} {
		if (app.URL == "") != (app.APIKey == "") {
			errs = append(errs, fmt.Errorf("%s URL and API key must be set together", app.name))
		}
	}
	for _, m := range c.ArrPathMap {
		if m.Local == "" || m.Remote == "" {
			errs = append(errs, errors.New("every path mapping needs both a Brakarr path and an app path"))
			break
		}
	}
	return errors.Join(errs...)
}

func Read(path string) (Config, error) {
	c := Defaults()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Defaults(), fmt.Errorf("%s: %w", path, err)
	}
	c.Normalize()
	return c, nil
}

func Write(path string, c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
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

type Store struct {
	path string
	mu   sync.RWMutex
	cur  Config
}

func Open(path string) (*Store, error) {
	c, err := Read(path)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
		if err := Write(path, c); err != nil {
			return nil, err
		}
	}
	return &Store{path: path, cur: c}, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

func (s *Store) Save(c Config) error {
	c.Normalize()
	if err := c.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := Write(s.path, c); err != nil {
		return err
	}
	s.cur = c
	return nil
}
