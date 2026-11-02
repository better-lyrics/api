package binilink

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestUploader(baseURL string, interval time.Duration) *uploader {
	return &uploader{
		store:     testStore,
		client:    &http.Client{Timeout: 5 * time.Second},
		baseURL:   baseURL,
		key:       "test-key",
		userAgent: "test-agent",
		interval:  interval,
	}
}

func enqueue(t *testing.T, videoID, appleTrackID string) {
	t.Helper()
	if err := testStore.EnqueueBiniLink(context.Background(), videoID, appleTrackID); err != nil {
		t.Fatalf("enqueue %s: %v", videoID, err)
	}
}

func makeDue(t *testing.T, videoID string, attempts int) {
	t.Helper()
	if err := testStore.SetBiniLinkResult(context.Background(), videoID, "pending", attempts, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("make due %s: %v", videoID, err)
	}
}

type ingressServer struct {
	*httptest.Server
	calls atomic.Int32
}

func newIngressServer(t *testing.T, respond func(w http.ResponseWriter, videoID string)) *ingressServer {
	t.Helper()
	s := &ingressServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		var body struct {
			YoutubeID string `json:"youtube_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		respond(w, body.YoutubeID)
	}))
	t.Cleanup(s.Close)
	return s
}

func statusResponder(code int) func(http.ResponseWriter, string) {
	return func(w http.ResponseWriter, _ string) { w.WriteHeader(code) }
}

func assertNear(t *testing.T, got, want time.Time, tolerance time.Duration) {
	t.Helper()
	if diff := got.Sub(want); diff < -tolerance || diff > tolerance {
		t.Fatalf("time %v not within %v of %v (diff %v)", got, tolerance, want, diff)
	}
}

func TestBackoff(t *testing.T) {
	cases := []struct {
		attempts   int
		retryAfter time.Duration
		want       time.Duration
	}{
		{1, 0, time.Minute},
		{2, 0, 5 * time.Minute},
		{3, 0, 30 * time.Minute},
		{4, 0, 2 * time.Hour},
		{5, 0, 24 * time.Hour},
		{9, 0, 24 * time.Hour},
		{1, 90 * time.Second, 90 * time.Second},
	}
	for _, c := range cases {
		if got := backoff(c.attempts, c.retryAfter); got != c.want {
			t.Errorf("backoff(%d, %v) = %v, want %v", c.attempts, c.retryAfter, got, c.want)
		}
	}
}

func TestDrainEmptyQueue(t *testing.T) {
	resetBiniLink(t)
	srv := newIngressServer(t, statusResponder(http.StatusCreated))

	stop, err := newTestUploader(srv.URL, 0).drain(context.Background())
	if stop || err != nil {
		t.Fatalf("drain = (%v, %v), want (false, nil)", stop, err)
	}
	if n := srv.calls.Load(); n != 0 {
		t.Fatalf("http calls = %d, want 0", n)
	}
}

func TestDrainRecordsOutcomes(t *testing.T) {
	resetBiniLink(t)
	enqueue(t, "vidLinked__", "1")
	enqueue(t, "vidConflict", "2")
	enqueue(t, "vidRetry___", "3")
	codes := map[string]int{
		"vidLinked__": http.StatusCreated,
		"vidConflict": http.StatusConflict,
		"vidRetry___": http.StatusServiceUnavailable,
	}
	srv := newIngressServer(t, func(w http.ResponseWriter, videoID string) {
		w.WriteHeader(codes[videoID])
	})

	stop, err := newTestUploader(srv.URL, 0).drain(context.Background())
	if stop || err != nil {
		t.Fatalf("drain = (%v, %v), want (false, nil)", stop, err)
	}
	want := map[string]string{"vidLinked__": "linked", "vidConflict": "conflict", "vidRetry___": "pending"}
	for videoID, status := range want {
		if got := statusOf(t, videoID); got != status {
			t.Errorf("status of %s = %q, want %q", videoID, got, status)
		}
		if got := attemptsOf(t, videoID); got != 1 {
			t.Errorf("attempts of %s = %d, want 1", videoID, got)
		}
	}
	if n := srv.calls.Load(); n != 3 {
		t.Errorf("http calls = %d, want 3", n)
	}
	due, err := testStore.ClaimBiniLinkPending(context.Background(), 50)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("due rows after drain = %v, want none", due)
	}
}

func TestDrainStopsOnUnauthorized(t *testing.T) {
	resetBiniLink(t)
	enqueue(t, "vidA_______", "1")
	enqueue(t, "vidB_______", "2")
	srv := newIngressServer(t, statusResponder(http.StatusUnauthorized))

	stop, err := newTestUploader(srv.URL, 0).drain(context.Background())
	if !stop || err != nil {
		t.Fatalf("drain = (%v, %v), want (true, nil)", stop, err)
	}
	if n := srv.calls.Load(); n != 1 {
		t.Fatalf("http calls = %d, want 1", n)
	}
	for _, videoID := range []string{"vidA_______", "vidB_______"} {
		if got := statusOf(t, videoID); got != "pending" {
			t.Errorf("status of %s = %q, want pending", videoID, got)
		}
		if got := attemptsOf(t, videoID); got != 0 {
			t.Errorf("attempts of %s = %d, want 0", videoID, got)
		}
	}
}

func TestDrainUnresolvedRetriesOnce(t *testing.T) {
	resetBiniLink(t)
	enqueue(t, "vid404_____", "1")
	srv := newIngressServer(t, statusResponder(http.StatusNotFound))
	u := newTestUploader(srv.URL, 0)

	if _, err := u.drain(context.Background()); err != nil {
		t.Fatalf("first drain: %v", err)
	}
	if got := statusOf(t, "vid404_____"); got != "pending" {
		t.Fatalf("status after first 404 = %q, want pending", got)
	}
	assertNear(t, nextAttemptOf(t, "vid404_____"), time.Now().Add(7*24*time.Hour), time.Minute)

	makeDue(t, "vid404_____", 1)
	if _, err := u.drain(context.Background()); err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if got := statusOf(t, "vid404_____"); got != "unresolved" {
		t.Fatalf("status after second 404 = %q, want unresolved", got)
	}
	if got := attemptsOf(t, "vid404_____"); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestDrainRetryGivesUpAfterMaxAttempts(t *testing.T) {
	resetBiniLink(t)
	enqueue(t, "vidTired___", "1")
	makeDue(t, "vidTired___", 9)
	srv := newIngressServer(t, statusResponder(http.StatusServiceUnavailable))

	if _, err := newTestUploader(srv.URL, 0).drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := statusOf(t, "vidTired___"); got != "failed" {
		t.Fatalf("status = %q, want failed", got)
	}
	if got := attemptsOf(t, "vidTired___"); got != 10 {
		t.Fatalf("attempts = %d, want 10", got)
	}
}

func TestDrainHonoursRetryAfter(t *testing.T) {
	resetBiniLink(t)
	enqueue(t, "vidBusy____", "1")
	srv := newIngressServer(t, func(w http.ResponseWriter, _ string) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	if _, err := newTestUploader(srv.URL, 0).drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := statusOf(t, "vidBusy____"); got != "pending" {
		t.Fatalf("status = %q, want pending", got)
	}
	assertNear(t, nextAttemptOf(t, "vidBusy____"), time.Now().Add(120*time.Second), 5*time.Second)
}

func TestDrainStopsOnContextCancel(t *testing.T) {
	resetBiniLink(t)
	for _, videoID := range []string{"vid1_______", "vid2_______", "vid3_______"} {
		enqueue(t, videoID, "1")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var once sync.Once
	srv := newIngressServer(t, func(w http.ResponseWriter, _ string) {
		w.WriteHeader(http.StatusCreated)
		once.Do(func() { time.AfterFunc(50*time.Millisecond, cancel) })
	})

	start := time.Now()
	if _, err := newTestUploader(srv.URL, time.Hour).drain(ctx); err != nil && ctx.Err() == nil {
		t.Fatalf("drain: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("drain took %v after cancel, want well under a second", elapsed)
	}
	if n := srv.calls.Load(); n != 1 {
		t.Fatalf("http calls = %d, want 1", n)
	}
	pending := 0
	for _, videoID := range []string{"vid1_______", "vid2_______", "vid3_______"} {
		if statusOf(t, videoID) == "pending" {
			pending++
		}
	}
	if pending != 2 {
		t.Fatalf("pending rows = %d, want 2", pending)
	}
}

func TestStartDisabled(t *testing.T) {
	cases := []Settings{
		{Enabled: false, Key: "k", BaseURL: "http://127.0.0.1:1"},
		{Enabled: true, Key: "", BaseURL: "http://127.0.0.1:1"},
	}
	for _, s := range cases {
		if Start(context.Background(), testStore, s) {
			t.Errorf("Start(%+v) = true, want false", s)
		}
	}
}

func TestDrainUnresolvedRetriesOnceAfterTransientErrors(t *testing.T) {
	resetBiniLink(t)
	enqueue(t, "vidFlaky___", "1")
	code := http.StatusServiceUnavailable
	srv := newIngressServer(t, func(w http.ResponseWriter, _ string) { w.WriteHeader(code) })
	u := newTestUploader(srv.URL, 0)

	if _, err := u.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	makeDue(t, "vidFlaky___", 1)
	code = http.StatusNotFound
	if _, err := u.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, "vidFlaky___"); got != "pending" {
		t.Fatalf("first 404 after a 503: status %q, want pending", got)
	}
	assertNear(t, nextAttemptOf(t, "vidFlaky___"), time.Now().Add(7*24*time.Hour), time.Minute)

	makeDue(t, "vidFlaky___", 2)
	if _, err := u.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, "vidFlaky___"); got != "unresolved" {
		t.Fatalf("second 404: status %q, want unresolved", got)
	}
}

func TestDrainUnresolvedRetryDoesNotOutliveMaxAttempts(t *testing.T) {
	resetBiniLink(t)
	enqueue(t, "vidLate404_", "1")
	makeDue(t, "vidLate404_", 9)
	srv := newIngressServer(t, statusResponder(http.StatusNotFound))

	if _, err := newTestUploader(srv.URL, 0).drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, "vidLate404_"); got != "pending" {
		t.Fatalf("status %q, want pending: a first 404 still earns its 7-day retry", got)
	}
}
