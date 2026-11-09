// Package config loads runtime settings from the environment (and an optional .env file).
package config

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Struck713/brakarr/internal/arr"
)

type Config struct {
	CrawlPath      string
	Extensions     []string
	CrawlInterval  time.Duration
	MinFileAge     time.Duration
	WorkDir        string
	DBPath         string
	VaapiDevice    string
	Quality        int
	Workers        int
	ListenAddr     string
	SkipHardlinked bool
	LogLevel       slog.Level
	Arr            []arr.App
	ArrPathMap     []arr.PathMap
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
	boolean := func(key string, def bool) bool {
		v := str(key, "")
		if v == "" {
			return def
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
			return def
		}
		return b
	}

	c := &Config{
		CrawlPath:      str("CRAWL_PATH", ""),
		CrawlInterval:  duration("CRAWL_INTERVAL", 10*time.Minute),
		MinFileAge:     duration("MIN_FILE_AGE", 30*time.Second),
		WorkDir:        str("WORK_DIR", "./work"),
		DBPath:         str("DB_PATH", "app.db"),
		VaapiDevice:    str("VAAPI_DEVICE", "/dev/dri/renderD128"),
		Quality:        integer("QUALITY", 22),
		Workers:        integer("WORKERS", 1),
		ListenAddr:     str("LISTEN_ADDR", "127.0.0.1:1323"),
		SkipHardlinked: boolean("SKIP_HARDLINKED", true),
	}

	// Do not add .m4v: ffmpeg treats that extension as a raw MPEG-4 stream.
	// .avi is not suitable for HEVC.
	for _, ext := range strings.Split(str("CRAWL_EXTENSIONS", ".mkv,.mp4"), ",") {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		if ext != ".mkv" && ext != ".mp4" {
			errs = append(errs, fmt.Errorf("CRAWL_EXTENSIONS: %s is not supported (use .mkv or .mp4)", ext))
			continue
		}
		c.Extensions = append(c.Extensions, ext)
	}

	for _, app := range []struct {
		name, prefix string
		build        func(url, key string) arr.App
	}{
		{"Sonarr", "SONARR", arr.Sonarr},
		{"Radarr", "RADARR", arr.Radarr},
	} {
		url, key := str(app.prefix+"_URL", ""), str(app.prefix+"_API_KEY", "")
		switch {
		case url == "" && key == "":
		case url == "" || key == "":
			errs = append(errs, fmt.Errorf("%s_URL and %s_API_KEY must be set together", app.prefix, app.prefix))
		default:
			c.Arr = append(c.Arr, app.build(url, key))
		}
	}
	maps, err := parsePathMap(str("ARR_PATH_MAP", ""))
	if err != nil {
		errs = append(errs, err)
	}
	c.ArrPathMap = maps

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
		value = parseValue(value)
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, value)
		}
	}
	return sc.Err()
}

var inlineComment = regexp.MustCompile(`\s+#.*$`)

// parseValue unquotes a value, or strips a trailing " # comment" from an unquoted one.
func parseValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 1 && (raw[0] == '"' || raw[0] == '\'') {
		if end := strings.IndexByte(raw[1:], raw[0]); end >= 0 {
			return raw[1 : end+1]
		}
	}
	return strings.TrimSpace(inlineComment.ReplaceAllString(raw, ""))
}

// parsePathMap parses "local=remote[,local=remote]".
func parsePathMap(raw string) ([]arr.PathMap, error) {
	var maps []arr.PathMap
	for _, pair := range strings.Split(raw, ",") {
		if strings.TrimSpace(pair) == "" {
			continue
		}
		local, remote, ok := strings.Cut(pair, "=")
		local, remote = strings.TrimRight(strings.TrimSpace(local), "/"), strings.TrimRight(strings.TrimSpace(remote), "/")
		if !ok || local == "" || remote == "" {
			return nil, fmt.Errorf("ARR_PATH_MAP: %q must look like local=remote", strings.TrimSpace(pair))
		}
		maps = append(maps, arr.PathMap{Local: local, Remote: remote})
	}
	return maps, nil
}
