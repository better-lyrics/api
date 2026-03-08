package qq

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"lyrics-api-go/logcolors"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/jixunmoe-go/qrc"
	log "github.com/sirupsen/logrus"
)

const (
	apiURL         = "https://u.y.qq.com/cgi-bin/musics.fcg"
	versionCode    = 13020508
	defaultTimeout = 10 * time.Second
	userAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
)

var httpClient = &http.Client{
	Timeout: defaultTimeout,
}

// generateSign computes the signature required by QQ Music's API
func generateSign(data string) string {
	h := sha1.Sum([]byte(data))
	hashStr := strings.ToUpper(fmt.Sprintf("%x", h))

	// Extract chars at specific positions for part1 (filter out index >= 40)
	part1Indices := []int{23, 14, 6, 36, 16, 7, 19}
	var part1 strings.Builder
	for _, idx := range part1Indices {
		if idx < len(hashStr) {
			part1.WriteByte(hashStr[idx])
		}
	}

	// Extract chars at specific positions for part2
	part2Indices := []int{16, 1, 32, 12, 19, 27, 8, 5}
	var part2 strings.Builder
	for _, idx := range part2Indices {
		if idx < len(hashStr) {
			part2.WriteByte(hashStr[idx])
		}
	}

	// XOR each byte pair with scramble values
	scramble := []byte{89, 39, 179, 150, 218, 82, 58, 252, 177, 52, 186, 123, 120, 64, 242, 133, 143, 161, 121, 179}
	xorBytes := make([]byte, 20)
	for i := 0; i < 20 && i < len(scramble); i++ {
		hexVal, _ := parseHexPair(hashStr[i*2], hashStr[i*2+1])
		xorBytes[i] = scramble[i] ^ byte(hexVal)
	}

	b64 := base64.StdEncoding.EncodeToString(xorBytes)
	b64 = strings.NewReplacer("/", "", "+", "", "=", "").Replace(b64)

	return strings.ToLower(fmt.Sprintf("zzc%s%s%s", part1.String(), b64, part2.String()))
}

// parseHexPair parses two hex characters into a byte value
func parseHexPair(hi, lo byte) (int, error) {
	h, err := hexCharToInt(hi)
	if err != nil {
		return 0, err
	}
	l, err := hexCharToInt(lo)
	if err != nil {
		return 0, err
	}
	return h*16 + l, nil
}

// hexCharToInt converts a hex character to its integer value
func hexCharToInt(c byte) (int, error) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), nil
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, nil
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, nil
	default:
		return 0, fmt.Errorf("invalid hex char: %c", c)
	}
}

