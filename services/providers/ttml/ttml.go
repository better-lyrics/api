package ttml

import (
	"encoding/json"
	"fmt"
	"lyrics-api-go/logcolors"

	log "github.com/sirupsen/logrus"
)

// FetchLyricsByTrackID fetches TTML lyrics directly by Apple Music track ID, skipping search.
// Used by the /override endpoint to correct cached lyrics with a known-good track ID.
func FetchLyricsByTrackID(trackID string) (string, error) {
	if accountManager == nil {
		initAccountManager()
	}

	if !accountManager.hasAccounts() {
		return "", fmt.Errorf("no TTML accounts configured")
	}

	if apiCircuitBreaker == nil {
		initCircuitBreaker()
	}
	if apiCircuitBreaker.IsOpen() {
		timeUntilRetry := apiCircuitBreaker.TimeUntilRetry()
		if timeUntilRetry > 0 {
			return "", fmt.Errorf("circuit breaker is open, API temporarily unavailable (retry in %v)", timeUntilRetry)
		}
	}

	account := accountManager.getNextAccount()
	storefront := account.Storefront
	if storefront == "" {
		storefront = "us"
	}

	log.Infof("%s Fetching lyrics by track ID %s via %s", logcolors.LogRequest, trackID, logcolors.Account(account.NameID))

	ttml, err := fetchLyricsTTML(trackID, storefront, account)
	if err != nil {
		return "", fmt.Errorf("failed to fetch TTML for track %s: %v", trackID, err)
	}

	if ttml == "" {
		return "", fmt.Errorf("TTML content is empty for track %s", trackID)
	}

	log.Infof("%s Fetched TTML by track ID %s via %s (%d bytes)",
		logcolors.LogSuccess, trackID, logcolors.Account(account.NameID), len(ttml))

	return ttml, nil
}

// FetchTTMLLyrics is the main function to fetch TTML API lyrics
// durationMs is optional (0 means no duration filter), used to find closest matching track by duration
// Returns: raw TTML string, track duration in ms, similarity score, track metadata, error
func FetchTTMLLyrics(songName, artistName, albumName string, durationMs int) (string, int, float64, *TrackMeta, error) {
	if accountManager == nil {
		initAccountManager()
	}

	if !accountManager.hasAccounts() {
		return "", 0, 0.0, nil, fmt.Errorf("no TTML accounts configured")
	}

	// Early-exit if circuit breaker is definitely open (avoid unnecessary work)
	// Use read-only checks to avoid consuming the half-open test slot
	// The authoritative Allow() check happens in makeAPIRequestWithAccount
	if apiCircuitBreaker == nil {
		initCircuitBreaker()
	}
	if apiCircuitBreaker.IsOpen() {
		timeUntilRetry := apiCircuitBreaker.TimeUntilRetry()
		if timeUntilRetry > 0 {
			return "", 0, 0.0, nil, fmt.Errorf("circuit breaker is open, API temporarily unavailable (retry in %v)", timeUntilRetry)
		}
		// Cooldown passed - let it through, Allow() will handle the HALF-OPEN transition
	}

	// Select initial account for the request (only if circuit breaker allows)
	account := accountManager.getNextAccount()
	storefront := account.Storefront
	if storefront == "" {
		storefront = "us"
	}

	if songName == "" && artistName == "" {
		return "", 0, 0.0, nil, fmt.Errorf("song name and artist name cannot both be empty")
	}

	query := songName + " " + artistName
	if albumName != "" {
		query += " " + albumName
	}

	if durationMs > 0 {
		log.Infof("%s Starting with account %s | Query: %s (duration: %dms)", logcolors.LogRequest, logcolors.Account(account.NameID), query, durationMs)
	} else {
		log.Infof("%s Starting with account %s | Query: %s", logcolors.LogRequest, logcolors.Account(account.NameID), query)
	}

	// Search returns the account that succeeded (may differ if retry occurred)
	track, score, workingAccount, err := searchTrack(query, storefront, songName, artistName, albumName, durationMs, account)
	if err != nil {
		return "", 0, 0.0, nil, fmt.Errorf("search failed: %v", err)
	}

	if track == nil {
		return "", 0, 0.0, nil, fmt.Errorf("no track found for query: %s", query)
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

	// Build TrackMeta early so it's available even on lyrics-fetch errors
	rawAttrsJSON, _ := json.Marshal(track.Attributes)
	trackMeta := &TrackMeta{
		TrackID:             track.ID,
		Name:                track.Attributes.Name,
		ArtistName:          track.Attributes.ArtistName,
		AlbumName:           track.Attributes.AlbumName,
		ISRC:                track.Attributes.ISRC,
		ReleaseDate:         track.Attributes.ReleaseDate,
		HasTimeSyncedLyrics: track.Attributes.HasTimeSyncedLyrics,
		RawAttributes:       string(rawAttrsJSON),
	}

	// Check hasTimeSyncedLyrics to potentially skip the lyrics fetch
	if track.Attributes.HasTimeSyncedLyrics == nil {
		// Field absent from API response — log warning and fall through to normal fetch
		log.Warnf("%s hasTimeSyncedLyrics field missing from search response for %s - %s, falling back to lyrics fetch",
			logcolors.LogWarning, track.Attributes.Name, track.Attributes.ArtistName)
	} else if !*track.Attributes.HasTimeSyncedLyrics {
		// Explicitly false — skip lyrics fetch entirely (saves an API call)
		log.Infof("%s Skipping lyrics fetch: hasTimeSyncedLyrics=false for %s - %s",
			logcolors.LogLyrics, track.Attributes.Name, track.Attributes.ArtistName)
		return "", trackDurationMs, score, trackMeta, fmt.Errorf("no lyrics data found (hasTimeSyncedLyrics=false)")
	}

	// Use the same account that succeeded for search to fetch lyrics
	// This ensures we don't hit a quarantined account
	ttml, err := fetchLyricsTTML(track.ID, storefront, workingAccount)
	if err != nil {
		return "", trackDurationMs, score, trackMeta, fmt.Errorf("failed to fetch TTML: %v", err)
	}

	if ttml == "" {
		return "", trackDurationMs, score, trackMeta, fmt.Errorf("TTML content is empty")
	}

	log.Infof("%s Fetched TTML via %s for: %s - %s (%d bytes)",
		logcolors.LogSuccess, logcolors.Account(workingAccount.NameID), track.Attributes.Name, track.Attributes.ArtistName, len(ttml))

	return ttml, trackDurationMs, score, trackMeta, nil
}
