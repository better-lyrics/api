package bini

import (
	"bytes"
	"encoding/json"
	"io"
	"lyrics-api-go/config"
	"lyrics-api-go/logcolors"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

type postLyricsPayload struct {
	TrackName      string `json:"track_name"`
	ArtistName     string `json:"artist_name"`
	AlbumName      string `json:"album_name"`
	OriginalSource string `json:"originalSource"`
	Duration       int    `json:"duration"`
	TTMLRaw        string `json:"ttml_raw"`
	ISRC           string `json:"isrc,omitempty"`
}

// PostLyrics sends lyrics data to the external Bini API.
// This is fire-and-forget; errors are logged but not returned.
func PostLyrics(trackName, artistName, albumName string, durationMs int, ttmlRaw, isrc string) {
	cfg := config.Get()
	if cfg.Configuration.BiniAPIKey == "" {
		return
	}

	payload := postLyricsPayload{
		TrackName:      trackName,
		ArtistName:     artistName,
		AlbumName:      albumName,
		OriginalSource: "Apple",
		Duration:       durationMs / 1000,
		TTMLRaw:        ttmlRaw,
		ISRC:           isrc,
	}

	log.Infof("%s Payload: track=%q artist=%q album=%q duration=%d isrc=%q", logcolors.LogBini, trackName, artistName, albumName, payload.Duration, isrc)

	body, err := json.Marshal(payload)
	if err != nil {
		log.Errorf("%s Failed to marshal payload: %v", logcolors.LogBini, err)
		return
	}

	req, err := http.NewRequest("POST", cfg.Configuration.BiniAPIURL, bytes.NewReader(body))
	if err != nil {
		log.Errorf("%s Failed to create request: %v", logcolors.LogBini, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.Configuration.BiniAPIKey)
	if cfg.Configuration.BiniSecretKey != "" {
		req.Header.Set("X-Bini-Secret", cfg.Configuration.BiniSecretKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Errorf("%s POST failed: %v", logcolors.LogBini, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Infof("%s Posted lyrics for: %s - %s (ISRC: %s)", logcolors.LogBini, trackName, artistName, isrc)
	} else {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		log.Warnf("%s POST returned status %d for: %s - %s | body: %s", logcolors.LogBini, resp.StatusCode, trackName, artistName, string(respBody))
	}
}
