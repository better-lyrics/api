package cache

import "strings"

// prefixOf returns the public counter name for a cache key. The cache key format
// is "<prefix>:<rest>". Known prefixes get mapped to short public names; the
// special "no_lyrics" prefix becomes "negative". Anything else (including the
// no-colon case) becomes "unknown".
func prefixOf(key string) string {
	idx := strings.IndexByte(key, ':')
	if idx <= 0 {
		return "unknown"
	}
	prefix := key[:idx]
	if prefix == "no_lyrics" {
		return "negative"
	}
	return strings.TrimSuffix(prefix, "_lyrics")
}
