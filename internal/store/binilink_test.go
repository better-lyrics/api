package store

import (
	"context"
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
