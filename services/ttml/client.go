package ttml

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"lyrics-api-go/config"
	"net/http"
	"net/url"
	"time"

	log "github.com/sirupsen/logrus"
)

// =============================================================================
// HTTP REQUEST HANDLING
// =============================================================================

func makeAPIRequest(urlStr string, retries int) (*http.Response, error) {
	if accountManager == nil {
		initAccountManager()
	}

	account := accountManager.getCurrentAccount()
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}

	// Set headers for web auth
	req.Header.Set("Authorization", "Bearer "+account.BearerToken)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Origin", "https://music.apple.com")
	req.Header.Set("Referer", "https://music.apple.com")
	if account.MediaUserToken != "" {
		req.Header.Set("media-user-token", account.MediaUserToken)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	// Handle rate limiting and auth errors with retry
	if (resp.StatusCode == 401 || resp.StatusCode == 429) && retries < 3 {
		resp.Body.Close()
		log.Warnf("TTML API returned %d, switching account and retrying...", resp.StatusCode)
		accountManager.switchToNextAccount()
		time.Sleep(time.Second * time.Duration(retries+1))
		return makeAPIRequest(urlStr, retries+1)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("TTML API returned status %d: %s", resp.StatusCode, string(body))
	}

	return resp, nil
}

// =============================================================================
// API FUNCTIONS
// =============================================================================

func searchTrack(query string, storefront string) (*Track, error) {
	if query == "" {
		return nil, fmt.Errorf("empty search query")
	}

	if storefront == "" {
		storefront = "us" // Default to US storefront
	}

	conf := config.Get()
	searchURL := conf.Configuration.TTMLBaseURL + fmt.Sprintf(
		conf.Configuration.TTMLSearchPath,
		storefront,
		url.QueryEscape(query),
	)

	resp, err := makeAPIRequest(searchURL, 0)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read search response: %v", err)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("empty search response body")
	}

	var searchResp SearchResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return nil, fmt.Errorf("failed to parse search response: %v", err)
	}

	if len(searchResp.Results.Songs.Data) == 0 {
		return nil, fmt.Errorf("no tracks found for query: %s", query)
	}

	// Return the first (best) match
	return &searchResp.Results.Songs.Data[0], nil
}

func fetchLyricsTTML(trackID string, storefront string) (string, error) {
	conf := config.Get()
	lyricsURL := conf.Configuration.TTMLBaseURL + fmt.Sprintf(
		conf.Configuration.TTMLLyricsPath,
		storefront,
		trackID,
	)

	resp, err := makeAPIRequest(lyricsURL, 0)
	if err != nil {
		return "", fmt.Errorf("lyrics request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read lyrics response: %v", err)
	}

	var lyricsResp LyricsResponse
	if err := json.Unmarshal(body, &lyricsResp); err != nil {
		return "", fmt.Errorf("failed to parse lyrics response: %v", err)
	}

	log.Debugf("[TTML Fetch] Parsed lyrics response, data entries: %d", len(lyricsResp.Data))

	if len(lyricsResp.Data) == 0 {
		return "", fmt.Errorf("no lyrics data found")
	}

	ttml := lyricsResp.Data[0].Attributes.TTML
	log.Debugf("[TTML Fetch] TTML field length: %d", len(ttml))

	if ttml == "" {
		ttml = lyricsResp.Data[0].Attributes.TTMLLocalizations
		log.Debugf("[TTML Fetch] Using TTMLLocalizations instead, length: %d", len(ttml))
	}

	if ttml == "" {
		return "", fmt.Errorf("TTML content is empty")
	}

	log.Debugf("[TTML Fetch] Successfully fetched TTML content, length: %d bytes", len(ttml))
	return ttml, nil
}
