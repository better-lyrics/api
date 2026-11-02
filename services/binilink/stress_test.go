//go:build stress

package binilink

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lyrics-api-go/stats"
)

type replyKind int

const (
	replyCreated replyKind = iota
	replyOK
	replyConflict
	replyNotFound
	replyNoISRC
	replyBadRequest
	replyBusyRetryAfter
	replyBusy
	replyServerError
	replyDrop
)

var replyWeights = []struct {
	kind   replyKind
	weight int
}{
	{replyCreated, 22}, {replyOK, 10}, {replyConflict, 5}, {replyNotFound, 15}, {replyNoISRC, 5},
	{replyBadRequest, 5}, {replyBusyRetryAfter, 9}, {replyBusy, 9}, {replyServerError, 10}, {replyDrop, 10},
}

func (k replyKind) retryable() bool { return k >= replyBusyRetryAfter }

type stressIngress struct {
	*httptest.Server
	mu          sync.Mutex
	rng         *rand.Rand
	replies     map[string][]replyKind
	counts      map[replyKind]int
	total       int
	inFlight    atomic.Int32
	maxInFlight atomic.Int32
}

func newStressIngress(t *testing.T, seed int64) *stressIngress {
	t.Helper()
	s := &stressIngress{rng: rand.New(rand.NewSource(seed)), replies: map[string][]replyKind{}, counts: map[replyKind]int{}}
	totalWeight := 0
	for _, w := range replyWeights {
		totalWeight += w.weight
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := s.inFlight.Add(1)
		defer s.inFlight.Add(-1)
		for {
			peak := s.maxInFlight.Load()
			if n <= peak || s.maxInFlight.CompareAndSwap(peak, n) {
				break
			}
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			YoutubeID string `json:"youtube_id"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode body %q: %v", raw, err)
		}

		s.mu.Lock()
		latency := time.Duration(s.rng.Intn(5001)) * time.Microsecond
		pick, kind := s.rng.Intn(totalWeight), replyCreated
		for _, w := range replyWeights {
			if pick < w.weight {
				kind = w.kind
				break
			}
			pick -= w.weight
		}
		s.replies[body.YoutubeID] = append(s.replies[body.YoutubeID], kind)
		s.counts[kind]++
		s.total++
		s.mu.Unlock()

		time.Sleep(latency)
		switch kind {
		case replyCreated:
			w.WriteHeader(http.StatusCreated)
		case replyOK:
			w.WriteHeader(http.StatusOK)
		case replyConflict:
			w.WriteHeader(http.StatusConflict)
		case replyNotFound:
			w.WriteHeader(http.StatusNotFound)
		case replyNoISRC:
			w.WriteHeader(http.StatusUnprocessableEntity)
		case replyBadRequest:
			w.WriteHeader(http.StatusBadRequest)
		case replyBusyRetryAfter:
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
		case replyBusy:
			w.WriteHeader(http.StatusServiceUnavailable)
		case replyServerError:
			w.WriteHeader(http.StatusInternalServerError)
		case replyDrop:
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			conn.Close()
		}
	}))
	t.Cleanup(s.Close)
	return s
}

type modelRow struct {
	status              string
	attempts            int
	retried             bool
	postedAfterTerminal bool
	late404             bool
}

func simulate(replies []replyKind) modelRow {
	m := modelRow{status: "pending"}
	for _, k := range replies {
		if m.status != "pending" {
			m.postedAfterTerminal = true
		}
		m.attempts++
		switch {
		case k.retryable():
			if m.attempts >= maxAttempts {
				m.status = statusFailed
			}
		case k == replyNotFound && !m.retried:
			m.retried = true
			m.late404 = m.attempts >= maxAttempts
		case k == replyNotFound:
			m.status = "unresolved"
		default:
			m.status = map[replyKind]string{
				replyCreated: "linked", replyOK: "exists", replyConflict: "conflict",
				replyNoISRC: "no_isrc", replyBadRequest: "invalid",
			}[k]
		}
	}
	return m
}

type biniLinkCounters struct{ sent, linked, exists, conflict, unresolved, errors int64 }

func readBiniLinkCounters() biniLinkCounters {
	s := stats.Get()
	return biniLinkCounters{
		s.BiniLinkSent.Load(), s.BiniLinkLinked.Load(), s.BiniLinkExists.Load(),
		s.BiniLinkConflict.Load(), s.BiniLinkUnresolved.Load(), s.BiniLinkErrors.Load(),
	}
}

func countRows(t *testing.T, where string) int {
	t.Helper()
	var n int
	if err := testStore.Pool().QueryRow(context.Background(), `SELECT count(*) FROM binilink_links WHERE `+where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStressDrainMixedReplies(t *testing.T) {
	const rows = 5000
	resetBiniLink(t)
	ctx := context.Background()
	if _, err := testStore.Pool().Exec(ctx, `
		INSERT INTO binilink_links (video_id, apple_track_id)
		SELECT 'mix' || lpad(i::text, 8, '0'), (1000000000 + i)::text FROM generate_series(1, $1) AS i`, rows); err != nil {
		t.Fatal(err)
	}
	srv := newStressIngress(t, 20261102)
	u := newTestUploader(srv.URL, 0)
	before := readBiniLinkCounters()

	start := time.Now()
	rounds, drains := 0, 0
	for ; rounds < 30 && countRows(t, `status = 'pending'`) > 0; rounds++ {
		for countRows(t, `status = 'pending' AND next_attempt <= now()`) > 0 {
			stop, _, err := u.drain(ctx)
			drains++
			if stop || err != nil {
				t.Fatalf("drain = (%v, %v)", stop, err)
			}
			if drains > 200000 {
				t.Fatal("drain loop never settled")
			}
		}
		if _, err := testStore.Pool().Exec(ctx,
			`UPDATE binilink_links SET next_attempt = now() - interval '1 second' WHERE status = 'pending'`); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	after := readBiniLinkCounters()

	if left := countRows(t, `status = 'pending'`); left > 0 {
		t.Errorf("%d rows still pending after %d rounds", left, rounds)
	}
	if peak := srv.maxInFlight.Load(); peak != 1 {
		t.Errorf("max in flight = %d, want 1", peak)
	}

	dbRows, err := testStore.Pool().Query(ctx, `SELECT video_id, status, attempts, unresolved_retried FROM binilink_links`)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]int{}
	checked := 0
	for dbRows.Next() {
		var v, status string
		var attempts int
		var retried bool
		if err := dbRows.Scan(&v, &status, &attempts, &retried); err != nil {
			t.Fatal(err)
		}
		statuses[status]++
		checked++
		replies := srv.replies[v]
		m := simulate(replies)
		if status != m.status || attempts != m.attempts || retried != m.retried {
			t.Errorf("%s after replies %v: db (%s, %d, retried %v), model (%s, %d, retried %v)",
				v, replies, status, attempts, retried, m.status, m.attempts, m.retried)
		}
		if m.postedAfterTerminal {
			t.Errorf("%s posted again after a terminal reply: %v", v, replies)
		}
		if attempts > maxAttempts && !m.late404 {
			t.Errorf("%s has %d attempts, want at most %d", v, attempts, maxAttempts)
		}
		first404 := -1
		for i, k := range replies {
			if k == replyNotFound {
				first404 = i
				break
			}
		}
		if first404 >= 0 && first404 == len(replies)-1 && status == "pending" {
			t.Errorf("%s got its first 404 last and was never retried: %v", v, replies)
		}
	}
	dbRows.Close()
	if checked != rows {
		t.Fatalf("checked %d rows, want %d", checked, rows)
	}

	srv.mu.Lock()
	counts, total := srv.counts, srv.total
	srv.mu.Unlock()
	if got := after.sent - before.sent; got != int64(total) {
		t.Errorf("sent counter grew by %d, server saw %d requests", got, total)
	}
	expect := biniLinkCounters{
		linked:     int64(counts[replyCreated]),
		exists:     int64(counts[replyOK]),
		conflict:   int64(counts[replyConflict]),
		unresolved: int64(counts[replyNotFound] + counts[replyNoISRC]),
		errors:     int64(counts[replyBadRequest] + counts[replyBusyRetryAfter] + counts[replyBusy] + counts[replyServerError] + counts[replyDrop]),
	}
	gotDelta := biniLinkCounters{
		linked: after.linked - before.linked, exists: after.exists - before.exists, conflict: after.conflict - before.conflict,
		unresolved: after.unresolved - before.unresolved, errors: after.errors - before.errors,
	}
	if gotDelta != expect {
		t.Errorf("counter deltas %+v, want %+v", gotDelta, expect)
	}
	t.Logf("%d rows, %d requests, %d drains, %d rounds, %v (%.0f req/s), max in flight %d, statuses %v",
		rows, total, drains, rounds, elapsed, float64(total)/elapsed.Seconds(), srv.maxInFlight.Load(), statuses)
}

func TestStressTwoUploadersShareOneLock(t *testing.T) {
	const rows = 2000
	resetBiniLink(t)
	ctx := context.Background()
	if _, err := testStore.Pool().Exec(ctx, `
		INSERT INTO binilink_links (video_id, apple_track_id)
		SELECT 'duo' || lpad(i::text, 8, '0'), (1000000000 + i)::text FROM generate_series(1, $1) AS i`, rows); err != nil {
		t.Fatal(err)
	}

	var inFlight, maxInFlight, overlaps atomic.Int32
	var mu sync.Mutex
	posts := map[string]int{}
	total := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			peak := maxInFlight.Load()
			if n <= peak || maxInFlight.CompareAndSwap(peak, n) {
				break
			}
		}
		if n > 1 {
			overlaps.Add(1)
		}
		var body struct {
			YoutubeID string `json:"youtube_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		posts[body.YoutubeID]++
		total++
		mu.Unlock()
		time.Sleep(time.Millisecond)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	previousLock, previousIdle := lockRetryInterval, idleSleep
	lockRetryInterval, idleSleep = 5*time.Millisecond, 20*time.Millisecond
	defer func() { lockRetryInterval, idleSleep = previousLock, previousIdle }()

	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			newTestUploader(srv.URL, 0).run(runCtx)
		}()
	}
	defer func() {
		cancel()
		wg.Wait()
	}()

	start := time.Now()
	kills := 0
	for kills < 6 {
		time.Sleep(150 * time.Millisecond)
		tag, err := testStore.Pool().Exec(ctx, `
			SELECT pg_terminate_backend(pid) FROM pg_locks
			WHERE locktype = 'advisory' AND granted AND classid::bigint = $1 AND objid::bigint = $2`,
			workerLockClassID, workerLockObjID)
		if err != nil {
			t.Fatal(err)
		}
		if tag.RowsAffected() > 0 {
			kills++
		}
		if time.Since(start) > 30*time.Second {
			t.Fatalf("only %d lock holders killed in 30s", kills)
		}
	}

	deadline := time.Now().Add(60 * time.Second)
	for countRows(t, `status = 'linked'`) < rows {
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d rows linked after 60s", countRows(t, `status = 'linked'`), rows)
		}
		time.Sleep(20 * time.Millisecond)
	}
	elapsed := time.Since(start)

	mu.Lock()
	defer mu.Unlock()
	var twice []string
	for v, n := range posts {
		if n > 1 {
			twice = append(twice, fmt.Sprintf("%s x%d", v, n))
		}
	}
	sort.Strings(twice)
	if len(posts) != rows {
		t.Errorf("server saw %d distinct videos, want %d", len(posts), rows)
	}
	if len(twice) > 0 {
		t.Errorf("%d videos posted again after they were linked, e.g. %v", len(twice), twice[:min(5, len(twice))])
	}
	if peak := maxInFlight.Load(); peak != 1 {
		t.Errorf("max in flight = %d, want 1 (%d requests overlapped another)", peak, overlaps.Load())
	}
	t.Logf("%d rows, %d requests, %d lock holders killed, %v, max in flight %d", rows, total, kills, elapsed, maxInFlight.Load())
}
