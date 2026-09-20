package store

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	normalizedDurationRe = regexp.MustCompile(`^(.*)\s(\d+)s$`)
	bracketedDurationRe  = regexp.MustCompile(`^(.*)\s\[(\d+)s\]$`)
)

// DeriveKey splits a stored cache_key string into provider, base_key (the key
// minus any trailing duration token), and duration. It is the single owner of
// this mapping: the offline migrator and the live server both key rows through it.
func DeriveKey(cacheKey string) Key {
	provider := cacheKey
	if idx := strings.Index(cacheKey, ":"); idx >= 0 {
		provider = cacheKey[:idx]
	}
	provider = strings.TrimSuffix(provider, "_lyrics")

	baseKey := cacheKey
	var durationSec *int
	if m := bracketedDurationRe.FindStringSubmatch(cacheKey); m != nil {
		if d, err := strconv.Atoi(m[2]); err == nil {
			baseKey = m[1]
			durationSec = &d
		}
	} else if m := normalizedDurationRe.FindStringSubmatch(cacheKey); m != nil {
		if d, err := strconv.Atoi(m[2]); err == nil {
			baseKey = m[1]
			durationSec = &d
		}
	}

	return Key{Provider: provider, BaseKey: baseKey, DurationSec: durationSec}
}
