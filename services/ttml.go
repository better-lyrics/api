package services

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"lyrics-api-go/config"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

var conf = config.Get()

// =============================================================================
// DATA STRUCTURES
// =============================================================================

// Syllable represents a single word/syllable with timing information
type Syllable struct {
	Text      string `json:"text"`
	StartTime string `json:"startTimeMs"`
	Duration  string `json:"durationMs"`
	EndTime   string `json:"endTimeMs"`
}

// Line represents a lyrics line with timing information
type Line struct {
	StartTimeMs string     `json:"startTimeMs"`
	DurationMs  string     `json:"durationMs"`
	Words       string     `json:"words"`
	Syllables   []Syllable `json:"syllables"`
	EndTimeMs   string     `json:"endTimeMs"`
}

// =============================================================================
// ACCOUNT MANAGEMENT
// =============================================================================

type MusicAccount struct {
	NameID           string
	AuthType         string
	AndroidAuthToken string
	AndroidDSID      string
	AndroidUserAgent string
	AndroidCookie    string
	Storefront       string
	MusicAuthToken   string
}

type AccountManager struct {
	accounts     []MusicAccount
	currentIndex int
}

var accountManager *AccountManager

func initAccountManager() {
	accounts := []MusicAccount{
		{
			NameID:           "Primary",
			AuthType:         conf.Configuration.TTMLAuthType,
			AndroidAuthToken: conf.Configuration.TTMLAndroidToken,
			AndroidDSID:      conf.Configuration.TTMLAndroidDSID,
			AndroidUserAgent: conf.Configuration.TTMLAndroidUserAgent,
			AndroidCookie:    conf.Configuration.TTMLAndroidCookie,
			Storefront:       conf.Configuration.TTMLStorefront,
			MusicAuthToken:   conf.Configuration.TTMLWebToken,
		},
	}

	accountManager = &AccountManager{
		accounts:     accounts,
		currentIndex: 0,
	}
}

func (m *AccountManager) getCurrentAccount() MusicAccount {
	return m.accounts[m.currentIndex]
}

func (m *AccountManager) switchToNextAccount() {
	m.currentIndex = (m.currentIndex + 1) % len(m.accounts)
	log.Warnf("Switched to TTML API account: %s", m.accounts[m.currentIndex].NameID)
}

// =============================================================================
// API RESPONSE STRUCTURES
// =============================================================================

type SearchResponse struct {
	Results struct {
		Songs struct {
			Data []Track `json:"data"`
		} `json:"songs"`
	} `json:"results"`
}

type Track struct {
	ID         string `json:"id"`
	Attributes struct {
		Name              string `json:"name"`
		ArtistName        string `json:"artistName"`
		AlbumName         string `json:"albumName"`
		DurationInMillis  int    `json:"durationInMillis"`
		URL               string `json:"url"`
		ISRC              string `json:"isrc"`
		SongwriterNames   string `json:"songwriterName"`
	} `json:"attributes"`
}

type LyricsResponse struct {
	Data []struct {
		ID         string `json:"id"`
		Attributes struct {
			TTML               string `json:"ttml"`
			TTMLLocalizations  string `json:"ttmlLocalizations"`
		} `json:"attributes"`
	} `json:"data"`
}

// =============================================================================
// TTML XML STRUCTURES
// =============================================================================

type TTML struct {
	XMLName xml.Name `xml:"tt"`
	Body    TTMLBody `xml:"body"`
}

type TTMLBody struct {
	Divs []TTMLDiv `xml:"div"`
}

type TTMLDiv struct {
	SongPart   string          `xml:"songPart,attr"`
	Paragraphs []TTMLParagraph `xml:"p"`
}

type TTMLParagraph struct {
	Begin string     `xml:"begin,attr"`
	End   string     `xml:"end,attr"`
	Key   string     `xml:"key,attr"`
	Agent string     `xml:"agent,attr"`
	Spans []TTMLSpan `xml:"span"`
	Text  string     `xml:",innerxml"`
}

