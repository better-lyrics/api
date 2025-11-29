package ttml

import (
	"fmt"
	"lyrics-api-go/logcolors"

	log "github.com/sirupsen/logrus"
)

// FetchTTMLLyrics is the main function to fetch TTML API lyrics
// durationMs is optional (0 means no duration filter), used to find closest matching track by duration
// Returns: raw TTML string, track duration in ms, similarity score, error
func FetchTTMLLyrics(songName, artistName, albumName string, durationMs int) (string, int, float64, error) {
	if accountManager == nil {
		initAccountManager()
	}

	if !accountManager.hasAccounts() {
		return "", 0, 0.0, fmt.Errorf("no TTML accounts configured")
	}

	// Select initial account for the request
	account := accountManager.getNextAccount()
	storefront := account.Storefront
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
		log.Infof("%s Starting with account %s | Query: %s (duration: %dms)", logcolors.LogRequest, account.NameID, query, durationMs)
	} else {
		log.Infof("%s Starting with account %s | Query: %s", logcolors.LogRequest, account.NameID, query)
	}

	// Search returns the account that succeeded (may differ if retry occurred)
	track, score, workingAccount, err := searchTrack(query, storefront, songName, artistName, albumName, durationMs, account)
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
		log.Infof("%s %s - %s (ID: %s, duration: %dms, diff: %dms, score: %.3f)",
			logcolors.LogMatch, track.Attributes.Name, track.Attributes.ArtistName, track.ID,
			trackDurationMs, durationDiff, score)
	} else {
		log.Infof("%s %s - %s (ID: %s, duration: %dms, score: %.3f)",
			logcolors.LogMatch, track.Attributes.Name, track.Attributes.ArtistName, track.ID, trackDurationMs, score)
	}

	// Use the same account that succeeded for search to fetch lyrics
	// This ensures we don't hit a quarantined account
	ttml, err := fetchLyricsTTML(track.ID, storefront, workingAccount)
	if err != nil {
		return "", 0, 0.0, fmt.Errorf("failed to fetch TTML: %v", err)
	}

	if ttml == "" {
		return "", 0, 0.0, fmt.Errorf("TTML content is empty")
	}

	log.Infof("%s Fetched TTML via %s for: %s - %s (%d bytes)",
		logcolors.LogSuccess, workingAccount.NameID, track.Attributes.Name, track.Attributes.ArtistName, len(ttml))

	return ttml, trackDurationMs, score, nil
}
