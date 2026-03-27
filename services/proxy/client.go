package proxy

import (
	"fmt"
	"lyrics-api-go/config"
	"lyrics-api-go/logcolors"
	"net/http"
	"net/url"
	"time"

	log "github.com/sirupsen/logrus"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

// RevalidateByVideoID calls the proxy's /revalidate endpoint for a specific videoId.
// Fire-and-forget: errors are logged but not returned.
func RevalidateByVideoID(videoID, song, artist, album string, duration int) {
	cfg := config.Get()

	params := url.Values{}
	params.Set("videoId", videoID)
	if song != "" {
		params.Set("song", song)
	}
	if artist != "" {
		params.Set("artist", artist)
	}
	if album != "" {
		params.Set("album", album)
	}
	if duration > 0 {
		params.Set("duration", fmt.Sprintf("%d", duration))
	}

	reqURL := cfg.Configuration.ProxyRevalidateURL + "?" + params.Encode()

	req, err := http.NewRequest("POST", reqURL, nil)
	if err != nil {
		log.Errorf("%s Failed to create request for videoId %s: %v", logcolors.LogProxy, videoID, err)
		return
	}
	req.Header.Set("x-admin-key", cfg.Configuration.ProxyAPIKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Errorf("%s POST failed for videoId %s: %v", logcolors.LogProxy, videoID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Infof("%s Revalidated proxy cache for videoId: %s (%s - %s)", logcolors.LogProxy, videoID, song, artist)
	} else {
		log.Warnf("%s Proxy revalidation returned status %d for videoId: %s", logcolors.LogProxy, resp.StatusCode, videoID)
	}
}

// RevalidateAllForSong looks up ALL videoIds across all duration variants of a song
// and calls proxy revalidate for each.
// videoIDsFunc is injected to avoid circular dependency with the main package.
// NO-OPS when PROXY_REVALIDATE_URL or PROXY_API_KEY is empty, or no videoIds found.
func RevalidateAllForSong(song, artist, album string, duration int, videoIDsFunc func(string, string) []string) {
	cfg := config.Get()
	if cfg.Configuration.ProxyRevalidateURL == "" || cfg.Configuration.ProxyAPIKey == "" {
		return
	}

	videoIDs := videoIDsFunc(song, artist)
	if len(videoIDs) == 0 {
		return
	}

	log.Infof("%s Revalidating %d proxy cache entries for: %s - %s", logcolors.LogProxy, len(videoIDs), song, artist)
	for _, vid := range videoIDs {
		RevalidateByVideoID(vid, song, artist, album, duration)
	}
}
