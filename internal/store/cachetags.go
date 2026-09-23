package store

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

// SongCacheTag covers every album, duration, and provider variant of a song.
func SongCacheTag(song, artist string) string {
	return cacheTag("s-", song+" "+artist)
}

// KeyCacheTag derives the per-album tag from a stored cache key, for writers that
// only know the key.
func KeyCacheTag(cacheKey string) string {
	base := DeriveKey(cacheKey).BaseKey
	if idx := strings.Index(base, ":"); idx >= 0 {
		base = base[idx+1:]
	}
	return cacheTag("k-", base)
}

// SongCacheTags is the Cloudflare Cache-Tag set for a lyrics response; purging either tag clears it.
func SongCacheTags(song, artist, album string) []string {
	return []string{
		SongCacheTag(song, artist),
		cacheTag("k-", song+" "+artist+" "+album),
	}
}

func cacheTag(prefix, text string) string {
	normalized := strings.Join(strings.Fields(strings.ToLower(text)), " ")
	sum := sha1.Sum([]byte(normalized))
	return prefix + hex.EncodeToString(sum[:])[:20]
}
