package cdn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
)

type purgeAPI struct {
	mu       sync.Mutex
	requests [][]string
	auth     []string
	paths    []string
	statuses []int
}

func (a *purgeAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tags []string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		a.requests = append(a.requests, body.Tags)
		a.auth = append(a.auth, r.Header.Get("Authorization"))
		a.paths = append(a.paths, r.Method+" "+r.URL.Path)
		status := http.StatusOK
		if len(a.statuses) > 0 {
			status, a.statuses = a.statuses[0], a.statuses[1:]
		}
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"success":%t}`, status == http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFlush(t *testing.T) {
	t.Run("sends tags to the zone purge endpoint with the bearer token", func(t *testing.T) {
		api := &purgeAPI{}
		p := New("zone123", "secret", api.server(t).URL)
		p.Enqueue("s-a", "k-b")

		if err := p.Flush(context.Background()); err != nil {
			t.Fatalf("flush: %v", err)
		}
		if len(api.requests) != 1 || !slices.Equal(api.requests[0], []string{"s-a", "k-b"}) {
			t.Fatalf("requests = %v", api.requests)
		}
		if api.auth[0] != "Bearer secret" {
			t.Fatalf("auth = %q", api.auth[0])
		}
		if api.paths[0] != "POST /zones/zone123/purge_cache" {
			t.Fatalf("path = %q", api.paths[0])
		}
		if p.Pending() != 0 {
			t.Fatalf("pending = %d after success", p.Pending())
		}
	})

	t.Run("nothing pending sends nothing", func(t *testing.T) {
		api := &purgeAPI{}
		p := New("zone", "token", api.server(t).URL)
		if err := p.Flush(context.Background()); err != nil {
			t.Fatalf("flush: %v", err)
		}
		if len(api.requests) != 0 {
			t.Fatalf("requests = %v", api.requests)
		}
	})
}

func TestEnqueue(t *testing.T) {
	t.Run("deduplicates tags", func(t *testing.T) {
		api := &purgeAPI{}
		p := New("zone", "token", api.server(t).URL)
		p.Enqueue("s-a", "s-a", "k-b")
		p.Enqueue("k-b", "")

		if err := p.Flush(context.Background()); err != nil {
			t.Fatalf("flush: %v", err)
		}
		if !slices.Equal(api.requests[0], []string{"s-a", "k-b"}) {
			t.Fatalf("tags = %v", api.requests[0])
		}
	})

	t.Run("splits more than 100 tags across requests", func(t *testing.T) {
		api := &purgeAPI{}
		p := New("zone", "token", api.server(t).URL)
		for i := range 250 {
			p.Enqueue(fmt.Sprintf("s-%d", i))
		}

		if err := p.Flush(context.Background()); err != nil {
			t.Fatalf("flush: %v", err)
		}
		if len(api.requests) != 3 || len(api.requests[0]) != 100 || len(api.requests[2]) != 50 {
			t.Fatalf("batch sizes wrong: %d requests", len(api.requests))
		}
	})

	t.Run("drops tags beyond the queue cap", func(t *testing.T) {
		p := New("zone", "token", "http://unused")
		for i := range maxPending + 10 {
			p.Enqueue(fmt.Sprintf("s-%d", i))
		}
		if p.Pending() != maxPending {
			t.Fatalf("pending = %d, want %d", p.Pending(), maxPending)
		}
	})
}

func TestFlushFailure(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(fmt.Sprintf("requeues tags after %d and retries next flush", status), func(t *testing.T) {
			api := &purgeAPI{statuses: []int{status}}
			p := New("zone", "token", api.server(t).URL)
			p.Enqueue("s-a")

			if err := p.Flush(context.Background()); err == nil {
				t.Fatal("expected error")
			}
			if p.Pending() != 1 {
				t.Fatalf("pending = %d after failure", p.Pending())
			}
			if err := p.Flush(context.Background()); err != nil {
				t.Fatalf("retry flush: %v", err)
			}
			if len(api.requests) != 2 || !slices.Equal(api.requests[1], []string{"s-a"}) {
				t.Fatalf("requests = %v", api.requests)
			}
		})
	}

	t.Run("unreachable API keeps tags pending", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		p := New("zone", "token", url)
		p.Enqueue("s-a")

		if err := p.Flush(context.Background()); err == nil {
			t.Fatal("expected error")
		}
		if p.Pending() != 1 {
			t.Fatalf("pending = %d", p.Pending())
		}
	})
}

func TestPurgeUnconfigured(t *testing.T) {
	t.Run("package-level Purge is a no-op before Start", func(t *testing.T) {
		Purge("s-a")
	})
}