type TTMLSpan struct {
	Begin string `xml:"begin,attr"`
	End   string `xml:"end,attr"`
	Text  string `xml:",chardata"`
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

	// Set headers based on auth type
	if account.AuthType == "android" {
		req.Header.Set("Authorization", "Bearer "+account.AndroidAuthToken)
		req.Header.Set("x-dsid", account.AndroidDSID)
		req.Header.Set("User-Agent", account.AndroidUserAgent)
		req.Header.Set("Cookie", account.AndroidCookie)
		// Don't set Accept-Encoding manually - let Go's http.Client handle compression automatically
	} else {
		// Web auth type
		req.Header.Set("Authorization", "Bearer "+account.AndroidAuthToken)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		req.Header.Set("Origin", "https://music.apple.com")
		req.Header.Set("Referer", "https://music.apple.com")
		if account.MusicAuthToken != "" {
			req.Header.Set("media-user-token", account.MusicAuthToken)
		}
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

// =============================================================================
// TTML PARSING FUNCTIONS
// =============================================================================

// parseTTMLTime parses TTML timestamp to milliseconds
func parseTTMLTime(timeStr string) (int64, error) {
	// Format: "0:00:12.34" or "12.34" or "12"
	parts := strings.Split(timeStr, ":")

	var hours, minutes, seconds float64
	var err error

	switch len(parts) {
	case 1:
		// Just seconds
		seconds, err = strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return 0, err
		}
	case 2:
		// Minutes:seconds
		minutes, _ = strconv.ParseFloat(parts[0], 64)
		seconds, err = strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return 0, err
		}
	case 3:
		// Hours:minutes:seconds
		hours, _ = strconv.ParseFloat(parts[0], 64)
		minutes, _ = strconv.ParseFloat(parts[1], 64)
		seconds, err = strconv.ParseFloat(parts[2], 64)
		if err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("invalid time format: %s", timeStr)
	}

	totalSeconds := hours*3600 + minutes*60 + seconds
	return int64(totalSeconds * 1000), nil
}

// Parse TTML directly to Lines (handles word-level TTML from Apple Music)
func parseTTMLToLines(ttmlContent string) ([]Line, error) {
	log.Debugf("[TTML Parser] Starting to parse TTML content (length: %d bytes)", len(ttmlContent))

	var ttml TTML
	if err := xml.Unmarshal([]byte(ttmlContent), &ttml); err != nil {
		log.Errorf("[TTML Parser] Failed to unmarshal XML: %v", err)
		return nil, fmt.Errorf("failed to parse TTML XML: %v", err)
	}

	log.Debugf("[TTML Parser] Successfully parsed XML structure")
	log.Debugf("[TTML Parser] Number of div sections found: %d", len(ttml.Body.Divs))

	var lines []Line

	// Apple Music returns word-level TTML with <span> elements inside <p> paragraphs
	// Iterate through all div sections (Verse, Chorus, etc.)
	for divIdx, div := range ttml.Body.Divs {
		log.Debugf("[TTML Parser] Processing div %d (songPart: %s) with %d paragraphs", divIdx, div.SongPart, len(div.Paragraphs))

		for i, para := range div.Paragraphs {
			log.Debugf("[TTML Parser]   Processing paragraph %d: begin=%s, end=%s, spans=%d", i, para.Begin, para.End, len(para.Spans))

		if len(para.Spans) == 0 {
			log.Warnf("[TTML Parser] Skipping paragraph %d - no spans found", i)
			continue
		}

		var syllables []Syllable
		var fullText string
		var earliestTime int64 = -1
		var latestEndTime int64 = 0

		// Process each span (word) in the paragraph
		for j, span := range para.Spans {
			wordText := strings.TrimSpace(span.Text)
			if wordText == "" {
				continue
			}

			startMs, err := parseTTMLTime(span.Begin)
			if err != nil {
				log.Warnf("[TTML Parser] Failed to parse span start time %s: %v", span.Begin, err)
				continue
			}

			endMs, err := parseTTMLTime(span.End)
			if err != nil {
				log.Warnf("[TTML Parser] Failed to parse span end time %s: %v", span.End, err)
				continue
			}

			durationMs := endMs - startMs

			// Track earliest and latest times
			if earliestTime == -1 || startMs < earliestTime {
				earliestTime = startMs
			}
			if endMs > latestEndTime {
				latestEndTime = endMs
			}

			// Create syllable with timing information
			syllable := Syllable{
				Text:      wordText,
				StartTime: strconv.FormatInt(startMs, 10),
				EndTime:   strconv.FormatInt(endMs, 10),
				Duration:  strconv.FormatInt(durationMs, 10),
			}
			syllables = append(syllables, syllable)

			// Build full text
			if j > 0 {
				fullText += " "
			}
			fullText += wordText

			log.Debugf("[TTML Parser]   Span %d: '%s' [%s - %s]", j, wordText, span.Begin, span.End)
		}

		if len(syllables) == 0 {
			log.Warnf("[TTML Parser] Skipping paragraph %d - no valid syllables extracted", i)
			continue
		}

		duration := latestEndTime - earliestTime

		line := Line{
			StartTimeMs: strconv.FormatInt(earliestTime, 10),
			EndTimeMs:   strconv.FormatInt(latestEndTime, 10),
			DurationMs:  strconv.FormatInt(duration, 10),
			Words:       fullText,
			Syllables:   syllables,
		}

			log.Debugf("[TTML Parser]   Created line %d: startMs=%s, endMs=%s, words='%s', syllables=%d", i, line.StartTimeMs, line.EndTimeMs, line.Words, len(line.Syllables))
			lines = append(lines, line)
		}
	}

	log.Infof("[TTML Parser] Successfully extracted %d lines from TTML", len(lines))
	return lines, nil
}


