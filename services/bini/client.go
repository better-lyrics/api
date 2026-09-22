package bini

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"lyrics-api-go/config"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/providers"
	"lyrics-api-go/stats"
	"lyrics-api-go/utils"

	log "github.com/sirupsen/logrus"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

// ingressBaseURL is lrc.red's trusted-contributor ingress base. The lyric and
// metadata endpoints hang off it. Package var so tests can point it at an
// httptest server.
var ingressBaseURL = "https://lrc.red/ingress"

// lrc.red's documented body caps: over these it returns 400, so skip locally.
const (
	maxLyricBytes    = 4 << 20
	maxMetadataBytes = 1 << 20
)

// ingressKey reads the configured key. A var so tests can inject one (config is
// loaded from the environment once at process start, before tests run).
var ingressKey = func() string { return config.Get().Configuration.LRCRedIngressKey }

type lyricResponse struct {
	Status string `json:"status"`
	Action string `json:"action"`
	Sync   string `json:"sync"`
}

type metadataResponse struct {
	Status          string `json:"status"`
	Action          string `json:"action"`
	HadPendingLyric bool   `json:"had_pending_lyric"`
	LyricIndexed    *bool  `json:"lyric_indexed"`
}

// Contribute posts a track's metadata then its lyric to lrc.red, keyed by ISRC.
// It no-ops unless source is Apple, so a lrc.red-sourced fetch is never re-submitted
// (that would land as a lossy "replace" of lrc.red's own record). Fire-and-forget;
// also no-ops when the ingress key is unset. Metadata goes first so an unknown track
// is in-catalog before the lyric (avoids pending_metadata).
func Contribute(trackName, artistName, isrc, source, rawAttributes, ttmlRaw string) {
	if source != providers.SourceApple {
		return
	}
	key := ingressKey()
	if key == "" {
		stats.Get().RecordLRCRedContribute(false)
		return
	}

	normISRC := utils.NormalizeISRC(isrc)
	if normISRC == "" {
		stats.Get().RecordLRCRedContribute(false)
		log.Warnf("%s Skipping ingress: missing or malformed ISRC %q for %s - %s", logcolors.LogBini, isrc, trackName, artistName)
		return
	}

	if rawAttributes != "" {
		postMetadata(key, normISRC, trackName, artistName, rawAttributes)
	}
	if ttmlRaw != "" {
		postLyric(key, normISRC, trackName, artistName, ttmlRaw)
	}
	stats.Get().RecordLRCRedContribute(true)
}

// PostLyrics is the entry point kept for the legacy call sites, which cannot pass
// provenance. It forwards an empty source so Contribute never contributes from the
// legacy path (which now resolves lrc.red first and could otherwise re-submit it).
func PostLyrics(trackName, artistName, albumName string, durationMs int, ttmlRaw, isrc string) {
	Contribute(trackName, artistName, isrc, "", "", ttmlRaw)
}

func postMetadata(key, isrc, trackName, artistName, rawAttributes string) {
	if len(rawAttributes) > maxMetadataBytes {
		log.Warnf("%s Skipping metadata ingress: attributes over 1MB (%d bytes) for ISRC %s", logcolors.LogBini, len(rawAttributes), isrc)
		return
	}

	req, err := buildIngressRequest(key, "/metadata", isrc, rawAttributes, "application/json")
	if err != nil {
		log.Errorf("%s Failed to build metadata request: %v", logcolors.LogBini, err)
		return
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Errorf("%s Metadata POST failed: %v", logcolors.LogBini, err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		// 400/401 are permanent; fire-and-forget never retries.
		log.Warnf("%s Metadata rejected isrc=%s http=%d body=%s", logcolors.LogBini, isrc, resp.StatusCode, string(body))
		return
	}

	var mr metadataResponse
	_ = json.Unmarshal(body, &mr)
	log.Infof("%s Metadata accepted isrc=%s status=%s action=%s hadPendingLyric=%t", logcolors.LogBini, isrc, mr.Status, mr.Action, mr.HadPendingLyric)
}

func postLyric(key, isrc, trackName, artistName, ttmlRaw string) {
	if len(ttmlRaw) > maxLyricBytes {
		log.Warnf("%s Skipping lyric ingress: TTML over 4MB (%d bytes) for ISRC %s", logcolors.LogBini, len(ttmlRaw), isrc)
		return
	}

	req, err := buildIngressRequest(key, "/lyrics", isrc, ttmlRaw, "application/ttml+xml")
	if err != nil {
		log.Errorf("%s Failed to build lyric request: %v", logcolors.LogBini, err)
		return
	}

	log.Infof("%s Contributing lyric to lrc.red: isrc=%s track=%q artist=%q", logcolors.LogBini, isrc, trackName, artistName)

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Errorf("%s Lyric POST failed: %v", logcolors.LogBini, err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		// 400/401 are permanent (bad body / auth) and 429 is rate limiting;
		// fire-and-forget means none are retried here.
		log.Warnf("%s Lyric rejected isrc=%s http=%d body=%s", logcolors.LogBini, isrc, resp.StatusCode, string(body))
		return
	}

	var lr lyricResponse
	_ = json.Unmarshal(body, &lr)
	log.Infof("%s Lyric accepted isrc=%s status=%s action=%s sync=%s", logcolors.LogBini, isrc, lr.Status, lr.Action, lr.Sync)
}

// buildIngressRequest constructs an ingress POST for the given sub-path
// ("/lyrics" or "/metadata") with the ISRC in the query string.
func buildIngressRequest(key, path, isrc, body, contentType string) (*http.Request, error) {
	req, err := http.NewRequest("POST", ingressBaseURL+path+"?isrc="+isrc, bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Ingress-Key", key)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", config.Get().Configuration.LRCRedUserAgent)
	return req, nil
}
