package ttml

import (
	"fmt"

	log "github.com/sirupsen/logrus"
)

// FetchTTMLLyrics is the main function to fetch TTML API lyrics
// durationMs is optional (0 means no duration filter), used to find closest matching track by duration
// Returns: raw TTML string, track duration in ms, similarity score, error
func FetchTTMLLyrics(songName, artistName, albumName string, durationMs int) (string, int, float64, error) {
	if accountManager == nil {
		initAccountManager()
	}

	storefront := accountManager.getCurrentAccount().Storefront
	if storefront == "" {
		storefront = "us"
	}

	if songName == "" && artistName == "" {
		return "", 0, 0.0, fmt.Errorf("song name and artist name cannot both be empty")
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

	track, score, err := searchTrack(query, storefront, songName, artistName, albumName, durationMs)
	if err != nil {
		return "", 0, 0.0, fmt.Errorf("search failed: %v", err)
	}

	if track == nil {
		return "", 0, 0.0, fmt.Errorf("no track found for query: %s", query)
	}

	trackDurationMs := track.Attributes.DurationInMillis

	if durationMs > 0 {
		durationDiff := trackDurationMs - durationMs
		if durationDiff < 0 {
			durationDiff = -durationDiff
		}
		log.Infof("Found track: %s by %s (ID: %s, duration: %dms, diff: %dms)",
			track.Attributes.Name, track.Attributes.ArtistName, track.ID,
			trackDurationMs, durationDiff)
	} else {
		log.Infof("Found track: %s by %s (ID: %s, duration: %dms)", track.Attributes.Name, track.Attributes.ArtistName, track.ID, trackDurationMs)
	}

	ttml, err := fetchLyricsTTML(track.ID, storefront)
	if err != nil {
		return "", 0, 0.0, fmt.Errorf("failed to fetch TTML: %v", err)
	}

	if ttml == "" {
		return "", 0, 0.0, fmt.Errorf("TTML content is empty")
	}

	log.Infof("Successfully fetched TTML from API (similarity score: %.3f)", score)

	return ttml, trackDurationMs, score, nil
}
