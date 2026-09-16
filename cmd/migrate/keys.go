package main

import (
	"regexp"
	"strconv"
	"strings"

	"lyrics-api-go/internal/store"
)

var (
	normalizedDurationRe = regexp.MustCompile(`^(.*)\s(\d+)s$`)
	bracketedDurationRe  = regexp.MustCompile(`^(.*)\s\[(\d+)s\]$`)
)

func deriveKey(cacheKey string) store.Key {
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

	return store.Key{Provider: provider, BaseKey: baseKey, DurationSec: durationSec}
}
