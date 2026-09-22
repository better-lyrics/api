package ttml

import (
	"encoding/json"
	"fmt"
	"lyrics-api-go/logcolors"

	log "github.com/sirupsen/logrus"
)

// FetchLyricsByTrackID fetches TTML lyrics directly by Apple Music track ID, skipping search.
// Used by the /override endpoint to correct cached lyrics with a known-good track ID.
func FetchLyricsByTrackID(trackID string, priority bool) (string, error) {
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

	ttml, err := fetchLyricsTTML(trackID, storefront, account, priority)
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

func FetchTTMLLyrics(songName, artistName, albumName string, durationMs int, priority bool, appleFirst bool) (string, int, float64, *TrackMeta, error) {
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

	// Minted lane first (no account spent); account lane only on mint/request failure.
	track, score, workingAccount, err := searchTwoLane(query, storefront, songName, artistName, albumName, durationMs, account, priority)
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

	trackMeta := trackMetaFrom(track)

	appleFetch := func() (string, error) {
		// Fetch from the account that actually succeeded for search, using ITS
		// storefront: on failover workingAccount can differ from the initial account
		// and live in a different storefront, so the initial one would be wrong.
		lyricsStorefront := workingAccount.Storefront
		if lyricsStorefront == "" {
			lyricsStorefront = "us"
		}
		ttml, err := fetchLyricsTTML(track.ID, lyricsStorefront, workingAccount, priority)
		if err != nil {
			return "", fmt.Errorf("failed to fetch TTML: %v", err)
		}
		if ttml == "" {
			return "", fmt.Errorf("TTML content is empty")
		}
		log.Infof("%s Fetched TTML via %s for: %s - %s (%d bytes)",
			logcolors.LogSuccess, logcolors.Account(workingAccount.NameID), track.Attributes.Name, track.Attributes.ArtistName, len(ttml))
		return ttml, nil
	}

	lyricsTTML, source, err := resolveLyrics(track, appleFirst, fetchLRCRedByISRC, appleFetch)
	trackMeta.Source = source
	if err != nil {
		return "", trackDurationMs, score, trackMeta, err
	}
	return lyricsTTML, trackDurationMs, score, trackMeta, nil
}

func resolveLyrics(track *Track, appleFirst bool, lrcRedFetch func(string) (string, bool, error), appleFetch func() (string, error)) (string, string, error) {
	tryLRCRed := func() (string, bool) {
		if lrcTTML, ok, lrcErr := lrcRedFetch(track.Attributes.ISRC); lrcErr != nil {
			log.Warnf("%s lrc.red lookup failed for ISRC %q: %v", logcolors.LogLyrics, track.Attributes.ISRC, lrcErr)
		} else if ok {
			log.Infof("%s Fetched lyrics from lrc.red for: %s - %s (%d bytes)",
				logcolors.LogSuccess, track.Attributes.Name, track.Attributes.ArtistName, len(lrcTTML))
			return lrcTTML, true
		}
		return "", false
	}

	tryApple := func() (string, error) {
		if track.Attributes.HasTimeSyncedLyrics == nil {
			log.Warnf("%s hasTimeSyncedLyrics field missing from search response for %s - %s, falling back to lyrics fetch",
				logcolors.LogWarning, track.Attributes.Name, track.Attributes.ArtistName)
		} else if !*track.Attributes.HasTimeSyncedLyrics {
			log.Infof("%s Skipping lyrics fetch: hasTimeSyncedLyrics=false for %s - %s",
				logcolors.LogLyrics, track.Attributes.Name, track.Attributes.ArtistName)
			return "", fmt.Errorf("no lyrics data found (hasTimeSyncedLyrics=false)")
		}
		return appleFetch()
	}

	if appleFirst {
		appleTTML, appleErr := tryApple()
		if appleErr == nil && appleTTML != "" {
			return appleTTML, SourceApple, nil
		}
		if lrcTTML, ok := tryLRCRed(); ok {
			return lrcTTML, SourceLRCRed, nil
		}
		return appleTTML, SourceApple, appleErr
	}

	if lrcTTML, ok := tryLRCRed(); ok {
		return lrcTTML, SourceLRCRed, nil
	}
	ttml, err := tryApple()
	return ttml, SourceApple, err
}

func trackMetaFrom(track *Track) *TrackMeta {
	rawAttrs := string(track.RawAttributes)
	if rawAttrs == "" {
		b, _ := json.Marshal(track.Attributes)
		rawAttrs = string(b)
	}
	return &TrackMeta{
		TrackID:             track.ID,
		Name:                track.Attributes.Name,
		ArtistName:          track.Attributes.ArtistName,
		AlbumName:           track.Attributes.AlbumName,
		ISRC:                track.Attributes.ISRC,
		ReleaseDate:         track.Attributes.ReleaseDate,
		HasTimeSyncedLyrics: track.Attributes.HasTimeSyncedLyrics,
		RawAttributes:       rawAttrs,
		Source:              SourceApple,
	}
}
