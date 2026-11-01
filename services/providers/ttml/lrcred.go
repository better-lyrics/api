package ttml

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"lyrics-api-go/config"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/stats"
	"lyrics-api-go/utils"

	log "github.com/sirupsen/logrus"
)

// lrcRedReadBase is the lrc.red read origin, from LRC_RED_BASE_URL (empty
// disables the lrc.red lookup). A package var so tests can point it at a stub.
var lrcRedReadBase = config.Get().Configuration.LRCRedBaseURL

// lrcRedSkipAISynced is FF_LRC_RED_SKIP_AI_SYNCED. A package var so tests can flip it.
var lrcRedSkipAISynced = config.Get().FeatureFlags.LRCRedSkipAISynced

var lrcRedClient = &http.Client{Timeout: 10 * time.Second}

// lrcRedSem caps concurrent outbound lrc.red reads at 8: a safety bound against a
// runaway (a retry loop or cache-write failure defeating "fetch once ever"), not
// rate limiting. The minted-token search lane removed the old MUT-throughput ceiling.
// The cap counts requests, so a .ttml read and its .song.json flag lookup take two slots.
var lrcRedSem = make(chan struct{}, 8)

// lrcRedAcquireMu makes a multi-slot acquire atomic: without it, eight callers each
// holding one slot while waiting for a second would deadlock. Releases skip the lock.
var lrcRedAcquireMu sync.Mutex

func acquireLRCRed(slots int) (release func()) {
	lrcRedAcquireMu.Lock()
	for range slots {
		lrcRedSem <- struct{}{}
	}
	lrcRedAcquireMu.Unlock()
	return func() {
		for range slots {
			<-lrcRedSem
		}
	}
}

// fetchLRCRedByISRC fetches TTML from lrc.red by ISRC: (ttml,true,nil) on 200,
// ("",false,nil) on 404, empty ISRC, or a model-synced file when
// FF_LRC_RED_SKIP_AI_SYNCED is on (a clean miss), ("",false,err) otherwise.
func fetchLRCRedByISRC(isrc string) (ttml string, ok bool, err error) {
	if lrcRedReadBase == "" {
		return "", false, nil
	}
	isrc = utils.NormalizeISRC(isrc)
	if isrc == "" {
		return "", false, nil
	}

	defer func() { stats.Get().RecordLRCRedFetch(ok, err) }()

	checkByModel := lrcRedSkipAISynced
	slots := 1
	if checkByModel {
		slots = 2
	}
	defer acquireLRCRed(slots)()

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()

	type flagResult struct {
		byModel bool
		err     error
	}
	var flag chan flagResult
	if checkByModel {
		flag = make(chan flagResult, 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			byModel, err := LRCRedByModel(ctx, isrc)
			flag <- flagResult{byModel, err}
		}()
	}

	ttml, ok, err = fetchLRCRedTTML(ctx, isrc)
	if err != nil || !ok || flag == nil {
		return ttml, ok, err
	}

	f := <-flag
	if f.err != nil {
		stats.Get().RecordLRCRedFlagError()
		return "", false, fmt.Errorf("lrc.red byModel check failed: %w", f.err)
	}
	if f.byModel {
		stats.Get().RecordLRCRedSkippedByModel()
		log.Infof("%s lrc.red file for ISRC %s is model-synced, skipping", logcolors.LogLyrics, isrc)
		return "", false, nil
	}
	return ttml, true, nil
}

func fetchLRCRedTTML(ctx context.Context, isrc string) (string, bool, error) {
	resp, err := lrcRedGet(ctx, isrc+".ttml", "application/ttml+xml")
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", false, fmt.Errorf("failed to read lrc.red response: %w", err)
		}
		if len(body) == 0 {
			return "", false, nil
		}
		log.Infof("%s lrc.red hit for ISRC %s (%d bytes)", logcolors.LogLyrics, isrc, len(body))
		return string(body), true, nil
	case http.StatusNotFound:
		return "", false, nil
	default:
		return "", false, fmt.Errorf("lrc.red returned status %d", resp.StatusCode)
	}
}

// LRCRedByModel reports whether lrc.red's file for a normalized ISRC was synced by
// a model ("Sync with AI"). The flag lives only in .song.json; any failure to read
// it is an error so callers fail closed.
func LRCRedByModel(ctx context.Context, isrc string) (bool, error) {
	resp, err := lrcRedGet(ctx, isrc+".song.json", "application/json")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("lrc.red song.json returned status %d", resp.StatusCode)
	}
	var song struct {
		ByModel bool `json:"byModel"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&song); err != nil {
		return false, fmt.Errorf("decode lrc.red song.json: %w", err)
	}
	return song.ByModel, nil
}

func lrcRedGet(ctx context.Context, file, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", lrcRedReadBase+"/s/"+file, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", config.Get().Configuration.LRCRedUserAgent)
	req.Header.Set("Accept", accept)
	resp, err := lrcRedClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lrc.red request failed: %w", err)
	}
	return resp, nil
}
