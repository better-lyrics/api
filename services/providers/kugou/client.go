package kugou

import (
	"encoding/json"
	"fmt"
	"io"
	"lyrics-api-go/logcolors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// API endpoints
	lyricsSearchURL   = "https://krcs.kugou.com/search"
	lyricsDownloadURL = "https://krcs.kugou.com/download"
	songSearchURL     = "http://msearchcdn.kugou.com/api/v3/search/song"

	// Request defaults
	defaultTimeout = 10 * time.Second
	userAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
)

var httpClient = &http.Client{
	Timeout: defaultTimeout,
}

// SearchLyrics searches for lyrics candidates from Kugou
// The hash parameter is required for the API to return candidates
func SearchLyrics(song, artist string, durationMs int, hash string) ([]LyricsCandidate, error) {
	keyword := song
	if artist != "" {
		keyword = song + " " + artist
	}

	params := url.Values{}
	params.Set("ver", "1")
	params.Set("man", "yes")
	params.Set("client", "mobi")
	params.Set("keyword", keyword)
	if durationMs > 0 {
		params.Set("duration", strconv.Itoa(durationMs))
	}
	if hash != "" {
		params.Set("hash", hash)
	}

	requestURL := lyricsSearchURL + "?" + params.Encode()

	log.Debugf("%s Searching lyrics: %s", logcolors.LogSearch, keyword)

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var searchResp SearchResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if searchResp.Status != 200 {
		return nil, fmt.Errorf("API error: %s (code: %d)", searchResp.ErrMsg, searchResp.ErrCode)
	}

	return searchResp.Candidates, nil
}

