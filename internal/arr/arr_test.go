package arr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type recorder struct {
	mu       sync.Mutex
	keys     []string
	commands []map[string]any
}

func (r *recorder) commandsCopy() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.commands...)
}

func newServer(t *testing.T, library string, rec *recorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.keys = append(rec.keys, r.Header.Get("X-Api-Key"))
		rec.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series":
			w.Write([]byte(library))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			rec.mu.Lock()
			rec.commands = append(rec.commands, body)
			rec.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const library = `[{"id":1,"path":"/tv/Foo"},{"id":2,"path":"/tv/Foobar"},{"id":3,"path":"/tv/Other/"}]`

func TestOneRescanPerSeriesWithAPIKey(t *testing.T) {
	rec := &recorder{}
	srv := newServer(t, library, rec)
	n := NewNotifier([]App{Sonarr(srv.URL+"/", "secret")}, nil)
	n.Replaced("/tv/Foo/Season 1/e1.mkv")
	n.Replaced("/tv/Foo/Season 1/e2.mkv")
	n.Replaced("/tv/Foo/Season 2/e1.mkv")
	n.flush(context.Background())

	cmds := rec.commandsCopy()
	if len(cmds) != 1 {
		t.Fatalf("commands = %v, want exactly one", cmds)
	}
	if cmds[0]["name"] != "RescanSeries" || cmds[0]["seriesId"] != float64(1) {
		t.Errorf("command = %v", cmds[0])
	}
	for _, k := range rec.keys {
		if k != "secret" {
			t.Errorf("X-Api-Key = %q", k)
		}
	}
}

func TestPrefixMatchesOnPathBoundary(t *testing.T) {
	rec := &recorder{}
	srv := newServer(t, library, rec)
	n := NewNotifier([]App{Sonarr(srv.URL, "k")}, nil)
	n.Replaced("/tv/Foobar/e1.mkv")
	n.Replaced("/tv/Other/e1.mkv") // library path has a trailing slash
	n.flush(context.Background())

	ids := map[float64]bool{}
	for _, c := range rec.commandsCopy() {
		ids[c["seriesId"].(float64)] = true
	}
	if len(ids) != 2 || !ids[2] || !ids[3] || ids[1] {
		t.Errorf("rescanned ids = %v, want 2 and 3 only", ids)
	}
}

func TestPathMapIsApplied(t *testing.T) {
	rec := &recorder{}
	srv := newServer(t, library, rec)
	n := NewNotifier([]App{Sonarr(srv.URL, "k")}, []PathMap{{Local: "/data/media/tv", Remote: "/tv"}})
	n.Replaced("/data/media/tv/Foo/e1.mkv")
	n.Replaced("/data/media/tvshows/Foo/e1.mkv") // not under the mapped prefix
	n.flush(context.Background())

	cmds := rec.commandsCopy()
	if len(cmds) != 1 || cmds[0]["seriesId"] != float64(1) {
		t.Errorf("commands = %v, want a single rescan of series 1", cmds)
	}
}

func TestServerErrorDoesNotPanicOrBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	n := NewNotifier([]App{Sonarr(srv.URL, "k"), Radarr(srv.URL, "k")}, nil)
	n.Replaced("/tv/Foo/e1.mkv")
	n.flush(context.Background())

	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.pending) != 0 {
		t.Errorf("pending = %v, want the batch dropped after a failed flush", n.pending)
	}
}

func TestRadarrUsesMovieCommand(t *testing.T) {
	app := Radarr("http://x/", "k")
	if app.ListPath != "/api/v3/movie" || app.Command != "RescanMovie" || app.IDField != "movieId" || strings.HasSuffix(app.BaseURL, "/") {
		t.Errorf("app = %+v", app)
	}
}
