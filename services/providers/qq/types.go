package qq

import "encoding/json"

// QQComm contains common request parameters
type QQComm struct {
	WID        string `json:"wid"`
	CV         int    `json:"cv"`
	V          int    `json:"v"`
	QIMEI36    string `json:"QIMEI36"`
	CT         string `json:"ct"`
	TmeAppID   string `json:"tmeAppID"`
	Format     string `json:"format"`
	InCharset  string `json:"inCharset"`
	OutCharset string `json:"outCharset"`
	UID        string `json:"uid"`
}

// QQAPIModule represents a single module call within the unified API request
type QQAPIModule struct {
	Module string      `json:"module"`
	Method string      `json:"method"`
	Param  interface{} `json:"param"`
}

// SearchParam contains search request parameters
type SearchParam struct {
	SearchID   string `json:"searchid"`
	Query      string `json:"query"`
	SearchType int    `json:"search_type"`
	NumPerPage int    `json:"num_per_page"`
	PageNum    int    `json:"page_num"`
	Highlight  int    `json:"highlight"`
	Grp        int    `json:"grp"`
}

// LyricsParam contains lyrics fetch request parameters
type LyricsParam struct {
	Crypt   int    `json:"crypt"`
	CT      int    `json:"ct"`
	CV      int    `json:"cv"`
	LrcT    int    `json:"lrc_t"`
	QRC     int    `json:"qrc"`
	QRCT    int    `json:"qrc_t"`
	Roma    int    `json:"roma"`
	RomaT   int    `json:"roma_t"`
	Trans   int    `json:"trans"`
	TransT  int    `json:"trans_t"`
	Type    int    `json:"type"`
	SongMID string `json:"songMid"`
}

// SongItem represents a song from QQ Music search results
type SongItem struct {
	Title    string   `json:"title"`
	Singer   []Singer `json:"singer"`
	Album    Album    `json:"album"`
	Interval int      `json:"interval"` // Duration in seconds
	MID      string   `json:"mid"`
	ID       int      `json:"id"`
}

// Singer represents an artist in QQ Music API
type Singer struct {
	Name string `json:"name"`
	MID  string `json:"mid"`
}

// Album represents an album in QQ Music API
type Album struct {
	Name  string `json:"name"`
	MID   string `json:"mid"`
	Title string `json:"title"`
}

// SingerNames returns a comma-separated string of all singer names
func (s SongItem) SingerNames() string {
	if len(s.Singer) == 0 {
		return ""
	}
	names := make([]string, len(s.Singer))
	for i, singer := range s.Singer {
		names[i] = singer.Name
	}
	result := names[0]
	for i := 1; i < len(names); i++ {
		result += ", " + names[i]
	}
	return result
}

// apiResponse is used for raw JSON parsing of the dynamic-keyed response
type apiResponse struct {
	Code int             `json:"code"`
	Raw  json.RawMessage `json:"-"`
}

// moduleResult represents a single module result within the API response
type moduleResult struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
}

// searchData represents the search result data
type searchData struct {
	Body struct {
		ItemSong []SongItem `json:"item_song"`
		Song     struct {
			List []SongItem `json:"list"`
		} `json:"song"`
	} `json:"body"`
}

// lyricsData represents the lyrics result data
// QRC and Lyric can be either strings (hex content) or numbers (0 when unavailable)
type lyricsData struct {
	Lyric json.RawMessage `json:"lyric"`
	QRC   json.RawMessage `json:"qrc"`
	Trans json.RawMessage `json:"trans"`
}

// rawToString extracts a string from a JSON value that may be a string or number
func rawToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	// If it's a number (like 0), it's not a valid lyrics value
	return ""
}