// DownloadLyrics downloads lyrics content by ID and access key
func DownloadLyrics(id, accessKey string) (string, error) {
	params := url.Values{}
	params.Set("ver", "1")
	params.Set("client", "pc")
	params.Set("id", id)
	params.Set("accesskey", accessKey)
	params.Set("fmt", "lrc")

	requestURL := lyricsDownloadURL + "?" + params.Encode()

	log.Debugf("%s Downloading lyrics ID: %s", logcolors.LogLyrics, id)

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var downloadResp DownloadResponse
	if err := json.Unmarshal(body, &downloadResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if downloadResp.Status != 200 {
		return "", fmt.Errorf("API error: %s (code: %d)", downloadResp.Info, downloadResp.ErrorCode)
	}

	if downloadResp.Content == "" {
		return "", fmt.Errorf("lyrics content is empty")
	}

	// Decode base64 content
	lrcContent, err := DecodeBase64Content(downloadResp.Content)
	if err != nil {
		return "", fmt.Errorf("failed to decode lyrics content: %w", err)
	}

	return lrcContent, nil
}

// SearchSongs searches for songs on Kugou (alternative method to get hash for lyrics)
func SearchSongs(song, artist string, pageSize int) ([]SongInfo, error) {
	keyword := song
	if artist != "" {
		keyword = song + " " + artist
	}

	if pageSize <= 0 {
		pageSize = 10
	}

	params := url.Values{}
	params.Set("keyword", keyword)
	params.Set("pagesize", strconv.Itoa(pageSize))
	params.Set("page", "1")
	params.Set("plat", "0")
	params.Set("version", "9108")

	requestURL := songSearchURL + "?" + params.Encode()

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var searchResp SongSearchResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if searchResp.Status != 1 {
		return nil, fmt.Errorf("API error: status %d, errcode %d", searchResp.Status, searchResp.ErrCode)
	}

	return searchResp.Data.Info, nil
}

// SelectBestCandidate selects the best lyrics candidate based on scoring
// Returns the best candidate and the calculated match score (0.0 to 1.0)
func SelectBestCandidate(candidates []LyricsCandidate, song, artist string, durationMs int) (*LyricsCandidate, float64) {
	if len(candidates) == 0 {
		return nil, 0
	}

	var best *LyricsCandidate
	bestScore := -1
	// Max possible: 60 (API base) + 20 (synced) + 20 (exact song) + 20 (exact artist) + 20 (duration) + 5 (official) = 145
	maxPossibleScore := 145

	songLower := strings.ToLower(song)
	artistLower := strings.ToLower(artist)

	for i := range candidates {
		c := &candidates[i]
		score := c.Score

		// Prefer synced lyrics (krctype == 1)
		if c.KRCType == 1 {
			score += 20
		}

		// Bonus for matching song name
		candidateSongLower := strings.ToLower(c.Song)
		if candidateSongLower == songLower {
			score += 20 // Exact match
		} else if strings.Contains(candidateSongLower, songLower) ||
			strings.Contains(songLower, candidateSongLower) {
			score += 10
		}

		// Bonus for matching artist
		if artistLower != "" {
			candidateSingerLower := strings.ToLower(c.Singer)
			if candidateSingerLower == artistLower {
				score += 20 // Exact match
			} else if strings.Contains(candidateSingerLower, artistLower) {
				score += 10
			}
		}

		// Bonus for duration match (within 5 seconds)
		if durationMs > 0 && c.Duration > 0 {
			diff := abs(c.Duration - durationMs)
			if diff < 3000 {
				score += 20
			} else if diff < 5000 {
				score += 10
			} else if diff < 10000 {
				score += 5
			}
		}

		// Prefer official lyrics
		if strings.Contains(c.ProductFrom, "官方") {
			score += 5
		}

		if score > bestScore {
			bestScore = score
			best = c
		}
	}

	// Normalize score to 0.0-1.0 range
	normalizedScore := float64(bestScore) / float64(maxPossibleScore)
	if normalizedScore > 1.0 {
		normalizedScore = 1.0
	}
	if normalizedScore < 0.0 {
		normalizedScore = 0.0
	}

	return best, normalizedScore
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// filterSongsByDuration filters songs to those within deltaMs of the target duration
func filterSongsByDuration(songs []SongInfo, durationMs, deltaMs int) []SongInfo {
	var filtered []SongInfo
	for _, s := range songs {
		// SongInfo.Duration is in seconds, convert to ms for comparison
		songDurationMs := s.Duration * 1000
		diff := abs(songDurationMs - durationMs)
		if diff <= deltaMs {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// SelectBestSong selects the best song from search results based on matching criteria
// Returns the best song and a normalized score (0.0 to 1.0)
func SelectBestSong(songs []SongInfo, song, artist string, durationMs int) (*SongInfo, float64) {
	if len(songs) == 0 {
		return nil, 0
	}

	var best *SongInfo
	bestScore := -1
	// Max possible: 30 (exact song) + 25 (exact artist) + 20 (duration) + 3 (quality) = 78
	maxPossibleScore := 78

	songLower := strings.ToLower(song)
	artistLower := strings.ToLower(artist)

	for i := range songs {
		s := &songs[i]
		score := 0

		// Bonus for matching song name
		songNameLower := strings.ToLower(s.SongName)
		if songNameLower == songLower {
			score += 30 // Exact match
		} else if strings.Contains(songNameLower, songLower) || strings.Contains(songLower, songNameLower) {
			score += 15
		}

		// Bonus for matching artist
		if artistLower != "" {
			singerLower := strings.ToLower(s.SingerName)
			if singerLower == artistLower {
				score += 25 // Exact match
			} else if strings.Contains(singerLower, artistLower) || strings.Contains(artistLower, singerLower) {
				score += 10
			}
		}

		// Bonus for duration match (within 3 seconds)
		// Note: SongInfo.Duration is in seconds, durationMs is in milliseconds
		if durationMs > 0 && s.Duration > 0 {
			diff := abs(s.Duration*1000 - durationMs)
			if diff < 3000 {
				score += 20
			} else if diff < 5000 {
				score += 10
			} else if diff < 10000 {
				score += 5
			}
		}

		// Prefer songs with higher quality available
		if s.SQHash != "" {
			score += 2
		}
		if s.Hash320 != "" {
			score += 1
		}

		if score > bestScore {
			bestScore = score
			best = s
		}
	}

	// Normalize score to 0.0-1.0 range
	normalizedScore := float64(bestScore) / float64(maxPossibleScore)
	if normalizedScore > 1.0 {
		normalizedScore = 1.0
	}
	if normalizedScore < 0.0 {
		normalizedScore = 0.0
	}

	return best, normalizedScore
}
