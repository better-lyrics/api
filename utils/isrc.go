package utils

import (
	"regexp"
	"strings"
)

var isrcPattern = regexp.MustCompile(`^[A-Z0-9]{12}$`)

// NormalizeISRC trims and uppercases an ISRC, returning "" if it is not exactly
// 12 alphanumeric characters. lrc.red keys lyrics by ISRC (12 alphanumerics,
// case-insensitive, normalized to uppercase) on both the read and ingress paths.
func NormalizeISRC(isrc string) string {
	s := strings.ToUpper(strings.TrimSpace(isrc))
	if !isrcPattern.MatchString(s) {
		return ""
	}
	return s
}
