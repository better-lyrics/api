package ttml

import "regexp"

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

// IsRTLLanguage checks if a language code is right-to-left
func IsRTLLanguage(langCode string) bool {
	rtlLanguages := map[string]bool{
		"ar": true, // Arabic
		"fa": true, // Persian (Farsi)
		"he": true, // Hebrew
		"ur": true, // Urdu
		"ps": true, // Pashto
		"sd": true, // Sindhi
		"ug": true, // Uyghur
		"yi": true, // Yiddish
		"ku": true, // Kurdish (some dialects)
		"dv": true, // Divehi (Maldivian)
	}
	return rtlLanguages[langCode]
}
