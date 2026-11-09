package crawler

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Struck713/brakarr/internal/media"
)

type Store interface {
	Get(ctx context.Context, hash []byte) (*media.Media, error)
	Insert(ctx context.Context, m *media.Media) (bool, error)
}

type Options struct {
	Root           string
	Extensions     []string
	Interval       time.Duration
	MinAge         time.Duration
	SkipHardlinked bool
	OnNew          func()
}

type Crawler struct {
	store Store
	opts  Options
	hash  func(path string) ([]byte, error)
	probe func(path string, hash []byte) (*media.Media, error)
	now   func() time.Time
}

func New(store Store, opts Options) *Crawler {
	return &Crawler{
		store: store,
		opts:  opts,
		hash:  media.Hash,
		probe: media.Probe,
		now:   time.Now,
	}
}

func (c *Crawler) Run(ctx context.Context) {
	for {
		c.scan(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.opts.Interval):
		}
	}
}

func (c *Crawler) scan(ctx context.Context) {
	slog.Info("crawl started", "root", c.opts.Root)
	added := 0
	err := filepath.WalkDir(c.opts.Root, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if path == c.opts.Root {
				return err
			}
			slog.Warn("crawl: skipping unreadable path", "path", path, "error", err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !c.wanted(path) {
			return nil
		}

		isNew, err := c.handle(ctx, path, d)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Warn("crawl: file skipped", "path", path, "error", err)
			return nil
		}
		if isNew {
			added++
		}
		return nil
	})

	switch {
	case errors.Is(err, context.Canceled):
		slog.Info("crawl cancelled")
	case err != nil:
		slog.Error("crawl failed", "root", c.opts.Root, "error", err)
	default:
		slog.Info("crawl finished", "root", c.opts.Root, "added", added)
	}
	if added > 0 && c.opts.OnNew != nil {
		c.opts.OnNew()
	}
}

func (c *Crawler) wanted(path string) bool {
	return slices.Contains(c.opts.Extensions, strings.ToLower(filepath.Ext(path)))
}

func (c *Crawler) handle(ctx context.Context, path string, d fs.DirEntry) (bool, error) {
	info, err := d.Info()
	if err != nil {
		return false, err
	}
	if age := c.now().Sub(info.ModTime()); age < c.opts.MinAge {
		slog.Debug("crawl: file still changing, will retry next scan", "path", path, "age", age)
		return false, nil
	}

	name := filepath.Base(path)
	hash, err := c.hash(path)
	if err != nil {
		return false, fmt.Errorf("hash: %w", err)
	}

	known, err := c.store.Get(ctx, hash)
	if err != nil {
		return false, err
	}
	if known != nil {
		return false, nil
	}

	m, err := c.probe(path, hash)
	if m != nil && m.Status == "" {
		m.Status = media.StatusPending
	}
	switch {
	case errors.Is(err, media.ErrNoVideo):
		m.Status, m.Error = media.StatusFailed, err.Error()
	case err != nil:
		return false, err
	case m.IsHEVC():
		m.Status, m.Error = media.StatusSkipped, "already HEVC"
	}

	inserted, err := c.store.Insert(ctx, m)
	if err != nil {
		return false, err
	}
	if inserted {
		slog.Info("new media", "name", name, "hash", fmt.Sprintf("%x", hash), "format", m.Format, "status", m.Status)
	}
	return inserted && m.Status == media.StatusPending, nil
}