// generateGUID creates a random 32-char hex GUID
func generateGUID() string {
	const chars = "0123456789ABCDEF"
	b := make([]byte, 32)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

// buildCommonParams creates the comm section for API requests
func buildCommonParams() QQComm {
	return QQComm{
		WID:        generateGUID(),
		CV:         versionCode,
		V:          versionCode,
		QIMEI36:    "8888888888888888",
		CT:         "11",
		TmeAppID:   "qqmusic",
		Format:     "json",
		InCharset:  "utf-8",
		OutCharset: "utf-8",
		UID:        "3931641530",
	}
}

// buildRequestBody creates a QQ Music API request body with the dynamic module.method key
func buildRequestBody(module, method string, param interface{}) ([]byte, string, error) {
	key := module + "." + method
	// Use ordered map to ensure consistent JSON output
	raw := map[string]interface{}{
		"comm": buildCommonParams(),
		key: QQAPIModule{
			Module: module,
			Method: method,
			Param:  param,
		},
	}
	body, err := json.Marshal(raw)
	return body, key, err
}

// doAPIRequest sends a request to the QQ Music unified API and extracts the module result
func doAPIRequest(module, method string, param interface{}) (json.RawMessage, error) {
	body, key, err := buildRequestBody(module, method, param)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	sign := generateSign(string(body))
	requestURL := fmt.Sprintf("%s?sign=%s", apiURL, sign)

	req, err := http.NewRequest("POST", requestURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", "https://y.qq.com/")
	req.Header.Set("Origin", "https://y.qq.com")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse the dynamic-keyed response
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Check top-level code
	if codeRaw, ok := raw["code"]; ok {
		var code int
		if json.Unmarshal(codeRaw, &code) == nil && code != 0 {
			return nil, fmt.Errorf("API error: code %d", code)
		}
	}

	// Extract the module result using the dynamic key
	moduleRaw, ok := raw[key]
	if !ok {
		return nil, fmt.Errorf("response missing key: %s", key)
	}

	var result moduleResult
	if err := json.Unmarshal(moduleRaw, &result); err != nil {
		return nil, fmt.Errorf("failed to parse module result: %w", err)
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("API error: code %d", result.Code)
	}

	return result.Data, nil
}

// SearchSongs searches for songs on QQ Music
func SearchSongs(song, artist string, numResults int) ([]SongItem, error) {
	query := song
	if artist != "" {
		query = song + " " + artist
	}

	if numResults <= 0 {
		numResults = 10
	}

	log.Debugf("%s [QQ] Searching: %s", logcolors.LogSearch, query)

	data, err := doAPIRequest(
		"music.search.SearchCgiService",
		"DoSearchForQQMusicMobile",
		SearchParam{
			SearchID:   fmt.Sprintf("%d", rand.Int63()),
			Query:      query,
			SearchType: 0,
			NumPerPage: numResults,
			PageNum:    1,
			Highlight:  1,
			Grp:        1,
		},
	)
	if err != nil {
		return nil, err
	}

	var sd searchData
	if err := json.Unmarshal(data, &sd); err != nil {
		return nil, fmt.Errorf("failed to parse search data: %w", err)
	}

	// Try item_song first (newer API), fall back to song.list
	songs := sd.Body.ItemSong
	if len(songs) == 0 {
		songs = sd.Body.Song.List
	}

	return songs, nil
}

// FetchQRCLyrics fetches and decrypts QRC lyrics for a song
func FetchQRCLyrics(songMID string) (string, error) {
	log.Debugf("%s [QQ] Fetching lyrics for MID: %s", logcolors.LogLyrics, songMID)

	data, err := doAPIRequest(
		"music.musichallSong.PlayLyricInfo",
		"GetPlayLyricInfo",
		LyricsParam{
			Crypt:   1,
			CT:      11,
			CV:      versionCode,
			LrcT:    0,
			QRC:     1,
			QRCT:    0,
			Roma:    0,
			RomaT:   0,
			Trans:   0,
			TransT:  0,
			Type:    1,
			SongMID: songMID,
		},
	)
	if err != nil {
		return "", err
	}

	var ld lyricsData
	if err := json.Unmarshal(data, &ld); err != nil {
		return "", fmt.Errorf("failed to parse lyrics data: %w", err)
	}

	// Prefer QRC (word-level timing) over plain lyric (LRC)
	lyricContent := rawToString(ld.QRC)
	if lyricContent == "" {
		lyricContent = rawToString(ld.Lyric)
	}
	if lyricContent == "" {
		return "", fmt.Errorf("lyrics content is empty")
	}

	// Process the lyric content - could be hex-encoded encrypted QRC or plain text
	return processLyricContent(lyricContent)
}

// processLyricContent handles different lyric content formats
func processLyricContent(content string) (string, error) {
	// If it starts with '[', it's already plain LRC/QRC text
	if strings.HasPrefix(content, "[") {
		return content, nil
	}

	// Check if it's hex-encoded (encrypted QRC)
	if len(content)%2 == 0 && isHexString(content) {
		encrypted, err := hex.DecodeString(content)
		if err != nil {
			return "", fmt.Errorf("failed to hex-decode lyrics: %w", err)
		}

		decrypted, err := qrc.DecodeQRC(encrypted)
		if err != nil {
			return "", fmt.Errorf("failed to decrypt QRC: %w", err)
		}

		return string(decrypted), nil
	}

	return content, nil
}

// isHexString checks if a string contains only hex characters
func isHexString(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return len(s) > 0
}

// SelectBestSong selects the best matching song from search results
// Returns the best song and a normalized score (0.0 to 1.0)
func SelectBestSong(songs []SongItem, song, artist string, durationMs int) (*SongItem, float64) {
	if len(songs) == 0 {
		return nil, 0
	}

	var best *SongItem
	bestScore := -1
	// Max possible: 30 (exact song) + 25 (exact artist) + 20 (duration) = 75
	maxPossibleScore := 75

	songLower := strings.ToLower(song)
	artistLower := strings.ToLower(artist)

	for i := range songs {
		s := &songs[i]
		score := 0

		titleLower := strings.ToLower(s.Title)
		if titleLower == songLower {
			score += 30
		} else if strings.Contains(titleLower, songLower) || strings.Contains(songLower, titleLower) {
			score += 15
		}

		if artistLower != "" {
			singerLower := strings.ToLower(s.SingerNames())
			if singerLower == artistLower {
				score += 25
			} else if strings.Contains(singerLower, artistLower) || strings.Contains(artistLower, singerLower) {
				score += 10
			}
		}

		if durationMs > 0 && s.Interval > 0 {
			diff := abs(s.Interval*1000 - durationMs)
			if diff < 3000 {
				score += 20
			} else if diff < 5000 {
				score += 10
			} else if diff < 10000 {
				score += 5
			}
		}

		if score > bestScore {
			bestScore = score
			best = s
		}
	}

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
func filterSongsByDuration(songs []SongItem, durationMs, deltaMs int) []SongItem {
	var filtered []SongItem
	for _, s := range songs {
		songDurationMs := s.Interval * 1000
		diff := abs(songDurationMs - durationMs)
		if diff <= deltaMs {
			filtered = append(filtered, s)
		}
	}
	return filtered
}
