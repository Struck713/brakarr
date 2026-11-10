package web

import (
	"cmp"
	"context"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Struck713/brakarr/internal/arr"
	"github.com/Struck713/brakarr/internal/config"
	"github.com/Struck713/brakarr/internal/media"
	"github.com/Struck713/brakarr/internal/pool"
	"github.com/Struck713/brakarr/internal/web/templates"
	"github.com/a-h/templ"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

const pageSize = 50

//go:embed static
var staticFiles embed.FS

type Store interface {
	List(ctx context.Context, limit, offset int) ([]media.Media, error)
	Count(ctx context.Context) (int, error)
	Retry(ctx context.Context, hash []byte) (bool, error)
}

type Web struct {
	db       Store
	pool     *pool.Pool
	settings *config.Store
}

func NewWeb(db Store, pool *pool.Pool, settings *config.Store) *Web {
	return &Web{db: db, pool: pool, settings: settings}
}

func Render(c *echo.Context, statusCode int, t templ.Component) error {
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	c.Response().WriteHeader(statusCode)
	return t.Render(c.Request().Context(), c.Response())
}

func (w *Web) Routes() *echo.Echo {
	e := echo.New()

	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())

	e.StaticFS("/static", echo.MustSubFS(staticFiles, "static"))

	e.GET("/", func(c *echo.Context) error {
		list, err := w.mediaList(c, pageParam(c))
		if err != nil {
			return err
		}
		return Render(c, http.StatusOK, templates.LibraryPage(list))
	})

	e.GET("/activity", func(c *echo.Context) error {
		return Render(c, http.StatusOK, templates.ActivityPage(w.activeJobs()))
	})

	e.GET("/media", func(c *echo.Context) error {
		return w.mediaPanel(c, pageParam(c))
	})

	e.POST("/media/:hash/retry", func(c *echo.Context) error {
		hash, err := hex.DecodeString(c.Param("hash"))
		if err != nil || len(hash) == 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid hash")
		}
		if _, err := w.db.Retry(c.Request().Context(), hash); err != nil {
			return err
		}
		w.pool.Notify()
		return w.mediaPanel(c, pageParam(c))
	})

	e.GET("/jobs", func(c *echo.Context) error {
		return Render(c, http.StatusOK, templates.ActiveJobs(w.activeJobs()))
	})

	e.GET("/settings", func(c *echo.Context) error {
		return Render(c, http.StatusOK, templates.SettingsPage(templates.SettingsView{
			Config: w.settings.Get(), Path: w.settings.Path(),
		}))
	})

	e.POST("/settings", func(c *echo.Context) error {
		cfg, err := w.settingsFromForm(c)
		view := templates.SettingsView{Config: cfg, Path: w.settings.Path()}
		if err == nil {
			err = w.settings.Save(cfg)
		}
		status := http.StatusOK
		if err != nil {
			status = http.StatusUnprocessableEntity
			view.Errors = splitErrors(err)
		} else {
			view.Config, view.Saved = w.settings.Get(), true
		}
		return Render(c, status, templates.SettingsPage(view))
	})

	return e
}

// settingsFromForm overlays the submitted form on the saved settings. The
// returned config is always usable to redisplay the form.
func (w *Web) settingsFromForm(c *echo.Context) (config.Config, error) {
	cfg := w.settings.Get()
	var errs []error
	text := func(name string) string { return strings.TrimSpace(c.FormValue(name)) }
	integer := func(name, label string, dst *int) {
		n, err := strconv.Atoi(text(name))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s must be a whole number", label))
			return
		}
		*dst = n
	}
	duration := func(name, label string, dst *config.Duration) {
		if err := dst.UnmarshalText([]byte(text(name))); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", label, err))
		}
	}

	cfg.CrawlPath = text("crawlPath")
	cfg.Extensions = strings.Split(text("extensions"), ",")
	duration("crawlInterval", "scan interval", &cfg.CrawlInterval)
	duration("minFileAge", "minimum file age", &cfg.MinFileAge)
	cfg.SkipHardlinked = c.FormValue("skipHardlinked") != ""
	cfg.VaapiDevice = text("vaapiDevice")
	integer("quality", "quality", &cfg.Quality)
	integer("workers", "workers", &cfg.Workers)
	cfg.WorkDir = text("workDir")
	cfg.ListenAddr = text("listenAddr")
	cfg.LogLevel = text("logLevel")
	cfg.DBPath = text("dbPath")

	cfg.Sonarr.URL = text("sonarrUrl")
	cfg.Radarr.URL = text("radarrUrl")
	// Keys are never sent to the browser, so a blank field keeps the saved one.
	if k := text("sonarrApiKey"); k != "" {
		cfg.Sonarr.APIKey = k
	}
	if k := text("radarrApiKey"); k != "" {
		cfg.Radarr.APIKey = k
	}
	if cfg.Sonarr.URL == "" {
		cfg.Sonarr.APIKey = ""
	}
	if cfg.Radarr.URL == "" {
		cfg.Radarr.APIKey = ""
	}

	cfg.ArrPathMap = nil
	for _, line := range strings.Split(c.FormValue("arrPathMap"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		local, remote, ok := strings.Cut(line, "=")
		if !ok {
			errs = append(errs, fmt.Errorf("path mapping %q must look like brakarr-path=app-path", strings.TrimSpace(line)))
			continue
		}
		cfg.ArrPathMap = append(cfg.ArrPathMap, arr.PathMap{Local: local, Remote: remote})
	}

	cfg.Normalize()
	return cfg, errors.Join(errs...)
}

// splitErrors flattens a joined error into one message per line.
func splitErrors(err error) []string {
	var out []string
	for _, line := range strings.Split(err.Error(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func (w *Web) Run(ctx context.Context, addr string) error {
	return echo.StartConfig{Address: addr}.Start(ctx, w.Routes())
}

func (w *Web) mediaList(c *echo.Context, page int) (templates.MediaList, error) {
	ctx := c.Request().Context()

	total, err := w.db.Count(ctx)
	if err != nil {
		return templates.MediaList{}, err
	}
	pages := max((total+pageSize-1)/pageSize, 1)
	page = min(max(page, 1), pages)

	items, err := w.db.List(ctx, pageSize, (page-1)*pageSize)
	if err != nil {
		return templates.MediaList{}, err
	}
	return templates.MediaList{Items: items, Page: page, Pages: pages, Total: total}, nil
}

func (w *Web) mediaPanel(c *echo.Context, page int) error {
	list, err := w.mediaList(c, page)
	if err != nil {
		return err
	}
	return Render(c, http.StatusOK, templates.MediaPanel(list))
}

func (w *Web) activeJobs() []*pool.Job {
	jobs := w.pool.Jobs()
	slices.SortFunc(jobs, func(a, b *pool.Job) int {
		return cmp.Compare(a.Media.Name, b.Media.Name)
	})
	return jobs
}

func pageParam(c *echo.Context) int {
	n, err := strconv.Atoi(c.QueryParam("page"))
	if err != nil {
		return 1
	}
	return n
}
