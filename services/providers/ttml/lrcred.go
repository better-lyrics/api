package ttml

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"lyrics-api-go/config"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/utils"

	log "github.com/sirupsen/logrus"
)

// lrcRedReadBase is the lrc.red read origin, from LRC_RED_BASE_URL (empty
// disables the lrc.red lookup). A package var so tests can point it at a stub.
var lrcRedReadBase = config.Get().Configuration.LRCRedBaseURL

var lrcRedClient = &http.Client{Timeout: 10 * time.Second}

// lrcRedSem caps concurrent outbound lrc.red reads at 8: a safety bound against a
// runaway (a retry loop or cache-write failure defeating "fetch once ever"), not
// rate limiting. The minted-token search lane removed the old MUT-throughput ceiling.
var lrcRedSem = make(chan struct{}, 8)

// fetchLRCRedByISRC fetches TTML from lrc.red by ISRC: (ttml,true,nil) on 200,
// ("",false,nil) on 404 or empty ISRC (a clean miss), ("",false,err) otherwise.
func fetchLRCRedByISRC(isrc string) (string, bool, error) {
	if lrcRedReadBase == "" {
		return "", false, nil
	}
	isrc = utils.NormalizeISRC(isrc)
	if isrc == "" {
		return "", false, nil
	}

	lrcRedSem <- struct{}{}
	defer func() { <-lrcRedSem }()

	url := lrcRedReadBase + "/s/" + isrc + ".ttml"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("User-Agent", config.Get().Configuration.LRCRedUserAgent)
	req.Header.Set("Accept", "application/ttml+xml")

	resp, err := lrcRedClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("lrc.red request failed: %w", err)
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