// FetchTTMLLyrics is the main function to fetch TTML API lyrics
func FetchTTMLLyrics(songName, artistName string) ([]Line, bool, string, error) {
	if accountManager == nil {
		initAccountManager()
	}

	storefront := accountManager.getCurrentAccount().Storefront
	if storefront == "" {
		storefront = "us"
	}

	// Validate input
	if songName == "" && artistName == "" {
		return nil, false, "", fmt.Errorf("song name and artist name cannot both be empty")
	}

	// Search for track
	query := songName + " " + artistName
	log.Infof("Searching TTML API for: %s", query)

	track, err := searchTrack(query, storefront)
	if err != nil {
		return nil, false, "", fmt.Errorf("search failed: %v", err)
	}

	if track == nil {
		return nil, false, "", fmt.Errorf("no track found for query: %s", query)
	}

	log.Infof("Found track: %s by %s (ID: %s)", track.Attributes.Name, track.Attributes.ArtistName, track.ID)

	// Fetch TTML lyrics
	ttml, err := fetchLyricsTTML(track.ID, storefront)
	if err != nil {
		return nil, false, "", fmt.Errorf("failed to fetch TTML: %v", err)
	}

	if ttml == "" {
		return nil, false, "", fmt.Errorf("TTML content is empty")
	}

	// Parse TTML directly to lines
	lines, err := parseTTMLToLines(ttml)
	if err != nil {
		return nil, false, "", fmt.Errorf("failed to parse TTML: %v", err)
	}

	if len(lines) == 0 {
		return nil, false, "", fmt.Errorf("no lines extracted from TTML")
	}

	// Try to detect language from TTML
	language := detectLanguageFromTTML(ttml)
	if language == "" {
		language = "en" // Default to English
	}
	isRTL := IsRTLLanguage(language)

	log.Infof("Successfully parsed %d lines from TTML API", len(lines))

	return lines, isRTL, language, nil
}

// =============================================================================
// UTILITY FUNCTIONS
// =============================================================================

// detectLanguageFromTTML extracts language from TTML content
func detectLanguageFromTTML(ttml string) string {
	// Try to extract xml:lang attribute
	langRegex := regexp.MustCompile(`xml:lang="([^"]+)"`)
	matches := langRegex.FindStringSubmatch(ttml)
	if len(matches) > 1 {
		return matches[1]
	}

	// Default to English
	return "en"
}

// IsRTLLanguage checks if a language code is right-to-left
func IsRTLLanguage(langCode string) bool {
	rtlLanguages := map[string]bool{
		"ar": true, // Arabic
		"fa": true, // Persian (Farsi)
		"he": true, // Hebrew
		"ur": true, // Urdu
		"ps": true, // Pashto
		"sd": true, // Sindhi
		"ug": true, // Uyghur
		"yi": true, // Yiddish
		"ku": true, // Kurdish (some dialects)
		"dv": true, // Divehi (Maldivian)
	}
	return rtlLanguages[langCode]
}
