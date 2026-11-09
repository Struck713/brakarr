package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Struck713/brakarr/internal/media"
	"github.com/Struck713/brakarr/internal/pool"
)

type fakeStore struct{ items []media.Media }

func (f *fakeStore) List(context.Context, int, int) ([]media.Media, error) { return f.items, nil }
func (f *fakeStore) Count(context.Context) (int, error)                    { return len(f.items), nil }
func (f *fakeStore) Retry(context.Context, []byte) (bool, error)           { return true, nil }

func get(t *testing.T, items []media.Media, path string) *httptest.ResponseRecorder {
	t.Helper()
	server := NewWeb(&fakeStore{items}, pool.NewPool(nil, nil, pool.Options{}))
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, rec.Code)
	}
	return rec
}

func assertContains(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q:\n%s", want, body)
		}
	}
}

func TestLibraryPageIsFullDocument(t *testing.T) {
	items := []media.Media{{Hash: []byte{1}, Name: "movie.mkv", Status: media.StatusPending}}
	body := get(t, items, "/").Body.String()
	assertContains(t, body, "<!doctype html>", "/static/brakarr.css", `id="media-panel"`, "movie.mkv", "Pending")
	if !strings.Contains(body, `href="/" aria-current="page"`) {
		t.Errorf("Library is not the current page:\n%s", body)
	}
}

func TestActivityPageMarksItselfCurrent(t *testing.T) {
	body := get(t, nil, "/activity").Body.String()
	assertContains(t, body, `href="/activity" aria-current="page"`, "No active transcodes.", `hx-get="/jobs"`)
}

func TestMediaFragmentHasNoDocument(t *testing.T) {
	body := get(t, nil, "/media").Body.String()
	assertContains(t, body, "No media found yet.")
	if strings.Contains(strings.ToLower(body), "<!doctype") {
		t.Errorf("fragment contains a full document:\n%s", body)
	}
}

func TestStatusesRenderLabels(t *testing.T) {
	items := []media.Media{
		{Hash: []byte{1}, Name: "a.mkv", Status: media.StatusDeferred, Error: "hardlinked (2 links)"},
		{Hash: []byte{2}, Name: "b.mkv", Status: media.StatusFailed, Error: "ffmpeg broke"},
		{Hash: []byte{3}, Name: "c.mkv", Status: media.StatusComplete},
	}
	body := get(t, items, "/media").Body.String()
	assertContains(t, body, "Waiting", "hardlinked (2 links)", "Failed", "ffmpeg broke", "Complete")
	if got := strings.Count(body, "/retry?page="); got != 1 {
		t.Errorf("retry buttons = %d, want 1 (failed rows only)", got)
	}
}

func TestStylesheetIsEmbedded(t *testing.T) {
	rec := get(t, nil, "/static/brakarr.css")
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
	assertContains(t, rec.Body.String(), "--sidebar-width")
}
