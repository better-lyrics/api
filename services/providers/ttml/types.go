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

// =============================================================================
// ACCOUNT MANAGEMENT TYPES
// =============================================================================

type MusicAccount struct {
	NameID         string
	BearerToken    string
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

type Track struct {
	ID         string `json:"id"`
	Attributes struct {
		Name             string `json:"name"`
		ArtistName       string `json:"artistName"`
		AlbumName        string `json:"albumName"`
		DurationInMillis int    `json:"durationInMillis"`
		URL              string `json:"url"`
		ISRC             string `json:"isrc"`
		SongwriterNames  string `json:"songwriterName"`
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
