package ttml

import (
	"encoding/json"
	"fmt"
	"io"
	"lyrics-api-go/circuitbreaker"
	"lyrics-api-go/config"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/notifier"
	"lyrics-api-go/stats"
	"net/http"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

var apiCircuitBreaker *circuitbreaker.CircuitBreaker

func initCircuitBreaker() {
	if apiCircuitBreaker != nil {
		return
	}

	// Ensure account manager is initialized to get account count
	if accountManager == nil {
		initAccountManager()
	}

	conf := config.Get()
	baseThreshold := conf.Configuration.CircuitBreakerThreshold

	// Scale threshold by number of accounts for fair distribution
	// With round-robin, each account may fail independently, so we need
	// a higher threshold to avoid premature circuit opening
	numAccounts := max(accountManager.accountCount(), 1)
	scaledThreshold := baseThreshold * numAccounts

	apiCircuitBreaker = circuitbreaker.New(circuitbreaker.Config{
		Name:      "TTML-API",
		Threshold: scaledThreshold,
		Cooldown:  time.Duration(conf.Configuration.CircuitBreakerCooldownSecs) * time.Second,
	})
	log.Infof("%s Initialized with threshold=%d (base=%d × %d accounts), cooldown=%ds", logcolors.LogCircuitBreaker,
		scaledThreshold,
		baseThreshold,
		numAccounts,
		conf.Configuration.CircuitBreakerCooldownSecs)
}

// GetCircuitBreakerStats returns circuit breaker statistics for monitoring
func GetCircuitBreakerStats() (state string, failures int, timeUntilRetry time.Duration) {
	if apiCircuitBreaker == nil {
		return "UNINITIALIZED", 0, 0
	}
	s, f, _ := apiCircuitBreaker.Stats()
	return s.String(), f, apiCircuitBreaker.TimeUntilRetry()
}

// ResetCircuitBreaker manually resets the circuit breaker (for admin use)
func ResetCircuitBreaker() {
	if apiCircuitBreaker != nil {
		apiCircuitBreaker.Reset()
	}
}

// TripCircuitBreakerOnFullQuarantine opens the circuit breaker when all accounts are quarantined.
// This is called by the account manager when all accounts become unavailable.
func TripCircuitBreakerOnFullQuarantine() {
	if apiCircuitBreaker == nil {
		initCircuitBreaker()
	}
	if apiCircuitBreaker == nil {
		return
	}

	// Record enough failures to trip the circuit if not already open
	threshold := apiCircuitBreaker.Threshold()
	currentFailures := apiCircuitBreaker.Failures()

	if currentFailures < threshold {
		log.Warnf("%s All accounts quarantined, tripping circuit breaker (adding %d failures)",
			logcolors.LogCircuitBreaker, threshold-currentFailures)
		for i := currentFailures; i < threshold; i++ {
			apiCircuitBreaker.RecordFailure()
		}
	}
}

// SimulateFailure simulates an API failure for testing the circuit breaker
func SimulateFailure() {
	if apiCircuitBreaker == nil {
		initCircuitBreaker()
	}
	apiCircuitBreaker.RecordFailure()
}

// =============================================================================
// STRING SIMILARITY & SCORING
// =============================================================================

// normalizeString normalizes a string for comparison
func normalizeString(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// stringSimilarity calculates similarity between two strings (0.0 to 1.0)
// Uses a combination of exact match, contains, and character overlap
func stringSimilarity(s1, s2 string) float64 {
	if s1 == "" || s2 == "" {
		return 0.0
	}

	n1 := normalizeString(s1)
	n2 := normalizeString(s2)

	// Exact match
	if n1 == n2 {
		return 1.0
	}

	// One contains the other
	if strings.Contains(n1, n2) || strings.Contains(n2, n1) {
		shorter := min(len(n1), len(n2))
		longer := max(len(n1), len(n2))
		return 0.7 + (0.3 * float64(shorter) / float64(longer))
	}

	// Calculate character overlap ratio
	chars1 := make(map[rune]int)
	chars2 := make(map[rune]int)

	for _, c := range n1 {
		chars1[c]++
	}
	for _, c := range n2 {
		chars2[c]++
	}

	overlap := 0
	for c, count1 := range chars1 {
		if count2, exists := chars2[c]; exists {
			if count1 < count2 {
				overlap += count1
			} else {
				overlap += count2
			}
		}
	}

	totalChars := len(n1) + len(n2)
	if totalChars == 0 {
		return 0.0
	}

	return float64(overlap*2) / float64(totalChars)
}

// TrackScore represents the scoring breakdown for a track
type TrackScore struct {
	Track       *Track
	TotalScore  float64
	NameScore   float64
	ArtistScore float64
	AlbumScore  float64
}

// scoreTrack calculates a weighted score for a track based on multiple factors
// Duration filtering is handled separately as a strict requirement, not a weight
func scoreTrack(track *Track, targetSongName, targetArtistName, targetAlbumName string) TrackScore {
	// Weights for each factor (must sum to 1.0)
	const (
		nameWeight   = 0.50  // 50% weight for song name match
		artistWeight = 0.375 // 37.5% weight for artist name match
		albumWeight  = 0.125 // 12.5% weight for album name match
	)

	score := TrackScore{Track: track}

	// Calculate individual scores
	score.NameScore = stringSimilarity(track.Attributes.Name, targetSongName)
	score.ArtistScore = stringSimilarity(track.Attributes.ArtistName, targetArtistName)
	score.AlbumScore = stringSimilarity(track.Attributes.AlbumName, targetAlbumName)

	// Calculate weighted total score
	score.TotalScore = (score.NameScore * nameWeight) +
		(score.ArtistScore * artistWeight) +
		(score.AlbumScore * albumWeight)

	return score
}

// =============================================================================
// HTTP REQUEST HANDLING
// =============================================================================

// makeAPIRequestWithAccount makes an HTTP request using the specified account.
// Returns the response, the account that succeeded (may differ from input if retried), and error.
func makeAPIRequestWithAccount(urlStr string, account MusicAccount, retries int) (*http.Response, MusicAccount, error) {
	if apiCircuitBreaker == nil {
		initCircuitBreaker()
	}

	// Check circuit breaker before making request
	if !apiCircuitBreaker.Allow() {
		timeUntilRetry := apiCircuitBreaker.TimeUntilRetry()
		if apiCircuitBreaker.IsHalfOpen() {
			log.Warnf("%s Request blocked, circuit is HALF-OPEN, waiting for test request (retry in %v)", logcolors.LogCircuitBreaker, timeUntilRetry)
			return nil, account, fmt.Errorf("circuit breaker is half-open, waiting for test request (retry in %v)", timeUntilRetry)
		}
		log.Warnf("%s Request blocked, circuit is OPEN (retry in %v)", logcolors.LogCircuitBreaker, timeUntilRetry)
		return nil, account, fmt.Errorf("circuit breaker is open, API temporarily unavailable (retry in %v)", timeUntilRetry)
	}

	attemptNum := retries + 1
	log.Infof("%s Making request via %s (attempt %d)...", logcolors.LogHTTP, logcolors.Account(account.NameID), attemptNum)

	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		log.Errorf("%s Failed to create request: %v", logcolors.LogHTTP, err)
		return nil, account, err
	}

	// Get shared bearer token (auto-scraped)
	bearerToken, err := GetBearerToken()
	if err != nil {
		log.Errorf("%s Failed to get bearer token: %v", logcolors.LogHTTP, err)
		return nil, account, fmt.Errorf("failed to get bearer token: %w", err)
	}

	// Set headers for web auth
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Origin", "https://music.apple.com")
	req.Header.Set("Referer", "https://music.apple.com")
	if account.MediaUserToken != "" {
		req.Header.Set("media-user-token", account.MediaUserToken)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		apiCircuitBreaker.RecordFailure()
		log.Errorf("%s Request failed via %s: %v", logcolors.LogHTTP, logcolors.Account(account.NameID), err)
		return nil, account, err
	}

	log.Infof("%s Response from %s: status %d", logcolors.LogHTTP, logcolors.Account(account.NameID), resp.StatusCode)

	// Calculate max retries based on account count (capped at 3)
	maxRetries := min(accountManager.accountCount(), 3)

	// Handle rate limiting - quarantine account and retry with different one
	if resp.StatusCode == 429 {
		// Log rate limit headers for debugging
		retryAfter := resp.Header.Get("Retry-After")
		xRateLimit := resp.Header.Get("X-Rate-Limit")
		rateLimitReset := resp.Header.Get("X-RateLimit-Reset")
		rateLimitRemaining := resp.Header.Get("X-RateLimit-Remaining")

		if retryAfter != "" || xRateLimit != "" || rateLimitReset != "" || rateLimitRemaining != "" {
			log.Warnf("%s Rate limit headers from %s: Retry-After=%q, X-Rate-Limit=%q, X-RateLimit-Reset=%q, X-RateLimit-Remaining=%q",
				logcolors.LogRateLimit, logcolors.Account(account.NameID),
				retryAfter, xRateLimit, rateLimitReset, rateLimitRemaining)
		} else {
			log.Warnf("%s No rate limit headers in 429 response from %s", logcolors.LogRateLimit, logcolors.Account(account.NameID))
		}

		accountManager.quarantineAccount(account)

		// Only count toward circuit breaker if no healthy accounts remain
		availableAccounts := accountManager.availableAccountCount()
		if availableAccounts == 0 {
			apiCircuitBreaker.RecordFailure()
			log.Warnf("%s All accounts quarantined, recording circuit breaker failure", logcolors.LogRateLimit)
		}

		if retries < maxRetries {
			resp.Body.Close()
			nextAccount := accountManager.getNextAccount()
			sleepDuration := time.Duration(retries+1) * time.Second
			log.Warnf("%s 429 on %s (quarantined), switching to %s (attempt %d/%d, sleeping %v, %d accounts available)...",
				logcolors.LogRateLimit, logcolors.Account(account.NameID), logcolors.Account(nextAccount.NameID), attemptNum, maxRetries, sleepDuration, availableAccounts)
			time.Sleep(sleepDuration)
			return makeAPIRequestWithAccount(urlStr, nextAccount, retries+1)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		log.Errorf("%s All %d retries exhausted, last account: %s", logcolors.LogRateLimit, maxRetries, logcolors.Account(account.NameID))
		return nil, account, fmt.Errorf("TTML API returned status 429: %s", string(body))
	}

	// Handle auth errors - since bearer is auto-refreshed, 401 indicates MUT issue
	// Don't count as circuit breaker failure, just retry with different account
	if resp.StatusCode == 401 {
		// Emit auth failure event (only on first occurrence per account to avoid spam during retries)
		// Since bearer is always fresh, this indicates the MUT is invalid/expired
		if retries == 0 {
			notifier.PublishAccountAuthFailure(account.NameID, resp.StatusCode)
		}

		if retries < maxRetries {
			resp.Body.Close()
			nextAccount := accountManager.getNextAccount()
			sleepDuration := time.Duration(retries+1) * time.Second
			log.Warnf("%s 401 on %s (MUT invalid), switching to %s (attempt %d/%d, sleeping %v)...",
				logcolors.LogAuthError, logcolors.Account(account.NameID), logcolors.Account(nextAccount.NameID), attemptNum, maxRetries, sleepDuration)
			time.Sleep(sleepDuration)
			return makeAPIRequestWithAccount(urlStr, nextAccount, retries+1)
		}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		apiCircuitBreaker.RecordFailure()
		log.Errorf("%s Unexpected status %d from %s: %s", logcolors.LogHTTP, resp.StatusCode, logcolors.Account(account.NameID), string(body))
		return nil, account, fmt.Errorf("TTML API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Success! Record it and clear any quarantine
	apiCircuitBreaker.RecordSuccess()
	accountManager.clearQuarantine(account)
	stats.Get().RecordAccountUsage(account.NameID)
	log.Infof("%s Request successful via %s", logcolors.LogHTTP, logcolors.Account(account.NameID))
	return resp, account, nil
}

// =============================================================================
// API FUNCTIONS
// =============================================================================

// searchTrack searches for a track and returns the best match, score, the account that succeeded, and any error.
// The returned account may differ from the input if a retry occurred due to rate limiting.
func searchTrack(query string, storefront string, songName, artistName, albumName string, durationMs int, account MusicAccount) (*Track, float64, MusicAccount, error) {
	if query == "" {
		return nil, 0.0, account, fmt.Errorf("empty search query")
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

	log.Infof("%s Querying TTML API via %s: %s", logcolors.LogSearch, logcolors.Account(account.NameID), query)
	resp, successAccount, err := makeAPIRequestWithAccount(searchURL, account, 0)
	if err != nil {
		return nil, 0.0, successAccount, fmt.Errorf("search request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0.0, successAccount, fmt.Errorf("failed to read search response: %v", err)
	}

	if len(body) == 0 {
		return nil, 0.0, successAccount, fmt.Errorf("empty search response body")
	}

	var searchResp SearchResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return nil, 0.0, successAccount, fmt.Errorf("failed to parse search response: %v", err)
	}

	if len(searchResp.Results.Songs.Data) == 0 {
		return nil, 0.0, successAccount, fmt.Errorf("no tracks found for query: %s", query)
	}

	tracks := searchResp.Results.Songs.Data

	// If duration is provided, apply strict duration filter first
	if durationMs > 0 {
		conf := config.Get()
		deltaMs := conf.Configuration.DurationMatchDeltaMs
		var filteredTracks []Track

		// Track closest match for error reporting
		var closestTrack *Track
		closestDiff := int(^uint(0) >> 1) // Max int

		for i, track := range tracks {
			diff := track.Attributes.DurationInMillis - durationMs
			if diff < 0 {
				diff = -diff
			}

			// Track closest match
			if diff < closestDiff {
				closestDiff = diff
				closestTrack = &tracks[i]
			}

			if diff <= deltaMs {
				filteredTracks = append(filteredTracks, track)
			} else {
				log.Debugf("%s Rejected %s - %s (duration: %dms, diff: %dms, max delta: %dms)",
					logcolors.LogDurationFilter,
					track.Attributes.Name,
					track.Attributes.ArtistName,
					track.Attributes.DurationInMillis,
					diff,
					deltaMs)
			}
		}

		if len(filteredTracks) == 0 {
			if closestTrack != nil {
				return nil, 0.0, successAccount, fmt.Errorf("no tracks within %dms of duration %dms (closest: %s - %s at %dms, diff: %dms)",
					deltaMs, durationMs,
					closestTrack.Attributes.Name,
					closestTrack.Attributes.ArtistName,
					closestTrack.Attributes.DurationInMillis,
					closestDiff)
			}
			return nil, 0.0, successAccount, fmt.Errorf("no tracks found within %dms of requested duration %dms", deltaMs, durationMs)
		}

		log.Infof("%s %d/%d tracks passed duration filter (delta: %dms)", logcolors.LogDurationFilter, len(filteredTracks), len(tracks), deltaMs)
		tracks = filteredTracks
	}

	// If we have any matching criteria (name, artist, album), use scoring system
	if songName != "" || artistName != "" || albumName != "" {
		var bestScore TrackScore
		bestScore.TotalScore = -1

		for i := range tracks {
			track := &tracks[i]
			score := scoreTrack(track, songName, artistName, albumName)

			// Log detailed scoring for debugging
			log.Debugf("%s %s - %s | Total: %.3f (Name: %.3f, Artist: %.3f, Album: %.3f) | Duration: %dms",
				logcolors.LogTrackScore,
				track.Attributes.Name,
				track.Attributes.ArtistName,
				score.TotalScore,
				score.NameScore,
				score.ArtistScore,
				score.AlbumScore,
				track.Attributes.DurationInMillis)

			if score.TotalScore > bestScore.TotalScore {
				bestScore = score
			}
		}

		if bestScore.Track != nil {
			conf := config.Get()
			minScore := conf.Configuration.MinSimilarityScore

			// Check if the best score meets the minimum threshold
			if bestScore.TotalScore < minScore {
				log.Warnf("%s Score %.3f below threshold %.3f for: %s - %s",
					logcolors.LogBestMatch,
					bestScore.TotalScore,
					minScore,
					bestScore.Track.Attributes.Name,
					bestScore.Track.Attributes.ArtistName)
				return nil, 0.0, successAccount, fmt.Errorf("no matching tracks found (best match score %.3f below threshold %.3f)", bestScore.TotalScore, minScore)
			}

			log.Infof("%s %s - %s (Score: %.3f)",
				logcolors.LogBestMatch,
				bestScore.Track.Attributes.Name,
				bestScore.Track.Attributes.ArtistName,
				bestScore.TotalScore)
			return bestScore.Track, bestScore.TotalScore, successAccount, nil
		}
	}

	// Fallback: return the first (best) match from API (no score calculated)
	log.Debugf("%s Using first search result", logcolors.LogFallback)
	return &tracks[0], 1.0, successAccount, nil
}

func fetchLyricsTTML(trackID string, storefront string, account MusicAccount) (string, error) {
	conf := config.Get()
	lyricsURL := conf.Configuration.TTMLBaseURL + fmt.Sprintf(
		conf.Configuration.TTMLLyricsPath,
		storefront,
		trackID,
	)

	log.Infof("%s Fetching TTML via %s for track: %s", logcolors.LogLyrics, logcolors.Account(account.NameID), trackID)
	resp, _, err := makeAPIRequestWithAccount(lyricsURL, account, 0)
	if err != nil {
		return "", fmt.Errorf("lyrics request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read lyrics response: %v", err)
	}

	var lyricsResp LyricsResponse
	if err := json.Unmarshal(body, &lyricsResp); err != nil {
		return "", fmt.Errorf("failed to parse lyrics response: %v", err)
	}

	log.Debugf("%s Parsed lyrics response, data entries: %d", logcolors.LogLyrics, len(lyricsResp.Data))

	if len(lyricsResp.Data) == 0 {
		return "", fmt.Errorf("no lyrics data found")
	}

	ttml := lyricsResp.Data[0].Attributes.TTML
	log.Debugf("%s TTML field length: %d", logcolors.LogLyrics, len(ttml))

	if ttml == "" {
		ttml = lyricsResp.Data[0].Attributes.TTMLLocalizations
		log.Debugf("%s Using TTMLLocalizations instead, length: %d", logcolors.LogLyrics, len(ttml))
	}

	if ttml == "" {
		return "", fmt.Errorf("TTML content is empty")
	}

	log.Debugf("%s Successfully fetched TTML content, length: %d bytes", logcolors.LogLyrics, len(ttml))
	return ttml, nil
}
