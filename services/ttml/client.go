package ttml

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"lyrics-api-go/config"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

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

// durationScore calculates score based on duration difference (0.0 to 1.0)
// Closer durations get higher scores
func durationScore(trackDurationMs, targetDurationMs int) float64 {
	if targetDurationMs <= 0 {
		return 0.0 // No score if no target duration provided
	}

	diff := float64(trackDurationMs - targetDurationMs)
	if diff < 0 {
		diff = -diff
	}

	// Use exponential decay: score decreases as difference increases
	// A 5-second difference gives ~0.8 score, 10 seconds gives ~0.6, 30 seconds gives ~0.2
	score := math.Exp(-diff / 20000.0) // 20000ms = 20 seconds half-life
	return score
}

// TrackScore represents the scoring breakdown for a track
type TrackScore struct {
	Track         *Track
	TotalScore    float64
	DurationScore float64
	NameScore     float64
	ArtistScore   float64
	AlbumScore    float64
}

// scoreTrack calculates a weighted score for a track based on multiple factors
func scoreTrack(track *Track, targetSongName, targetArtistName, targetAlbumName string, targetDurationMs int) TrackScore {
	// Weights for each factor (must sum to 1.0)
	const (
		durationWeight = 0.20 // 20% weight for duration match
		nameWeight     = 0.40 // 40% weight for song name match
		artistWeight   = 0.30 // 30% weight for artist name match
		albumWeight    = 0.10 // 10% weight for album name match
	)

	score := TrackScore{Track: track}

	// Calculate individual scores
	score.DurationScore = durationScore(track.Attributes.DurationInMillis, targetDurationMs)
	score.NameScore = stringSimilarity(track.Attributes.Name, targetSongName)
	score.ArtistScore = stringSimilarity(track.Attributes.ArtistName, targetArtistName)
	score.AlbumScore = stringSimilarity(track.Attributes.AlbumName, targetAlbumName)

	// Calculate weighted total score
	// Only include duration in score if target duration was provided
	if targetDurationMs > 0 {
		score.TotalScore = (score.DurationScore * durationWeight) +
			(score.NameScore * nameWeight) +
			(score.ArtistScore * artistWeight) +
			(score.AlbumScore * albumWeight)
	} else {
		// Redistribute duration weight to other factors when no duration provided
		adjustedNameWeight := nameWeight + (durationWeight * 0.5)
		adjustedArtistWeight := artistWeight + (durationWeight * 0.35)
		adjustedAlbumWeight := albumWeight + (durationWeight * 0.15)

		score.TotalScore = (score.NameScore * adjustedNameWeight) +
			(score.ArtistScore * adjustedArtistWeight) +
			(score.AlbumScore * adjustedAlbumWeight)
	}

	return score
}

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

	// If we have any matching criteria (duration, name, artist, album), use scoring system
	if durationMs > 0 || songName != "" || artistName != "" || albumName != "" {
		var bestScore TrackScore
		bestScore.TotalScore = -1

		for i := range tracks {
			track := &tracks[i]
			score := scoreTrack(track, songName, artistName, albumName, durationMs)

			// Log detailed scoring for debugging
			log.Debugf("[Track Score] %s - %s | Total: %.3f (Name: %.3f, Artist: %.3f, Album: %.3f, Duration: %.3f) | Duration: %dms",
				track.Attributes.Name,
				track.Attributes.ArtistName,
				score.TotalScore,
				score.NameScore,
				score.ArtistScore,
				score.AlbumScore,
				score.DurationScore,
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
