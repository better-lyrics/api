package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestEnqueueBiniLinkKeepsFirstPair(t *testing.T) {
	resetTables(t)
	ctx := context.Background()

	if err := testStore.EnqueueBiniLink(ctx, "qrO4YZeyl0I", "1440815557"); err != nil {
		t.Fatal(err)
	}
	if err := testStore.EnqueueBiniLink(ctx, "qrO4YZeyl0I", "999"); err != nil {
		t.Fatal(err)
	}

	rows, err := testStore.ClaimBiniLinkPending(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].AppleTrackID != "1440815557" || rows[0].Attempts != 0 {
		t.Fatalf("want first pair kept, got %+v", rows)
	}
}

func TestEnqueueBiniLinkSkipsEmpty(t *testing.T) {
	resetTables(t)
	ctx := context.Background()

	if err := testStore.EnqueueBiniLink(ctx, "", "1"); err != nil {
		t.Fatal(err)
	}
	if err := testStore.EnqueueBiniLink(ctx, "qrO4YZeyl0I", ""); err != nil {
		t.Fatal(err)
	}

	rows, err := testStore.ClaimBiniLinkPending(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("want no rows, got %+v", rows)
	}
}

func TestClaimBiniLinkPendingOnlyReturnsDueRows(t *testing.T) {
	resetTables(t)
	ctx := context.Background()

	for _, v := range []string{"aaaaaaaaaaa", "bbbbbbbbbbb", "ccccccccccc"} {
		if err := testStore.EnqueueBiniLink(ctx, v, "1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := testStore.SetBiniLinkResult(ctx, "aaaaaaaaaaa", "pending", 1, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetBiniLinkResult(ctx, "bbbbbbbbbbb", "linked", 1, time.Now()); err != nil {
		t.Fatal(err)
	}

	rows, err := testStore.ClaimBiniLinkPending(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].VideoID != "ccccccccccc" {
		t.Fatalf("want only the due pending row, got %+v", rows)
	}
}

func TestClaimBiniLinkPendingHonoursLimit(t *testing.T) {
	resetTables(t)
	ctx := context.Background()

	for _, v := range []string{"aaaaaaaaaaa", "bbbbbbbbbbb", "ccccccccccc"} {
		if err := testStore.EnqueueBiniLink(ctx, v, "1"); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := testStore.ClaimBiniLinkPending(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
}

func TestSetBiniLinkResultRecordsAttempts(t *testing.T) {
	resetTables(t)
	ctx := context.Background()

	if err := testStore.EnqueueBiniLink(ctx, "aaaaaaaaaaa", "1"); err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetBiniLinkResult(ctx, "aaaaaaaaaaa", "pending", 3, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}

	rows, err := testStore.ClaimBiniLinkPending(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Attempts != 3 {
		t.Fatalf("want attempts 3, got %+v", rows)
	}
}

func TestSeedBiniLinkBackfill(t *testing.T) {
	resetTables(t)
	ctx := context.Background()

	put := func(key, apple, isrc string, vids ...string) {
		t.Helper()
		if err := testStore.SetSongMetadata(ctx, &SongMetadata{
			CacheKey: key, AppleTrackID: apple, ISRC: isrc, VideoIDs: vids,
		}); err != nil {
			t.Fatal(err)
		}
	}
	put("k1", "100", "USAAA0000001", "single00001")
	put("k2", "200", "USAAA0000002", "split000001")
	put("k3", "300", "USAAA0000003", "split000001")
	put("k4", "", "USAAA0000004", "noapple0001")
	put("k5", "500", "USAAA0000005", "sameisrc001")
	put("k6", "501", "USAAA0000005", "sameisrc001")
	put("k7", "700", "", "noisrc00001")
	put("k8", "800", "USAAA0000008", "orphankey01")
	if err := testStore.AddVideoID(ctx, "k-without-metadata", "orphankey01"); err != nil {
		t.Fatal(err)
	}

	seeded, next, err := testStore.SeedBiniLinkBackfill(ctx, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if next != "" {
		t.Fatalf("want end of scan, got cursor %q", next)
	}
	if seeded != 2 {
		t.Fatalf("want 2 seeded, got %d", seeded)
	}

	rows, err := testStore.ClaimBiniLinkPending(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.VideoID] = r.AppleTrackID
	}
	if got["single00001"] != "100" {
		t.Fatalf("single-ISRC video missing: %+v", got)
	}
	if _, ok := got["sameisrc001"]; !ok {
		t.Fatalf("same-ISRC video missing: %+v", got)
	}
	for _, skipped := range []string{"split000001", "noapple0001", "noisrc00001", "orphankey01"} {
		if _, ok := got[skipped]; ok {
			t.Fatalf("%s must be skipped", skipped)
		}
	}
}

func TestSeedBiniLinkBackfillPagesAndIsIdempotent(t *testing.T) {
	resetTables(t)
	ctx := context.Background()

	for _, v := range []string{"aaaaaaaaaaa", "bbbbbbbbbbb", "ccccccccccc"} {
		if err := testStore.SetSongMetadata(ctx, &SongMetadata{
			CacheKey: "k" + v, AppleTrackID: "1", ISRC: "USAAA0000001", VideoIDs: []string{v},
		}); err != nil {
			t.Fatal(err)
		}
	}

	seedAll := func() int {
		total, cursor := 0, ""
		for {
			seeded, next, err := testStore.SeedBiniLinkBackfill(ctx, cursor, 2)
			if err != nil {
				t.Fatal(err)
			}
			total += seeded
			if next == "" {
				return total
			}
			cursor = next
		}
	}

	if total := seedAll(); total != 3 {
		t.Fatalf("want 3 across pages, got %d", total)
	}
	if total := seedAll(); total != 0 {
		t.Fatalf("second pass must seed nothing, got %d", total)
	}
}

func TestEnqueueBiniLinkRejectsMalformedIDs(t *testing.T) {
	resetTables(t)
	ctx := context.Background()

	bad := []struct{ video, apple string }{
		{"qrO4YZeyl0IqrO4YZeyl0I", "1"},
		{"short", "1"},
		{"qrO4YZeyl0!", "1"},
		{"qrO4YZeyl0I", "12a"},
		{"qrO4YZeyl0I", "-1"},
		{"qrO4YZeyl0I", strings.Repeat("9", 129)},
	}
	for _, b := range bad {
		if err := testStore.EnqueueBiniLink(ctx, b.video, b.apple); err != nil {
			t.Fatal(err)
		}
	}
	if err := testStore.EnqueueBiniLink(ctx, "a-_Z09bcdeF", "0001440815557"); err != nil {
		t.Fatal(err)
	}

	rows, err := testStore.ClaimBiniLinkPending(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].VideoID != "a-_Z09bcdeF" || rows[0].AppleTrackID != "0001440815557" {
		t.Fatalf("want only the well-formed pair, got %+v", rows)
	}
}

func TestAddVideoIDAndEnqueueBiniLink(t *testing.T) {
	resetTables(t)
	ctx := context.Background()

	for _, m := range []SongMetadata{
		{CacheKey: "k-apple", AppleTrackID: "1440815557", ISRC: "USAAA0000001"},
		{CacheKey: "k-no-apple", ISRC: "USAAA0000002"},
		{CacheKey: "k-bad-apple", AppleTrackID: "abc", ISRC: "USAAA0000003"},
	} {
		if err := testStore.SetSongMetadata(ctx, &m); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct{ video, key string }{
		{"aaaaaaaaaaa", "k-apple"},
		{"bbbbbbbbbbb", "k-no-apple"},
		{"ccccccccccc", "k-bad-apple"},
		{"ddddddddddd", "k-missing"},
		{"not-a-video-id", "k-apple"},
		{"aaaaaaaaaaa", "k-apple"},
	}
	for _, c := range cases {
		if err := testStore.AddVideoIDAndEnqueueBiniLink(ctx, c.key, c.video); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := testStore.ClaimBiniLinkPending(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].VideoID != "aaaaaaaaaaa" || rows[0].AppleTrackID != "1440815557" {
		t.Fatalf("want only the pair with a valid Apple ID, got %+v", rows)
	}

	for _, c := range cases {
		keys, err := testStore.GetCacheKeysByVideoID(ctx, c.video)
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 1 || keys[0] != c.key {
			t.Fatalf("video_map for %s = %v, want [%s]: recording must not depend on queueing", c.video, keys, c.key)
		}
	}
}

func TestAddVideoIDAndEnqueueBiniLinkSkipsEmpty(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	if err := testStore.AddVideoIDAndEnqueueBiniLink(ctx, "", "aaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if err := testStore.AddVideoIDAndEnqueueBiniLink(ctx, "k", ""); err != nil {
		t.Fatal(err)
	}
	if keys, _ := testStore.GetCacheKeysByVideoID(ctx, "aaaaaaaaaaa"); len(keys) != 0 {
		t.Fatalf("empty cache key must not be recorded, got %v", keys)
	}
}

func TestSeedBiniLinkBackfillSkipsMalformedIDs(t *testing.T) {
	resetTables(t)
	ctx := context.Background()

	for _, m := range []SongMetadata{
		{CacheKey: "k1", AppleTrackID: "100", ISRC: "USAAA0000001", VideoIDs: []string{"good0000001"}},
		{CacheKey: "k2", AppleTrackID: "200", ISRC: "USAAA0000002", VideoIDs: []string{"good0000001good0000001"}},
		{CacheKey: "k3", AppleTrackID: "x300", ISRC: "USAAA0000003", VideoIDs: []string{"good0000003"}},
	} {
		if err := testStore.SetSongMetadata(ctx, &m); err != nil {
			t.Fatal(err)
		}
	}

	seeded, _, err := testStore.SeedBiniLinkBackfill(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := testStore.ClaimBiniLinkPending(ctx, 10)
	if seeded != 1 || len(rows) != 1 || rows[0].VideoID != "good0000001" {
		t.Fatalf("seeded %d, rows %+v", seeded, rows)
	}
}

func TestMarkBiniLinkUnresolvedRetry(t *testing.T) {
	resetTables(t)
	ctx := context.Background()

	if err := testStore.EnqueueBiniLink(ctx, "aaaaaaaaaaa", "1"); err != nil {
		t.Fatal(err)
	}
	rows, _ := testStore.ClaimBiniLinkPending(ctx, 10)
	if len(rows) != 1 || rows[0].UnresolvedRetried {
		t.Fatalf("fresh row must not be marked, got %+v", rows)
	}

	if err := testStore.MarkBiniLinkUnresolvedRetry(ctx, "aaaaaaaaaaa", 2, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	rows, _ = testStore.ClaimBiniLinkPending(ctx, 10)
	if len(rows) != 1 || !rows[0].UnresolvedRetried || rows[0].Attempts != 2 {
		t.Fatalf("want pending, marked, attempts 2, got %+v", rows)
	}
}

func TestTryBiniLinkWorkerLock(t *testing.T) {
	ctx := context.Background()

	first, ok, err := testStore.TryBiniLinkWorkerLock(ctx)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	if !first.Held(ctx) {
		t.Fatal("first lock must report held")
	}

	if _, ok, err := testStore.TryBiniLinkWorkerLock(ctx); err != nil || ok {
		t.Fatalf("second worker must not get the lock: ok=%v err=%v", ok, err)
	}

	first.Release()

	second, ok, err := testStore.TryBiniLinkWorkerLock(ctx)
	if err != nil || !ok {
		t.Fatalf("lock after release: ok=%v err=%v", ok, err)
	}
	second.Release()
}

func TestBiniLinkWorkerLockDoesNotUsePoolConnections(t *testing.T) {
	ctx := context.Background()
	before := testStore.pool.Stat().AcquiredConns()

	lock, ok, err := testStore.TryBiniLinkWorkerLock(ctx)
	if err != nil || !ok {
		t.Fatalf("lock: ok=%v err=%v", ok, err)
	}
	defer lock.Release()

	if after := testStore.pool.Stat().AcquiredConns(); after != before {
		t.Fatalf("pool acquired conns %d -> %d: the lock must hold its own connection", before, after)
	}
}

func TestBiniLinkWorkerLockDetectsLostSession(t *testing.T) {
	ctx := context.Background()

	lock, ok, err := testStore.TryBiniLinkWorkerLock(ctx)
	if err != nil || !ok {
		t.Fatalf("lock: ok=%v err=%v", ok, err)
	}
	defer lock.Release()

	if _, err := testStore.pool.Exec(ctx, `SELECT pg_terminate_backend($1)`, lock.conn.PgConn().PID()); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for lock.Held(ctx) {
		if time.Now().After(deadline) {
			t.Fatal("Held kept reporting true after the session was terminated")
		}
		time.Sleep(20 * time.Millisecond)
	}

	other, ok, err := testStore.TryBiniLinkWorkerLock(ctx)
	if err != nil || !ok {
		t.Fatalf("lock must be free once its session is gone: ok=%v err=%v", ok, err)
	}
	other.Release()
}
