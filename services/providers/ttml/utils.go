package ttml

import (
	"regexp"

	"lyrics-api-go/services/providers"
)

// =============================================================================
// UTILITY FUNCTIONS
// =============================================================================

// detectLanguageFromTTML extracts language from TTML content
func detectLanguageFromTTML(ttml string) string {
	// Try to extract xml:lang attribute
	langRegex := regexp.MustCompile(`xml:lang="([^"]+)"`)
	matches := langRegex.FindStringSubmatch(ttml)
	if len(matches) > 1 {
		return matches[1]
	}

	// Default to English
	return "en"
}

// IsRTLLanguage is an alias for the shared providers.IsRTLLanguage function
var IsRTLLanguage = providers.IsRTLLanguage
