package ttml

import "encoding/xml"

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
// ACCOUNT MANAGEMENT TYPES
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
	XMLName xml.Name `xml:"tt"`
	Timing  string   `xml:"timing,attr"`
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
