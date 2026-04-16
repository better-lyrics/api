package ttml

import (
	"encoding/xml"

	"lyrics-api-go/services/providers"
)

// =============================================================================
// DATA STRUCTURES (use shared types from providers package)
// =============================================================================

// Line is an alias for the shared Line type
type Line = providers.Line

// Syllable is an alias for the shared Syllable type
type Syllable = providers.Syllable

// TrackMeta contains metadata about the matched track from Apple Music
type TrackMeta struct {
	TrackID             string // Apple Music track ID
	Name                string
	ArtistName          string
	AlbumName           string
	ISRC                string
	ReleaseDate         string
	HasTimeSyncedLyrics *bool  // nil = field absent from API, false = no synced lyrics, true = has synced lyrics
	RawAttributes       string // JSON string of full Apple Music attributes
}

// =============================================================================
// ACCOUNT MANAGEMENT TYPES
// =============================================================================

// MusicAccount represents a single TTML API account.
// Bearer token is now shared and auto-scraped - only MUT is per-account.
type MusicAccount struct {
	NameID         string
	MediaUserToken string
	Storefront     string
}

type AccountManager struct {
	accounts       []MusicAccount
	currentIndex   uint64        // Use uint64 for atomic operations
	quarantineTime map[int]int64 // account index -> unix timestamp when quarantine ends
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

// Artwork mirrors Apple Music's attributes.artwork object.
type Artwork struct {
	URL        string `json:"url,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	BgColor    string `json:"bgColor,omitempty"`
	TextColor1 string `json:"textColor1,omitempty"`
	TextColor2 string `json:"textColor2,omitempty"`
	TextColor3 string `json:"textColor3,omitempty"`
	TextColor4 string `json:"textColor4,omitempty"`
}

type Track struct {
	ID         string `json:"id"`
	Attributes struct {
		Name                string   `json:"name"`
		ArtistName          string   `json:"artistName"`
		AlbumName           string   `json:"albumName"`
		DurationInMillis    int      `json:"durationInMillis"`
		URL                 string   `json:"url"`
		ISRC                string   `json:"isrc"`
		SongwriterNames     string   `json:"songwriterName"`
		ReleaseDate         string   `json:"releaseDate"`           // ISO 8601 date, e.g. "2008-05-25"
		HasLyrics           *bool    `json:"hasLyrics"`             // nil = field absent from API response
		HasTimeSyncedLyrics *bool    `json:"hasTimeSyncedLyrics"`   // nil = field absent from API response
		Artwork             *Artwork `json:"artwork,omitempty"`     // Apple Music artwork (URL template, dimensions, colors)
		GenreNames          []string `json:"genreNames,omitempty"`  // e.g. ["Pop", "Alternative"]
		ComposerName        string   `json:"composerName,omitempty"`
		HasCredits          *bool    `json:"hasCredits,omitempty"`
	} `json:"attributes"`
}

type LyricsResponse struct {
	Data []struct {
		ID         string `json:"id"`
		Attributes struct {
			TTML              string `json:"ttml"`
			TTMLLocalizations string `json:"ttmlLocalizations"`
		} `json:"attributes"`
	} `json:"data"`
}

// =============================================================================
// ACCOUNT API RESPONSE STRUCTURES
// =============================================================================

// AccountResponse for /v1/me/account endpoint
type AccountResponse struct {
	Meta AccountMeta `json:"meta"`
}

type AccountMeta struct {
	Subscription SubscriptionInfo `json:"subscription"`
}

type SubscriptionInfo struct {
	Active     bool   `json:"active"`
	Storefront string `json:"storefront"`
}

// =============================================================================
// TTML XML STRUCTURES
// =============================================================================

type TTML struct {
	XMLName      xml.Name `xml:"tt"`
	Timing       string   `xml:"timing,attr"`
	ITunesTiming string   `xml:"http://music.apple.com/lyric-ttml-internal timing,attr"`
	Head         TTMLHead `xml:"head"`
	Body         TTMLBody `xml:"body"`
}

type TTMLHead struct {
	Metadata TTMLMetadata `xml:"metadata"`
}

type TTMLMetadata struct {
	Agents []TTMLAgent `xml:"agent"`
}

type TTMLAgent struct {
	ID   string `xml:"id,attr"`
	Type string `xml:"type,attr"`
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
	Begin       string     `xml:"begin,attr"`
	End         string     `xml:"end,attr"`
	Role        string     `xml:"role,attr"`
	Text        string     `xml:",chardata"`
	NestedSpans []TTMLSpan `xml:"span"` // For background vocals with nested structure
}
