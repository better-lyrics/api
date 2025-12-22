package legacy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"lyrics-api-go/config"
	"lyrics-api-go/logcolors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

var (
	conf       = config.Get()
	httpClient = &http.Client{
		Timeout: 10 * time.Second,
	}

	// Token cache
	tokenCache     sync.Map
	oauthTokenKey  = "legacy_oauth_token"
	accessTokenKey = "legacy_access_token"
)

type cachedToken struct {
	Token      string
	Expiration int64
}

// setCommonHeaders sets the common headers for requests
func setCommonHeaders(req *http.Request) {
	req.Header.Set("App-Platform", conf.Configuration.AppPlatform)
	req.Header.Set("User-Agent", conf.Configuration.UserAgent)
	if conf.Configuration.CookieStringFormat != "" && conf.Configuration.CookieValue != "" {
		req.Header.Set("cookie", fmt.Sprintf(conf.Configuration.CookieStringFormat, conf.Configuration.CookieValue))
	}
}

// getOAuthAccessToken gets OAuth token for Spotify API
func getOAuthAccessToken() (string, error) {
	clientID := conf.Configuration.ClientID
	clientSecret := conf.Configuration.ClientSecret
	oauthTokenURL := conf.Configuration.OauthTokenUrl

	if clientID == "" || clientSecret == "" || oauthTokenURL == "" {
		return "", fmt.Errorf("OAuth credentials not configured")
	}

	// Check cache
	if cached, ok := tokenCache.Load(oauthTokenKey); ok {
		ct := cached.(cachedToken)
		if time.Now().UnixNano() < ct.Expiration {
			log.Debugf("%s [Legacy] Using cached OAuth token", logcolors.LogCache)
			return ct.Token, nil
		}
	}

	auth := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))

	data := url.Values{}
	data.Set("grant_type", "client_credentials")

	req, err := http.NewRequest("POST", oauthTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("error creating token request: %w", err)
	}

	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("error making token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading token response: %w", err)
	}

	var tokenResp OAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("error parsing token response: %w", err)
	}

	// Cache the token
	tokenCache.Store(oauthTokenKey, cachedToken{
		Token:      tokenResp.AccessToken,
		Expiration: time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).UnixNano(),
	})

	log.Debugf("%s [Legacy] Cached new OAuth token", logcolors.LogCache)
	return tokenResp.AccessToken, nil
}

// getValidAccessToken gets the lyrics access token
func getValidAccessToken() (string, error) {
	tokenURL := conf.Configuration.TokenUrl
	if tokenURL == "" {
		return "", fmt.Errorf("token URL not configured")
	}

	// Check cache
	if cached, ok := tokenCache.Load(accessTokenKey); ok {
		ct := cached.(cachedToken)
		if time.Now().UnixNano() < ct.Expiration {
			log.Debugf("%s [Legacy] Using cached access token", logcolors.LogCache)
			return ct.Token, nil
		}
	}

	req, err := http.NewRequest("GET", tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("error creating request: %w", err)
	}
	setCommonHeaders(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response: %w", err)
	}

	var tokenData TokenData
	if err := json.Unmarshal(body, &tokenData); err != nil {
		return "", fmt.Errorf("error parsing response: %w", err)
	}

	// Calculate expiration
	expiresInSeconds := (tokenData.AccessTokenExpirationTimestampMs - time.Now().UnixMilli()) / 1000

	// Cache the token
	tokenCache.Store(accessTokenKey, cachedToken{
		Token:      tokenData.AccessToken,
		Expiration: time.Now().Add(time.Duration(expiresInSeconds) * time.Second).UnixNano(),
	})

	log.Debugf("%s [Legacy] Cached new access token", logcolors.LogCache)
	return tokenData.AccessToken, nil
}

// SearchTrack searches for a track on Spotify
func SearchTrack(query string) (*TrackItem, error) {
	trackURL := conf.Configuration.TrackUrl
	if trackURL == "" {
		return nil, fmt.Errorf("track URL not configured")
	}

	accessToken, err := getOAuthAccessToken()
	if err != nil {
		return nil, fmt.Errorf("error getting OAuth token: %w", err)
	}

	searchURL := trackURL + url.QueryEscape(query)

	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search request failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %w", err)
	}

	var trackResp TrackResponse
	if err := json.Unmarshal(body, &trackResp); err != nil {
		return nil, fmt.Errorf("error parsing response: %w", err)
	}

	if len(trackResp.Tracks.Items) == 0 {
		return nil, nil
	}

	return &trackResp.Tracks.Items[0], nil
}

// FetchLyrics fetches lyrics for a track
func FetchLyrics(trackID string) (*LyricsData, error) {
	lyricsURL := conf.Configuration.LyricsUrl
	if lyricsURL == "" {
		return nil, fmt.Errorf("lyrics URL not configured")
	}

	accessToken, err := getValidAccessToken()
	if err != nil {
		return nil, fmt.Errorf("error getting access token: %w", err)
	}

	requestURL := lyricsURL + trackID + "?format=json&market=from_token"

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	setCommonHeaders(req)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // No lyrics available
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lyrics request failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %w", err)
	}

	var lyricsResp LyricsResponse
	if err := json.Unmarshal(body, &lyricsResp); err != nil {
		return nil, fmt.Errorf("error parsing response: %w", err)
	}

	// Calculate durations for each line
	lines := lyricsResp.Lyrics.Lines
	for i := 0; i < len(lines); i++ {
		startTime, _ := strconv.ParseInt(lines[i].StartTimeMs, 10, 64)
		var endTime int64

		if i == len(lines)-1 {
			// Last line: use same start time as end
			endTime = startTime + 5000 // Default 5 second duration for last line
		} else {
			endTime, _ = strconv.ParseInt(lines[i+1].StartTimeMs, 10, 64)
		}

		duration := endTime - startTime
		lines[i].DurationMs = strconv.FormatInt(duration, 10)
		lines[i].EndTimeMs = strconv.FormatInt(endTime, 10)
	}

	return &lyricsResp.Lyrics, nil
}
