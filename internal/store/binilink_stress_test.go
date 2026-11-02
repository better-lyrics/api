//go:build stress

package store

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func TestStressBiniLinkQueue(t *testing.T) {
	const (
		workers        = 64
		callsPerWorker = 500
		videos         = 400
		cacheKeys      = 40
	)
	resetTables(t)
	ctx := context.Background()

	videoIDs := make([]string, videos)
	for i := range videoIDs {
		videoIDs[i] = fmt.Sprintf("stress%05d", i)
	}
	invalidVideos := []string{"", "short", "stress00000x", "bad id!!!!!", "stress0000\n"}
	invalidApples := []string{"", "abc", "1.5", "-1", "１２３"}

	keyApple := map[string]string{}
	for i := 0; i < cacheKeys; i++ {
		key := fmt.Sprintf("ttml_lyrics:stress song %d", i)
		apple := fmt.Sprintf("%d", 2000000000+i)
		if i%5 == 0 {
			apple = invalidApples[i/5%len(invalidApples)]
		}
		keyApple[key] = apple
		if err := testStore.SetSongMetadata(ctx, &SongMetadata{CacheKey: key, AppleTrackID: apple, ISRC: fmt.Sprintf("USSTR%07d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	keys := make([]string, 0, cacheKeys)
	for k := range keyApple {
		keys = append(keys, k)
	}

	firstApple := map[string]string{}
	for i, v := range videoIDs[:videos/4] {
		apple := fmt.Sprintf("%d", 3000000000+i)
		if err := testStore.EnqueueBiniLink(ctx, v, apple); err != nil {
			t.Fatal(err)
		}
		firstApple[v] = apple
	}

	type mapping struct{ key, video string }
	var (
		mu          sync.Mutex
		errs        []error
		attempted   = map[string]map[string]bool{}
		mapped      = map[mapping]bool{}
		observed    = map[string]string{}
		conflicting []string
	)
	record := func(err error) {
		if err != nil {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}
	}
	attempt := func(video, apple string) {
		mu.Lock()
		defer mu.Unlock()
		if attempted[video] == nil {
			attempted[video] = map[string]bool{}
		}
		attempted[video][apple] = true
	}

	start := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < callsPerWorker; i++ {
				video := videoIDs[rng.Intn(videos)]
				if rng.Intn(20) == 0 {
					video = invalidVideos[rng.Intn(len(invalidVideos))]
				}
				switch op := rng.Intn(10); {
				case op < 4:
					apple := fmt.Sprintf("%d", 4000000000+rng.Intn(5))
					if rng.Intn(10) == 0 {
						apple = invalidApples[rng.Intn(len(invalidApples))]
					}
					if biniLinkVideoID.MatchString(video) && biniLinkAppleTrackID.MatchString(apple) {
						attempt(video, apple)
					}
					record(testStore.EnqueueBiniLink(ctx, video, apple))
				case op < 8:
					key := keys[rng.Intn(len(keys))]
					if biniLinkVideoID.MatchString(video) && biniLinkAppleTrackID.MatchString(keyApple[key]) {
						attempt(video, keyApple[key])
					}
					err := testStore.AddVideoIDAndEnqueueBiniLink(ctx, key, video)
					record(err)
					if err == nil && video != "" {
						mu.Lock()
						mapped[mapping{key, video}] = true
						mu.Unlock()
					}
				default:
					rows, err := testStore.ClaimBiniLinkPending(ctx, 10)
					record(err)
					for _, row := range rows {
						mu.Lock()
						if prev, ok := observed[row.VideoID]; ok && prev != row.AppleTrackID {
							conflicting = append(conflicting, row.VideoID)
						}
						observed[row.VideoID] = row.AppleTrackID
						mu.Unlock()
						status := []string{"pending", "linked", "exists", "conflict"}[rng.Intn(4)]
						record(testStore.SetBiniLinkResult(ctx, row.VideoID, status, row.Attempts+1, time.Now().Add(-time.Second)))
					}
				}
			}
		}(int64(w + 1))
	}
	wg.Wait()
	elapsed := time.Since(start)

	if len(errs) > 0 {
		t.Fatalf("%d store errors, first: %v", len(errs), errs[0])
	}
	if len(conflicting) > 0 {
		t.Fatalf("apple id changed under a queued video: %v", conflicting[:min(5, len(conflicting))])
	}

	rows, err := testStore.pool.Query(ctx, `SELECT video_id, apple_track_id FROM binilink_links`)
	if err != nil {
		t.Fatal(err)
	}
	final := map[string]string{}
	for rows.Next() {
		var v, a string
		if err := rows.Scan(&v, &a); err != nil {
			t.Fatal(err)
		}
		if _, dup := final[v]; dup {
			t.Fatalf("duplicate row for %s", v)
		}
		final[v] = a
	}
	rows.Close()

	for v, a := range final {
		if !biniLinkVideoID.MatchString(v) || !biniLinkAppleTrackID.MatchString(a) {
			t.Errorf("invalid pair queued: %q -> %q", v, a)
		}
		if want, ok := firstApple[v]; ok && a != want {
			t.Errorf("%s: apple %s, want the first queued %s", v, a, want)
		}
		if _, pre := firstApple[v]; !pre && !attempted[v][a] {
			t.Errorf("%s: apple %s was never offered for it", v, a)
		}
		if prev, ok := observed[v]; ok && prev != a {
			t.Errorf("%s: apple changed from %s to %s", v, prev, a)
		}
	}
	for v := range attempted {
		if _, ok := final[v]; !ok {
			t.Errorf("%s was offered a valid apple id but has no row", v)
		}
	}
	for v := range firstApple {
		if _, ok := final[v]; !ok {
			t.Errorf("%s lost its first queued row", v)
		}
	}
	for m := range mapped {
		var n int
		if err := testStore.pool.QueryRow(ctx, `SELECT count(*) FROM video_map WHERE video_id = $1 AND cache_key = $2`, m.video, m.key).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("video_map rows for %q on %q = %d, want 1", m.video, m.key, n)
		}
	}
	t.Logf("%d calls in %v (%.0f calls/s): %d rows queued, %d video_map pairs checked",
		workers*callsPerWorker, elapsed, float64(workers*callsPerWorker)/elapsed.Seconds(), len(final), len(mapped))
}
