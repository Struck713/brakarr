// Package config loads runtime settings from the environment (and an optional .env file).
package config

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	CrawlPath        string
	Extensions       []string
	CrawlInterval    time.Duration
	MinFileAge       time.Duration
	WorkDir          string
	DBPath           string
	VaapiDevice      string
	Quality          int
	Workers          int
	ListenAddr       string
	ReplaceOriginals bool
	LogLevel         slog.Level
}

func Load() (*Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return nil, err
	}
	return fromEnv(os.Getenv)
}

func fromEnv(get func(string) string) (*Config, error) {
	var errs []error
	str := func(key, def string) string {
		if v := strings.TrimSpace(get(key)); v != "" {
			return v
		}
		return def
	}
	integer := func(key string, def int) int {
		v := str(key, "")
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
			return def
		}
		return n
	}
	duration := func(key string, def time.Duration) time.Duration {
		v := str(key, "")
		if v == "" {
			return def
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
			return def
		}
		return d
	}
	boolean := func(key string) bool {
		b, _ := strconv.ParseBool(str(key, ""))
		return b
	}

	c := &Config{
		CrawlPath:        str("CRAWL_PATH", ""),
		CrawlInterval:    duration("CRAWL_INTERVAL", 10*time.Minute),
		MinFileAge:       duration("MIN_FILE_AGE", 30*time.Second),
		WorkDir:          str("WORK_DIR", "./work"),
		DBPath:           str("DB_PATH", "app.db"),
		VaapiDevice:      str("VAAPI_DEVICE", "/dev/dri/renderD128"),
		Quality:          integer("QUALITY", 22),
		Workers:          integer("WORKERS", 1),
		ListenAddr:       str("LISTEN_ADDR", "127.0.0.1:1323"),
		ReplaceOriginals: boolean("REPLACE_ORIGINALS"),
	}

	for _, ext := range strings.Split(str("CRAWL_EXTENSIONS", ".mkv"), ",") {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		c.Extensions = append(c.Extensions, ext)
	}

	if err := c.LogLevel.UnmarshalText([]byte(str("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}

	if c.CrawlPath == "" {
		errs = append(errs, errors.New("CRAWL_PATH is required"))
	} else if st, err := os.Stat(c.CrawlPath); err != nil || !st.IsDir() {
		errs = append(errs, fmt.Errorf("CRAWL_PATH %q is not a readable directory", c.CrawlPath))
	}
	if c.Workers < 1 {
		errs = append(errs, errors.New("WORKERS must be >= 1"))
	}
	if c.Quality < 0 || c.Quality > 51 {
		errs = append(errs, errors.New("QUALITY must be between 0 and 51"))
	}
	if len(c.Extensions) == 0 {
		errs = append(errs, errors.New("CRAWL_EXTENSIONS must list at least one extension"))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return c, nil
}

func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, value)
		}
	}
	return sc.Err()
}
