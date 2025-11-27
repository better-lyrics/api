package ttml

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"lyrics-api-go/circuitbreaker"
	"lyrics-api-go/config"
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
	conf := config.Get()
	apiCircuitBreaker = circuitbreaker.New(circuitbreaker.Config{
		Name:      "TTML-API",
		Threshold: conf.Configuration.CircuitBreakerThreshold,
		Cooldown:  time.Duration(conf.Configuration.CircuitBreakerCooldownSecs) * time.Second,
	})
	log.Infof("[CircuitBreaker] Initialized with threshold=%d, cooldown=%ds",
		conf.Configuration.CircuitBreakerThreshold,
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
		shorter := len(n1)
		if len(n2) < shorter {
			shorter = len(n2)
		}
		longer := len(n1)
		if len(n2) > longer {
			longer = len(n2)
		}
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
		nameWeight   = 0.50 // 50% weight for song name match
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

func makeAPIRequest(urlStr string, retries int) (*http.Response, error) {
	if accountManager == nil {
		initAccountManager()
	}
	if apiCircuitBreaker == nil {
		initCircuitBreaker()
	}

	// Check circuit breaker before making request
	if !apiCircuitBreaker.Allow() {
		timeUntilRetry := apiCircuitBreaker.TimeUntilRetry()
		log.Warnf("[CircuitBreaker] Request blocked, circuit is OPEN (retry in %v)", timeUntilRetry)
		return nil, fmt.Errorf("circuit breaker is open, API temporarily unavailable (retry in %v)", timeUntilRetry)
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
		apiCircuitBreaker.RecordFailure()
		return nil, err
	}

	// Handle rate limiting - record failure for circuit breaker
	if resp.StatusCode == 429 {
		apiCircuitBreaker.RecordFailure()

		// Still attempt retry with account switch if we have retries left
		if retries < 3 {
			resp.Body.Close()
			log.Warnf("TTML API returned 429, switching account and retrying (attempt %d/3)...", retries+1)
			accountManager.switchToNextAccount()
			time.Sleep(time.Second * time.Duration(retries+1))
			return makeAPIRequest(urlStr, retries+1)
		}

		body, _ := ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("TTML API returned status 429: %s", string(body))
	}

	// Handle auth errors (don't count as circuit breaker failure, just retry)
	if resp.StatusCode == 401 && retries < 3 {
		resp.Body.Close()
		log.Warnf("TTML API returned 401, switching account and retrying...")
		accountManager.switchToNextAccount()
		time.Sleep(time.Second * time.Duration(retries+1))
		return makeAPIRequest(urlStr, retries+1)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("TTML API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Success! Record it
	apiCircuitBreaker.RecordSuccess()
	return resp, nil
}

// =============================================================================
// API FUNCTIONS
// =============================================================================

func searchTrack(query string, storefront string, songName, artistName, albumName string, durationMs int) (*Track, float64, error) {
	if query == "" {
		return nil, 0.0, fmt.Errorf("empty search query")
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
		return nil, 0.0, fmt.Errorf("search request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, 0.0, fmt.Errorf("failed to read search response: %v", err)
	}

	if len(body) == 0 {
		return nil, 0.0, fmt.Errorf("empty search response body")
	}

	var searchResp SearchResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return nil, 0.0, fmt.Errorf("failed to parse search response: %v", err)
	}

	if len(searchResp.Results.Songs.Data) == 0 {
		return nil, 0.0, fmt.Errorf("no tracks found for query: %s", query)
	}

	tracks := searchResp.Results.Songs.Data

	// If duration is provided, apply strict duration filter first
	if durationMs > 0 {
		conf := config.Get()
		deltaMs := conf.Configuration.DurationMatchDeltaMs
		var filteredTracks []Track

		for _, track := range tracks {
			diff := track.Attributes.DurationInMillis - durationMs
			if diff < 0 {
				diff = -diff
			}
			if diff <= deltaMs {
				filteredTracks = append(filteredTracks, track)
			} else {
				log.Debugf("[Duration Filter] Rejected %s - %s (duration: %dms, diff: %dms, max delta: %dms)",
					track.Attributes.Name,
					track.Attributes.ArtistName,
					track.Attributes.DurationInMillis,
					diff,
					deltaMs)
			}
		}

		if len(filteredTracks) == 0 {
			return nil, 0.0, fmt.Errorf("no tracks found within %dms of requested duration %dms", deltaMs, durationMs)
		}

		log.Infof("[Duration Filter] %d/%d tracks passed duration filter (delta: %dms)", len(filteredTracks), len(tracks), deltaMs)
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
			log.Debugf("[Track Score] %s - %s | Total: %.3f (Name: %.3f, Artist: %.3f, Album: %.3f) | Duration: %dms",
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
				log.Warnf("[Best Match] Score %.3f below threshold %.3f for: %s - %s",
					bestScore.TotalScore,
					minScore,
					bestScore.Track.Attributes.Name,
					bestScore.Track.Attributes.ArtistName)
				return nil, 0.0, fmt.Errorf("no matching tracks found (best match score %.3f below threshold %.3f)", bestScore.TotalScore, minScore)
			}

			log.Infof("[Best Match] %s - %s (Score: %.3f)",
				bestScore.Track.Attributes.Name,
				bestScore.Track.Attributes.ArtistName,
				bestScore.TotalScore)
			return bestScore.Track, bestScore.TotalScore, nil
		}
	}

	// Fallback: return the first (best) match from API (no score calculated)
	log.Debugf("[Fallback] Using first search result")
	return &tracks[0], 1.0, nil
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
