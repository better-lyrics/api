package ttml

import (
	"fmt"

	log "github.com/sirupsen/logrus"
)

// FetchTTMLLyrics is the main function to fetch TTML API lyrics
// Returns: lyrics, isRTL, language, timingType, rawTTML (only on parsing errors), error
func FetchTTMLLyrics(songName, artistName string) ([]Line, bool, string, string, string, error) {
	if accountManager == nil {
		initAccountManager()
	}

	storefront := accountManager.getCurrentAccount().Storefront
	if storefront == "" {
		storefront = "us"
	}

	// Validate input
	if songName == "" && artistName == "" {
		return nil, false, "", "", "", fmt.Errorf("song name and artist name cannot both be empty")
	}

	// Search for track
	query := songName + " " + artistName
	log.Infof("Searching TTML API for: %s", query)

	track, err := searchTrack(query, storefront)
	if err != nil {
		return nil, false, "", "", "", fmt.Errorf("search failed: %v", err)
	}

	if track == nil {
		return nil, false, "", "", "", fmt.Errorf("no track found for query: %s", query)
	}

	log.Infof("Found track: %s by %s (ID: %s)", track.Attributes.Name, track.Attributes.ArtistName, track.ID)

	// Fetch TTML lyrics
	ttml, err := fetchLyricsTTML(track.ID, storefront)
	if err != nil {
		return nil, false, "", "", "", fmt.Errorf("failed to fetch TTML: %v", err)
	}

	if ttml == "" {
		return nil, false, "", "", "", fmt.Errorf("TTML content is empty")
	}

	// Parse TTML directly to lines
	lines, timingType, err := parseTTMLToLines(ttml)
	if err != nil {
		// Return raw TTML for debugging parsing errors
		return nil, false, "", "", ttml, fmt.Errorf("failed to parse TTML: %v", err)
	}

	if len(lines) == 0 {
		// Return raw TTML for debugging when no lines extracted
		return nil, false, "", timingType, ttml, fmt.Errorf("no lines extracted from TTML")
	}

	// Try to detect language from TTML
	language := detectLanguageFromTTML(ttml)
	if language == "" {
		language = "en" // Default to English
	}
	isRTL := IsRTLLanguage(language)

	log.Infof("Successfully parsed %d lines from TTML API", len(lines))

	return lines, isRTL, language, timingType, "", nil
}
