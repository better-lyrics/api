package ttml

import (
	"fmt"

	log "github.com/sirupsen/logrus"
)

// FetchTTMLLyrics is the main function to fetch TTML API lyrics
// Returns: raw TTML string, error
func FetchTTMLLyrics(songName, artistName, albumName string) (string, error) {
	if accountManager == nil {
		initAccountManager()
	}

	storefront := accountManager.getCurrentAccount().Storefront
	if storefront == "" {
		storefront = "us"
	}

	if songName == "" && artistName == "" {
		return "", fmt.Errorf("song name and artist name cannot both be empty")
	}

	query := songName + " " + artistName
	if albumName != "" {
		query += " " + albumName
	}
	log.Infof("Searching TTML API for: %s", query)

	track, err := searchTrack(query, storefront)
	if err != nil {
		return "", fmt.Errorf("search failed: %v", err)
	}

	if track == nil {
		return "", fmt.Errorf("no track found for query: %s", query)
	}

	log.Infof("Found track: %s by %s (ID: %s)", track.Attributes.Name, track.Attributes.ArtistName, track.ID)

	ttml, err := fetchLyricsTTML(track.ID, storefront)
	if err != nil {
		return "", fmt.Errorf("failed to fetch TTML: %v", err)
	}

	if ttml == "" {
		return "", fmt.Errorf("TTML content is empty")
	}

	log.Infof("Successfully fetched TTML from API")

	return ttml, nil
}
