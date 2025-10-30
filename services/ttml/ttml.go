package ttml

import (
	"fmt"

	log "github.com/sirupsen/logrus"
)

// FetchTTMLLyrics is the main function to fetch TTML API lyrics
// durationMs is optional (0 means no duration filter), used to find closest matching track by duration
// Returns: raw TTML string, error
func FetchTTMLLyrics(songName, artistName, albumName string, durationMs int) (string, error) {
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

	if durationMs > 0 {
		log.Infof("Searching TTML API for: %s (duration: %dms)", query, durationMs)
	} else {
		log.Infof("Searching TTML API for: %s", query)
	}

	track, err := searchTrack(query, storefront, songName, artistName, albumName, durationMs)
	if err != nil {
		return "", fmt.Errorf("search failed: %v", err)
	}

	if track == nil {
		return "", fmt.Errorf("no track found for query: %s", query)
	}

	if durationMs > 0 {
		durationDiff := track.Attributes.DurationInMillis - durationMs
		if durationDiff < 0 {
			durationDiff = -durationDiff
		}
		log.Infof("Found track: %s by %s (ID: %s, duration: %dms, diff: %dms)",
			track.Attributes.Name, track.Attributes.ArtistName, track.ID,
			track.Attributes.DurationInMillis, durationDiff)
	} else {
		log.Infof("Found track: %s by %s (ID: %s)", track.Attributes.Name, track.Attributes.ArtistName, track.ID)
	}

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
