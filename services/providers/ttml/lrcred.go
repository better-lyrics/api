package ttml

import (
	"context"
	"encoding/json"
	"errors"
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

var lrcRedSkipAISynced = config.Get().FeatureFlags.LRCRedSkipAISynced

// errLRCRedFlagCheck marks a failed byModel lookup, the only lrc.red error that leaves an Apple miss unconfirmed.
var errLRCRedFlagCheck = errors.New("lrc.red byModel check failed")

var errLRCRedModelSynced = errors.New("lrc.red file is model-synced")

func lrcRedHasUnservedFile(err error) bool {
	return errors.Is(err, errLRCRedModelSynced) || errors.Is(err, errLRCRedFlagCheck)
}

var lrcRedClient = &http.Client{Timeout: 10 * time.Second}

// lrcRedSem caps concurrent outbound lrc.red reads at 8: a safety bound against a
// runaway (a retry loop or cache-write failure defeating "fetch once ever"), not
// rate limiting. The minted-token search lane removed the old MUT-throughput ceiling.
var lrcRedSem = make(chan struct{}, 8)

// fetchLRCRedByISRC fetches TTML from lrc.red by ISRC: (ttml,true,nil) on 200,
// ("",false,nil) on 404 or empty ISRC (a clean miss), ("",false,err) otherwise.
func fetchLRCRedByISRC(isrc string) (ttml string, ok bool, err error) {
	if lrcRedReadBase == "" {
		return "", false, nil
	}
	isrc = utils.NormalizeISRC(isrc)
	if isrc == "" {
		return "", false, nil
	}

	defer func() {
		if lrcRedHasUnservedFile(err) {
			stats.Get().RecordLRCRedFetch(false, nil)
			return
		}
		stats.Get().RecordLRCRedFetch(ok, err)
	}()

	lrcRedSem <- struct{}{}
	defer func() { <-lrcRedSem }()

	if !lrcRedSkipAISynced {
		return fetchLRCRedTTML(context.Background(), isrc)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()

	type flagResult struct {
		byModel bool
		err     error
	}
	// A parallel byModel read takes a second slot only while the semaphore is under half full, so bursts of lookups keep most slots.
	var flag chan flagResult
	if len(lrcRedSem) <= cap(lrcRedSem)/2 {
		select {
		case lrcRedSem <- struct{}{}:
			flag = make(chan flagResult, 1)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-lrcRedSem }()
				byModel, err := lrcRedByModel(ctx, isrc)
				flag <- flagResult{byModel, err}
			}()
		default:
		}
	}

	ttml, ok, err = fetchLRCRedTTML(ctx, isrc)
	if err != nil || !ok {
		return ttml, ok, err
	}

	var f flagResult
	if flag != nil {
		f = <-flag
	} else {
		f.byModel, f.err = lrcRedByModel(ctx, isrc)
	}
	if f.err != nil {
		stats.Get().RecordLRCRedFlagError()
		return "", false, fmt.Errorf("%w: %w", errLRCRedFlagCheck, f.err)
	}
	if f.byModel {
		stats.Get().RecordLRCRedSkippedByModel()
		log.Infof("%s lrc.red file for ISRC %s is model-synced, skipping", logcolors.LogLyrics, isrc)
		return "", false, errLRCRedModelSynced
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

var (
	errAISyncFlagOff = errors.New("FF_LRC_RED_SKIP_AI_SYNCED is off; the sweep only runs while model-synced lrc.red files are skipped")
	errAISyncNoBase  = errors.New("LRC_RED_BASE_URL is not set; the sweep has nothing to check against")
)

func AISyncCheckReady() error {
	if !lrcRedSkipAISynced {
		return errAISyncFlagOff
	}
	if lrcRedReadBase == "" {
		return errAISyncNoBase
	}
	return nil
}

func LRCRedByModel(ctx context.Context, isrc string) (bool, error) {
	release, err := acquireLRCRedSlot(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	return lrcRedByModel(ctx, isrc)
}

func LRCRedTTML(ctx context.Context, isrc string) (string, bool, error) {
	release, err := acquireLRCRedSlot(ctx)
	if err != nil {
		return "", false, err
	}
	defer release()
	return fetchLRCRedTTML(ctx, isrc)
}

// acquireLRCRedSlot waits until the semaphore is under half full, so background reads never take the slots lookups need.
func acquireLRCRedSlot(ctx context.Context) (release func(), err error) {
	if lrcRedReadBase == "" {
		return nil, errors.New("LRC_RED_BASE_URL is not set")
	}
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if len(lrcRedSem) < cap(lrcRedSem)/2 {
			select {
			case lrcRedSem <- struct{}{}:
				return func() { <-lrcRedSem }, nil
			default:
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}

// lrcRedSongJSONMaxBytes bounds the decode; .song.json carries the full lyrics, so a runaway body must not exhaust memory.
const lrcRedSongJSONMaxBytes = 4 << 20

func lrcRedByModel(ctx context.Context, isrc string) (bool, error) {
	resp, err := lrcRedGet(ctx, isrc+".song.json", "application/json")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("lrc.red song.json returned status %d", resp.StatusCode)
	}
	body := io.LimitReader(resp.Body, lrcRedSongJSONMaxBytes)
	var song *struct {
		ByModel bool `json:"byModel"`
	}
	if err := json.NewDecoder(body).Decode(&song); err != nil {
		return false, fmt.Errorf("decode lrc.red song.json: %w", err)
	}
	if song == nil {
		return false, errors.New("lrc.red song.json is null")
	}
	_, _ = io.Copy(io.Discard, body)
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
