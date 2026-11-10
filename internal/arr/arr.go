package arr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const flushInterval = time.Minute

type App struct {
	Name, BaseURL, APIKey      string
	ListPath, Command, IDField string
}

func Sonarr(url, key string) App {
	return App{"sonarr", strings.TrimRight(url, "/"), key, "/api/v3/series", "RescanSeries", "seriesId"}
}

func Radarr(url, key string) App {
	return App{"radarr", strings.TrimRight(url, "/"), key, "/api/v3/movie", "RescanMovie", "movieId"}
}

type PathMap struct {
	Local  string `json:"local"`
	Remote string `json:"remote"`
}

type Notifier struct {
	apps   []App
	maps   []PathMap
	client *http.Client

	mu      sync.Mutex
	pending map[string]struct{}
}

func NewNotifier(apps []App, maps []PathMap) *Notifier {
	return &Notifier{apps: apps, maps: maps,
		client: &http.Client{Timeout: 30 * time.Second}, pending: map[string]struct{}{}}
}

func (n *Notifier) Replaced(path string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.pending[n.remote(path)] = struct{}{}
}

func (n *Notifier) Run(ctx context.Context) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.flush(ctx)
		}
	}
}

func (n *Notifier) flush(ctx context.Context) {
	n.mu.Lock()
	paths := n.pending
	n.pending = map[string]struct{}{}
	n.mu.Unlock()
	if len(paths) == 0 {
		return
	}
	for _, app := range n.apps {
		if err := n.notify(ctx, app, paths); err != nil {
			// Not retried: the app's own scheduled rescan will catch up.
			slog.Warn("arr: notify failed", "app", app.Name, "error", err)
		}
	}
}

type libraryItem struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
}

func (n *Notifier) notify(ctx context.Context, app App, paths map[string]struct{}) error {
	var items []libraryItem
	if err := n.do(ctx, app, http.MethodGet, app.ListPath, nil, &items); err != nil {
		return err
	}
	ids := map[int]struct{}{}
	for _, item := range items {
		prefix := strings.TrimRight(item.Path, "/") + "/"
		for p := range paths {
			if strings.HasPrefix(p, prefix) {
				ids[item.ID] = struct{}{}
				break
			}
		}
	}
	for id := range ids {
		body := map[string]any{"name": app.Command, app.IDField: id}
		if err := n.do(ctx, app, http.MethodPost, "/api/v3/command", body, nil); err != nil {
			return err
		}
		slog.Info("arr: rescan requested", "app", app.Name, "id", id)
	}
	return nil
}

func (n *Notifier) do(ctx context.Context, app App, method, path string, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, app.BaseURL+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", app.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// remote translates a local path to the path the apps see. A mapping only
// applies on a path boundary, so /data/tv does not match /data/tvshows.
func (n *Notifier) remote(path string) string {
	for _, m := range n.maps {
		if rest, ok := strings.CutPrefix(path, m.Local); ok && (rest == "" || rest[0] == '/') {
			return m.Remote + rest
		}
	}
	return path
}
