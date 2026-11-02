//go:build conformance && stress

package contracttest

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"lyrics-api-go/internal/store"
)

func TestConformanceNewBiniLinkStress(t *testing.T) {
	const (
		requests    = 3000
		concurrency = 50
		videos      = 300
		songs       = 10
	)
	ensurePostgres(t)
	resetBiniLinkTables(t)
	stub := upstreamStub(t)
	env := stubEnv(stub.URL)
	env["FF_BINILINK_INGRESS"] = "true"
	env["API_KEY_REQUIRED"] = "true"
	seed := append([]SeedLyrics(nil), seedLyricsData...)
	for i := 0; i < songs; i++ {
		seed = append(seed, SeedLyrics{Song: fmt.Sprintf("Stress Song %d", i), Artist: "Stress Tester", Duration: "200", TTML: "<tt>STRESS</tt>"})
	}
	base := newServerBase(t, env, seed, seedNegativeData)

	_, st := ensurePostgres(t)
	ctx := context.Background()
	for i := 0; i < songs; i++ {
		if err := st.SetSongMetadata(ctx, &store.SongMetadata{
			CacheKey:     normalizedCacheKey(fmt.Sprintf("Stress Song %d", i), "Stress Tester", "", "200"),
			AppleTrackID: fmt.Sprintf("%d", 2100000000+i),
			ISRC:         fmt.Sprintf("USSTRS%06d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	want := map[string]string{}
	type job struct{ videoID, song string }
	jobs := make([]job, 0, requests)
	junk := []string{"short", "bad id!!!!!", "toolong00000", "stressééééé"}
	for i := 0; i < requests; i++ {
		song := i % songs
		if i%11 == 10 {
			jobs = append(jobs, job{junk[i%len(junk)], fmt.Sprintf("Stress Song %d", song)})
			continue
		}
		v := fmt.Sprintf("stv%08d", i%videos)
		song = i % videos % songs
		want[v] = fmt.Sprintf("%d", 2100000000+song)
		jobs = append(jobs, job{v, fmt.Sprintf("Stress Song %d", song)})
	}

	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: concurrency}}
	latencies := make([]time.Duration, len(jobs))
	failures := make(chan string, len(jobs))
	work := make(chan int)
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				q := lyricsQuery(jobs[i].song, "Stress Tester", "200", jobs[i].videoID)
				req, _ := http.NewRequest(http.MethodGet, base+"/getLyrics?"+q.Encode(), nil)
				req.Header.Set("X-API-Key", "test-api-key")
				began := time.Now()
				resp, err := client.Do(req)
				latencies[i] = time.Since(began)
				if err != nil {
					failures <- err.Error()
					continue
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Cache-Status") != "HIT" {
					failures <- fmt.Sprintf("%s: %d %s", jobs[i].videoID, resp.StatusCode, resp.Header.Get("X-Cache-Status"))
				}
			}
		}()
	}
	for i := range jobs {
		work <- i
	}
	close(work)
	wg.Wait()
	elapsed := time.Since(start)
	close(failures)
	var failed []string
	for f := range failures {
		failed = append(failed, f)
	}
	if len(failed) > 0 {
		t.Fatalf("%d of %d requests failed, e.g. %v", len(failed), len(jobs), failed[:min(5, len(failed))])
	}

	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p50, p99 := sorted[len(sorted)/2], sorted[len(sorted)*99/100]

	queued := func() map[string]string {
		rows, err := st.Pool().Query(ctx, `SELECT video_id, apple_track_id FROM binilink_links`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var v, a string
			if err := rows.Scan(&v, &a); err != nil {
				t.Fatal(err)
			}
			out[v] = a
		}
		return out
	}
	deadline := time.Now().Add(10 * time.Second)
	got := queued()
	for len(got) < len(want) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		got = queued()
	}
	time.Sleep(500 * time.Millisecond)
	got = queued()
	if len(got) != len(want) {
		t.Errorf("queue holds %d videos, want %d", len(got), len(want))
	}
	for v, a := range want {
		if got[v] != a {
			t.Errorf("%s queued as %q, want %q", v, got[v], a)
		}
	}
	for v := range got {
		if _, ok := want[v]; !ok {
			t.Errorf("unexpected video queued: %q", v)
		}
	}
	t.Logf("%d requests at concurrency %d in %v: %.0f req/s, p50 %v, p99 %v, max %v; %d videos queued",
		len(jobs), concurrency, elapsed, float64(len(jobs))/elapsed.Seconds(), p50, p99, sorted[len(sorted)-1], len(got))
}
