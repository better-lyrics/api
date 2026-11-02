package binilink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lyrics-api-go/internal/store"
	"lyrics-api-go/stats"
)

func newTestUploader(baseURL string, interval time.Duration) *uploader {
	return &uploader{
		store:     testStore,
		client:    &http.Client{Timeout: time.Second},
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
		{1, time.Second, time.Minute},
		{3, time.Hour, time.Hour},
		{4, time.Second, 2 * time.Hour},
		{2, 48 * time.Hour, 24 * time.Hour},
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

	stop, _, err := newTestUploader(srv.URL, 0).drain(context.Background())
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
	codes := map[string]int{
		"vidLinked__": http.StatusCreated,
		"vidConflict": http.StatusConflict,
	}
	srv := newIngressServer(t, func(w http.ResponseWriter, videoID string) {
		w.WriteHeader(codes[videoID])
	})

	stop, _, err := newTestUploader(srv.URL, 0).drain(context.Background())
	if stop || err != nil {
		t.Fatalf("drain = (%v, %v), want (false, nil)", stop, err)
	}
	want := map[string]string{"vidLinked__": "linked", "vidConflict": "conflict"}
	for videoID, status := range want {
		if got := statusOf(t, videoID); got != status {
			t.Errorf("status of %s = %q, want %q", videoID, got, status)
		}
		if got := attemptsOf(t, videoID); got != 1 {
			t.Errorf("attempts of %s = %d, want 1", videoID, got)
		}
	}
	if n := srv.calls.Load(); n != 2 {
		t.Errorf("http calls = %d, want 2", n)
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

	stop, _, err := newTestUploader(srv.URL, 0).drain(context.Background())
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

	if _, _, err := u.drain(context.Background()); err != nil {
		t.Fatalf("first drain: %v", err)
	}
	if got := statusOf(t, "vid404_____"); got != "pending" {
		t.Fatalf("status after first 404 = %q, want pending", got)
	}
	assertNear(t, nextAttemptOf(t, "vid404_____"), time.Now().Add(7*24*time.Hour), time.Minute)

	makeDue(t, "vid404_____", 1)
	if _, _, err := u.drain(context.Background()); err != nil {
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

	if _, _, err := newTestUploader(srv.URL, 0).drain(context.Background()); err != nil {
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

	if _, _, err := newTestUploader(srv.URL, 0).drain(context.Background()); err != nil {
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
	if _, _, err := newTestUploader(srv.URL, time.Hour).drain(ctx); err != nil && ctx.Err() == nil {
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

	if _, _, err := u.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	makeDue(t, "vidFlaky___", 1)
	code = http.StatusNotFound
	if _, _, err := u.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, "vidFlaky___"); got != "pending" {
		t.Fatalf("first 404 after a 503: status %q, want pending", got)
	}
	assertNear(t, nextAttemptOf(t, "vidFlaky___"), time.Now().Add(7*24*time.Hour), time.Minute)

	makeDue(t, "vidFlaky___", 2)
	if _, _, err := u.drain(context.Background()); err != nil {
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

	if _, _, err := newTestUploader(srv.URL, 0).drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, "vidLate404_"); got != "pending" {
		t.Fatalf("status %q, want pending: a first 404 still earns its 7-day retry", got)
	}
}

func TestDrainPausesOnRetryableReply(t *testing.T) {
	cases := []struct {
		name      string
		respond   func(http.ResponseWriter, string)
		wantPause time.Duration
	}{
		{"503 with Retry-After", func(w http.ResponseWriter, _ string) {
			w.Header().Set("Retry-After", "600")
			w.WriteHeader(http.StatusServiceUnavailable)
		}, 600 * time.Second},
		{"503 without Retry-After", statusResponder(http.StatusServiceUnavailable), 0},
		{"500", statusResponder(http.StatusInternalServerError), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resetBiniLink(t)
			for _, v := range []string{"busy1______", "busy2______", "busy3______"} {
				enqueue(t, v, "1")
			}
			srv := newIngressServer(t, c.respond)

			stop, pause, err := newTestUploader(srv.URL, 0).drain(context.Background())
			if stop || err != nil {
				t.Fatalf("drain = (%v, %v), want (false, nil)", stop, err)
			}
			if pause != c.wantPause {
				t.Fatalf("pause = %v, want %v", pause, c.wantPause)
			}
			if n := srv.calls.Load(); n != 1 {
				t.Fatalf("http calls = %d, want 1: a busy server must not get more requests", n)
			}
			tried := 0
			for _, v := range []string{"busy1______", "busy2______", "busy3______"} {
				if statusOf(t, v) != "pending" {
					t.Fatalf("%s left pending state", v)
				}
				tried += attemptsOf(t, v)
			}
			if tried != 1 {
				t.Fatalf("attempts spent = %d, want 1: untried rows must keep their attempts", tried)
			}
		})
	}
}

func TestDrainNetworkFailurePauses(t *testing.T) {
	resetBiniLink(t)
	enqueue(t, "down1______", "1")
	enqueue(t, "down2______", "1")
	srv := newIngressServer(t, statusResponder(http.StatusCreated))
	url := srv.URL
	srv.Close()

	stop, _, err := newTestUploader(url, 0).drain(context.Background())
	if stop || err != nil {
		t.Fatalf("drain = (%v, %v)", stop, err)
	}
	if attemptsOf(t, "down1______")+attemptsOf(t, "down2______") != 1 {
		t.Fatal("an unreachable server must cost one attempt, not one per row")
	}
}

func TestRunWait(t *testing.T) {
	cases := []struct{ pause, want time.Duration }{
		{0, idleSleep},
		{time.Second, idleSleep},
		{10 * time.Minute, 10 * time.Minute},
	}
	for _, c := range cases {
		if got := runWait(c.pause); got != c.want {
			t.Errorf("runWait(%v) = %v, want %v", c.pause, got, c.want)
		}
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	ok := map[string]string{
		"https://link.binimum.org":   "https://link.binimum.org",
		"https://link.binimum.org/":  "https://link.binimum.org",
		"https://link.binimum.org//": "https://link.binimum.org",
		"http://127.0.0.1:8080":      "http://127.0.0.1:8080",
		"https://example.com/proxy/": "https://example.com/proxy",
	}
	for in, want := range ok {
		got, err := normalizeBaseURL(in)
		if err != nil || got != want {
			t.Errorf("normalizeBaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "link.binimum.org", "ftp://link.binimum.org", "https://", "://bad", "https://link.binimum.org?x=1", "https://link.binimum.org#frag", "https://link.binimum.org?", "https://link.binimum.org#", "https://user:pass@link.binimum.org"} {
		if got, err := normalizeBaseURL(in); err == nil {
			t.Errorf("normalizeBaseURL(%q) = %q, want error", in, got)
		}
	}
}

func TestStartRefusesBadBaseURL(t *testing.T) {
	if Start(context.Background(), testStore, Settings{Enabled: true, Key: "k", BaseURL: "link.binimum.org"}) {
		t.Fatal("must not start with a base URL that has no scheme")
	}
}

func TestRunWaitsForWorkerLock(t *testing.T) {
	resetBiniLink(t)
	enqueue(t, "lockWait___", "1")
	srv := newIngressServer(t, statusResponder(http.StatusCreated))

	held, ok, err := testStore.TryBiniLinkWorkerLock(context.Background())
	if err != nil || !ok {
		t.Fatalf("hold lock: ok=%v err=%v", ok, err)
	}
	released := false
	defer func() {
		if !released {
			held.Release()
		}
	}()

	previous := lockRetryInterval
	lockRetryInterval = 20 * time.Millisecond
	defer func() { lockRetryInterval = previous }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		newTestUploader(srv.URL, 0).run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	time.Sleep(200 * time.Millisecond)
	if n := srv.calls.Load(); n != 0 {
		t.Fatalf("http calls while another worker holds the lock = %d, want 0", n)
	}

	held.Release()
	released = true
	deadline := time.Now().Add(3 * time.Second)
	for srv.calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("worker never started after the lock was released")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := statusOf(t, "lockWait___"); got != "linked" {
		t.Fatalf("status = %q, want linked", got)
	}
}

func TestRunStopsWhileWaitingForLock(t *testing.T) {
	held, ok, err := testStore.TryBiniLinkWorkerLock(context.Background())
	if err != nil || !ok {
		t.Fatalf("hold lock: ok=%v err=%v", ok, err)
	}
	defer held.Release()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		newTestUploader("http://127.0.0.1:1", 0).run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return after cancel while waiting for the lock")
	}
}

func TestShortRetryAfterDoesNotBurnAttempts(t *testing.T) {
	resetBiniLink(t)
	enqueue(t, "shortRetry_", "1")
	srv := newIngressServer(t, func(w http.ResponseWriter, _ string) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	u := newTestUploader(srv.URL, 0)

	if _, _, err := u.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertNear(t, nextAttemptOf(t, "shortRetry_"), time.Now().Add(time.Minute), 5*time.Second)
	if _, _, err := u.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := srv.calls.Load(); n != 1 {
		t.Fatalf("http calls = %d, want 1: the row must not be due again within a minute", n)
	}
}

func TestDrainCountsUnauthorized(t *testing.T) {
	resetBiniLink(t)
	enqueue(t, "unauthCount", "1")
	srv := newIngressServer(t, statusResponder(http.StatusUnauthorized))
	before := stats.Get().BiniLinkErrors.Load()

	if stop, _, _ := newTestUploader(srv.URL, 0).drain(context.Background()); !stop {
		t.Fatal("401 must stop the worker")
	}
	if got := stats.Get().BiniLinkErrors.Load() - before; got != 1 {
		t.Fatalf("errors counted = %d, want 1", got)
	}
}

func TestRunStopsSendingWhenItLosesTheLock(t *testing.T) {
	resetBiniLink(t)
	enqueue(t, "beforeLoss_", "1")
	srv := newIngressServer(t, statusResponder(http.StatusCreated))

	previousLock, previousIdle := lockRetryInterval, idleSleep
	lockRetryInterval, idleSleep = 20*time.Millisecond, 50*time.Millisecond
	defer func() { lockRetryInterval, idleSleep = previousLock, previousIdle }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		newTestUploader(srv.URL, 0).run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	waitFor(t, func() bool { return srv.calls.Load() == 1 })

	if _, err := testStore.Pool().Exec(context.Background(), `
		SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype = 'advisory' AND classid::bigint = $1 AND objid::bigint = $2`,
		workerLockClassID, workerLockObjID); err != nil {
		t.Fatal(err)
	}
	var thief *store.BiniLinkWorkerLock
	waitFor(t, func() bool {
		lock, ok, err := testStore.TryBiniLinkWorkerLock(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		thief = lock
		return ok
	})

	enqueue(t, "afterLoss__", "1")
	time.Sleep(300 * time.Millisecond)
	if n := srv.calls.Load(); n != 1 {
		thief.Release()
		t.Fatalf("http calls = %d while another worker holds the lock, want 1", n)
	}

	thief.Release()
	waitFor(t, func() bool { return srv.calls.Load() == 2 })
	if got := statusOf(t, "afterLoss__"); got != "linked" {
		t.Fatalf("status = %q, want linked once the lock is regained", got)
	}
}

const (
	workerLockClassID int64 = 0x62696e69
	workerLockObjID   int64 = 0x6c696e6b
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestNormalizeBaseURLErrorOmitsURL(t *testing.T) {
	if _, err := normalizeBaseURL("https://user:secret@link.binimum.org"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error must not echo the URL, got %v", err)
	}
}

func TestDrainChecksLockBeforeEachPost(t *testing.T) {
	resetBiniLink(t)
	for i := 0; i < batchSize+5; i++ {
		enqueue(t, fmt.Sprintf("batch%06d", i), "1")
	}

	lock, ok, err := testStore.TryBiniLinkWorkerLock(context.Background())
	if err != nil || !ok {
		t.Fatalf("lock: ok=%v err=%v", ok, err)
	}
	defer lock.Release()

	var srv *ingressServer
	srv = newIngressServer(t, func(w http.ResponseWriter, _ string) {
		if srv.calls.Load() == 7 {
			if _, err := testStore.Pool().Exec(context.Background(), `
				SELECT pg_terminate_backend(pid) FROM pg_locks
				WHERE locktype = 'advisory' AND classid::bigint = $1 AND objid::bigint = $2`,
				workerLockClassID, workerLockObjID); err != nil {
				t.Error(err)
			}
		}
		w.WriteHeader(http.StatusCreated)
	})

	u := newTestUploader(srv.URL, 0)
	u.lock = lock
	_, _, err = u.drain(context.Background())
	if !errors.Is(err, errLockLost) {
		t.Fatalf("drain err = %v, want errLockLost", err)
	}
	if n := srv.calls.Load(); n != 7 {
		t.Fatalf("http calls = %d, want 7: no request may start without the lock", n)
	}
}

func TestLockSettleOutlastsAnyRequest(t *testing.T) {
	u := newTestUploader("http://127.0.0.1:1", 0)
	if got := u.lockSettle(); got <= u.client.Timeout {
		t.Fatalf("lockSettle = %v, must exceed the client timeout %v", got, u.client.Timeout)
	}
	if got := newHTTPClient().Timeout; (&uploader{client: newHTTPClient()}).lockSettle() <= got {
		t.Fatal("production settle must exceed the production client timeout")
	}
}

func TestRunWaitsBeforeFirstPostAfterTakingTheLock(t *testing.T) {
	resetBiniLink(t)
	enqueue(t, "settleWait_", "1")
	srv := newIngressServer(t, statusResponder(http.StatusCreated))
	u := newTestUploader(srv.URL, 0)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	start := time.Now()
	go func() {
		u.run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	waitFor(t, func() bool { return srv.calls.Load() == 1 })
	if elapsed := time.Since(start); elapsed < u.client.Timeout {
		t.Fatalf("first post after %v, want at least the client timeout %v", elapsed, u.client.Timeout)
	}
}
