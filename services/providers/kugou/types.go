package kugou

// SearchResponse represents the response from Kugou lyrics search API
type SearchResponse struct {
	Status   int    `json:"status"`
	Info     string `json:"info"`
	ErrCode  int    `json:"errcode"`
	ErrMsg   string `json:"errmsg"`
	Keyword  string `json:"keyword"`
	Proposal string `json:"proposal"`
	UGC      int    `json:"ugc"`
	Expire   int    `json:"expire"`

	Candidates []LyricsCandidate `json:"candidates"`
}

// LyricsCandidate represents a lyrics match candidate
type LyricsCandidate struct {
	ID          string `json:"id"`
	ProductFrom string `json:"product_from"`
	AccessKey   string `json:"accesskey"`
	CanScore    bool   `json:"can_score"`
	Singer      string `json:"singer"`
	Song        string `json:"song"`
	Duration    int    `json:"duration"` // Duration in milliseconds
	UID         string `json:"uid"`
	Nickname    string `json:"nickname"`
	Language    string `json:"language"`
	KRCType     int    `json:"krctype"` // 1 = synced, 2 = other
	HitLayer    int    `json:"hitlayer"`
	Score       int    `json:"score"`
	ContentType int    `json:"contenttype"`
}

// DownloadResponse represents the response from Kugou lyrics download API
type DownloadResponse struct {
	Status      int    `json:"status"`
	Info        string `json:"info"`
	ErrorCode   int    `json:"error_code"`
	Fmt         string `json:"fmt"`
	ContentType int    `json:"contenttype"`
	Source      string `json:"_source"`
	Charset     string `json:"charset"`
	Content     string `json:"content"` // Base64-encoded LRC content
}

// SongSearchResponse represents the response from Kugou song search API
type SongSearchResponse struct {
	Status  int `json:"status"`
	ErrCode int `json:"errcode"`
	Data    struct {
		Timestamp int64      `json:"timestamp"`
		Total     int        `json:"total"`
		Info      []SongInfo `json:"info"`
	} `json:"data"`
}

// SongInfo represents a song from the search results
type SongInfo struct {
	Hash           string `json:"hash"`
	SQHash         string `json:"sqhash"`
	Hash320        string `json:"320hash"`
	SongName       string `json:"songname"`
	SingerName     string `json:"singername"`
	AlbumName      string `json:"album_name"`
	AlbumID        string `json:"album_id"`
	Duration       int    `json:"duration"` // Duration in seconds
	MVHash         string `json:"mvhash"`
	Filename       string `json:"filename"`
	AudioID        int    `json:"audio_id"`
	AlbumAudioID   int    `json:"album_audio_id"`
	OtherName      string `json:"othername"`
	SongNameOrig   string `json:"songname_original"`
	OtherNameOrig  string `json:"othername_original"`
	PayType        int    `json:"pay_type"`
	SourceID       int    `json:"sourceid"`
	OwnerCount     int    `json:"ownercount"`
	Bitrate        int    `json:"bitrate"`
	FileSize       int    `json:"filesize"`
	FileSize320    int    `json:"320filesize"`
	SQFileSize     int    `json:"sqfilesize"`
	ExtName        string `json:"extname"`
	IsNew          int    `json:"isnew"`
	SourceType     int    `json:"srctype"`
	RPType         string `json:"rp_type"`
	FailProcess    int    `json:"fail_process"`
	FailProcess320 int    `json:"fail_process_320"`
	Accompany      int    `json:"Accompany"`
}
