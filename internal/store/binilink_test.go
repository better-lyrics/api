package store

import (
	"context"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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
	put("ttml_lyrics:quiet harbor mira vale 180s", "", "", "stubkey0001")
	for _, m := range []SongMetadata{
		{CacheKey: "ttml_lyrics:paper lanterns the fold 0s", AppleTrackID: "800", ISRC: "USAAA0000008", DurationMs: 200600, TrackName: "Paper Lanterns", ArtistName: "The Fold", VideoIDs: []string{"orphankey01"}},
		{CacheKey: "ttml_lyrics:north wind arlo 150s", AppleTrackID: "1300", ISRC: "USAAA0000013", DurationMs: 150000, TrackName: "North Wind", ArtistName: "Arlo", VideoIDs: []string{"othersong01"}},
		{CacheKey: "ttml_lyrics:quiet harbor mira vale harbor 0s", AppleTrackID: "1000", ISRC: "USAAA0000010", DurationMs: 180400, TrackName: "Quiet Harbor", ArtistName: "Mira Vale", VideoIDs: []string{"stubkey0001"}},
	} {
		if err := testStore.SetSongMetadata(ctx, &m); err != nil {
			t.Fatal(err)
		}
	}
	put("k11", "1100", "", "halfisrc001")
	put("k12", "1200", "USAAA0000012", "halfisrc001")
	for key, vid := range map[string]string{
		"ttml_lyrics:paper lanterns the fold 201s": "orphankey01",
		"ttml_lyrics:south road arlo 150s":         "othersong01",
	} {
		if err := testStore.AddVideoID(ctx, key, vid); err != nil {
			t.Fatal(err)
		}
	}

	seeded, next, err := testStore.SeedBiniLinkBackfill(ctx, "", 1000, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if next != "" {
		t.Fatalf("want end of scan, got cursor %q", next)
	}
	if seeded != 4 {
		t.Fatalf("want 4 seeded, got %d", seeded)
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
	if got["orphankey01"] != "800" || got["stubkey0001"] != "1000" {
		t.Fatalf("keys without an Apple track must not block the video: %+v", got)
	}
	for _, skipped := range []string{"split000001", "noapple0001", "noisrc00001", "halfisrc001", "othersong01"} {
		if _, ok := got[skipped]; ok {
			t.Fatalf("%s must be skipped", skipped)
		}
	}
}

func TestBiniLinkBackfillTrack(t *testing.T) {
	resolved := biniLinkKey{
		CacheKey: "ttml_lyrics:glass meadow orla finch u 0s", AppleTrackID: "1878382599",
		ISRC: "USAAA2600025", DurationMs: 294857,
		TrackName: "Glass Meadow", ArtistName: "Orla Finch", AlbumName: "The Long Field",
	}
	cases := []struct {
		name    string
		ignored biniLinkKey
		want    string
	}{
		{"alternate key of the same recording links", biniLinkKey{CacheKey: "ttml_lyrics:glass meadow orla finch u 295s"}, "1878382599"},
		{"album words from the Apple track link", biniLinkKey{CacheKey: "ttml_lyrics:glass meadow orla finch y the long field 295s"}, "1878382599"},
		{"legacy casing links", biniLinkKey{CacheKey: "ttml_lyrics:Glass Meadow Orla Finch 295s"}, "1878382599"},
		{"a key without a duration blocks the video", biniLinkKey{CacheKey: "ttml_lyrics:glass meadow orla finch"}, ""},
		{"the same key at another length blocks the video", biniLinkKey{CacheKey: "ttml_lyrics:glass meadow orla finch u 341s"}, ""},
		{"a different song with the same opening blocks the video", biniLinkKey{CacheKey: "ttml_lyrics:glass meadow road orla finch 295s"}, ""},
		{"a shorter title blocks the video", biniLinkKey{CacheKey: "ttml_lyrics:glass orla finch 295s"}, ""},
		{"the album title track blocks the video", biniLinkKey{CacheKey: "ttml_lyrics:the long field orla finch 295s"}, ""},
		{"the title words in another order block the video", biniLinkKey{CacheKey: "ttml_lyrics:meadow glass orla finch 295s"}, ""},
		{"a cover by another artist blocks the video", biniLinkKey{CacheKey: "ttml_lyrics:glass meadow wren hale 295s"}, ""},
		{"a re-recording blocks the video", biniLinkKey{CacheKey: "ttml_lyrics:glass meadow (re-recorded) orla finch 295s"}, ""},
		{"a remix blocks the video", biniLinkKey{CacheKey: "ttml_lyrics:glass meadow (remix) orla finch 295s"}, ""},
		{"another ISRC blocks the video", biniLinkKey{CacheKey: "ttml_lyrics:glass meadow orla finch 295s", ISRC: "USAAA2600099"}, ""},
		{"a malformed track ID blocks the video", biniLinkKey{CacheKey: "ttml_lyrics:glass meadow orla finch 295s", AppleTrackID: "x12"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := biniLinkBackfillTrack([]biniLinkKey{resolved, c.ignored}, 2000)
			if got != c.want || ok != (c.want != "") {
				t.Fatalf("got %q, %v; want %q", got, ok, c.want)
			}
		})
	}
}

func TestBiniLinkBackfillTrackAlbumTitleTrack(t *testing.T) {
	resolved := biniLinkKey{
		CacheKey: "ttml_lyrics:blue wren hale 0s", AppleTrackID: "42", ISRC: "USAAA2600042",
		DurationMs: 241000, TrackName: "Blue", ArtistName: "Wren Hale", AlbumName: "Blue Hour",
	}
	if got, ok := biniLinkBackfillTrack([]biniLinkKey{resolved, {CacheKey: "ttml_lyrics:blue hour wren hale 241s"}}, 2000); ok {
		t.Fatalf("the album's title track must not stand in for %q, got %q", resolved.TrackName, got)
	}
	if _, ok := biniLinkBackfillTrack([]biniLinkKey{resolved, {CacheKey: "ttml_lyrics:blue wren hale blue hour 241s"}}, 2000); !ok {
		t.Fatal("a key naming the track and its album must link")
	}
	for _, album := range []string{"Blue Hour (Deluxe)", "Blue Hour - Deluxe Edition", "Blue Hour: The Remixes", "Blue Hour, Vol. 1"} {
		edition := resolved
		edition.AlbumName = album
		if _, ok := biniLinkBackfillTrack([]biniLinkKey{edition, {CacheKey: "ttml_lyrics:blue hour wren hale 241s"}}, 2000); ok {
			t.Fatalf("album %q must not hide its title track", album)
		}
	}
	single := resolved
	single.AlbumName = "Blue - Single"
	if _, ok := biniLinkBackfillTrack([]biniLinkKey{single, {CacheKey: "ttml_lyrics:blue wren hale 241s"}}, 2000); !ok {
		t.Fatal("a self-titled single must link")
	}
}

func TestBiniLinkBackfillTrackBracketedTitle(t *testing.T) {
	variant := biniLinkKey{
		CacheKey: "ttml_lyrics:glass meadow (orla's version) orla finch 0s", AppleTrackID: "77", ISRC: "USAAA2600077",
		DurationMs: 295000, TrackName: "Glass Meadow (Orla's Version)", ArtistName: "Orla Finch",
	}
	plain := biniLinkKey{CacheKey: "ttml_lyrics:glass meadow orla finch 295s"}
	if got, ok := biniLinkBackfillTrack([]biniLinkKey{variant, plain}, 2000); ok {
		t.Fatalf("a plain key must not link to the variant, got %q", got)
	}
	for _, title := range []string{"Glass Meadow (Part 2)", "Glass Meadow (with The Harbor Orchestra)"} {
		other := variant
		other.TrackName = title
		if got, ok := biniLinkBackfillTrack([]biniLinkKey{other, plain}, 2000); ok {
			t.Fatalf("a plain key must not link to %q, got %q", title, got)
		}
	}
	featured := variant
	featured.CacheKey = "ttml_lyrics:glass meadow (feat. wren hale) orla finch 0s"
	featured.TrackName = "Glass Meadow (feat. Wren Hale)"
	if _, ok := biniLinkBackfillTrack([]biniLinkKey{featured, plain}, 2000); !ok {
		t.Fatal("a featured artist bracket must not be required")
	}
}

func TestBiniLinkBackfillTrackTitleRun(t *testing.T) {
	glass := biniLinkKey{
		CacheKey: "ttml_lyrics:glass orla finch 0s", AppleTrackID: "51", ISRC: "USAAA2600051",
		DurationMs: 241000, TrackName: "Glass", ArtistName: "Orla Finch", AlbumName: "Meadow Songs",
	}
	if got, ok := biniLinkBackfillTrack([]biniLinkKey{glass, {CacheKey: "ttml_lyrics:glass meadow orla finch 241s"}}, 2000); ok {
		t.Fatalf("title plus an album word must not spell another song, got %q", got)
	}
	for _, c := range []struct{ album, key string }{
		{"Meadow", "ttml_lyrics:glass meadow orla finch 241s"},
		{"Midnight Sessions", "ttml_lyrics:midnight glass orla finch 241s"},
		{"Glass, Part II", "ttml_lyrics:glass part ii orla finch 241s"},
		{"Glass and Ash", "ttml_lyrics:glass and ash orla finch 241s"},
		{"Glass with Strings", "ttml_lyrics:glass with strings orla finch 241s"},
		{"Meadow Songs", "ttml_lyrics:glass 241s"},
	} {
		other := glass
		other.AlbumName = c.album
		if got, ok := biniLinkBackfillTrack([]biniLinkKey{other, {CacheKey: c.key}}, 2000); ok {
			t.Fatalf("%q on album %q must not link, got %q", c.key, c.album, got)
		}
	}
	if _, ok := biniLinkBackfillTrack([]biniLinkKey{glass, {CacheKey: "ttml_lyrics:glass orla finch meadow songs 241s"}}, 2000); !ok {
		t.Fatal("title, artist and album in key order must link")
	}
}

func TestKeyWordsKeepsCombiningMarks(t *testing.T) {
	if got := keyWords("ttml_lyrics:दिल से 300s"); !slices.Equal(got, []string{"ttml", "lyrics", "दिल", "से", "300s"}) {
		t.Fatalf("got %q", got)
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
			seeded, next, err := testStore.SeedBiniLinkBackfill(ctx, cursor, 2, 2000)
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

	seeded, _, err := testStore.SeedBiniLinkBackfill(ctx, "", 100, 2000)
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

func TestTryBiniLinkWorkerLockIsBounded(t *testing.T) {
	cfg := testStore.pool.Config().ConnConfig
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	silent := *testStore
	poolCfg := testStore.pool.Config()
	poolCfg.ConnConfig = cfg.Copy()
	poolCfg.ConnConfig.Host = "127.0.0.1"
	poolCfg.ConnConfig.Port = uint16(ln.Addr().(*net.TCPAddr).Port)
	poolCfg.ConnConfig.ConnectTimeout = 0
	poolCfg.ConnConfig.Fallbacks = nil
	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	silent.pool = pool

	start := time.Now()
	if _, ok, err := silent.TryBiniLinkWorkerLock(context.Background()); ok || err == nil {
		t.Fatalf("a silent server must fail the lock: ok=%v err=%v", ok, err)
	}
	if elapsed := time.Since(start); elapsed > BiniLinkLockQueryTimeout+2*time.Second {
		t.Fatalf("lock attempt took %v, want about %v", elapsed, BiniLinkLockQueryTimeout)
	}
}

func TestBiniLinkWorkerLockSetsKeepalives(t *testing.T) {
	ctx := context.Background()
	lock, ok, err := testStore.TryBiniLinkWorkerLock(ctx)
	if err != nil || !ok {
		t.Fatalf("lock: ok=%v err=%v", ok, err)
	}
	defer lock.Release()

	for setting, want := range map[string]string{"tcp_keepalives_idle": "30", "tcp_keepalives_interval": "10", "tcp_keepalives_count": "3"} {
		var got string
		if err := lock.conn.QueryRow(ctx, "SELECT current_setting($1)", setting).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s = %q, want %q", setting, got, want)
		}
	}
}
