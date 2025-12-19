package ttml

import "testing"

func TestDetectLanguageFromTTML(t *testing.T) {
	tests := []struct {
		name     string
		ttml     string
		expected string
	}{
		{
			name:     "English language in TTML",
			ttml:     `<?xml version="1.0" encoding="UTF-8"?><tt xml:lang="en">...</tt>`,
			expected: "en",
		},
		{
			name:     "Spanish language in TTML",
			ttml:     `<?xml version="1.0" encoding="UTF-8"?><tt xml:lang="es">...</tt>`,
			expected: "es",
		},
		{
			name:     "Arabic language in TTML",
			ttml:     `<?xml version="1.0" encoding="UTF-8"?><tt xml:lang="ar">...</tt>`,
			expected: "ar",
		},
		{
			name:     "No language attribute defaults to English",
			ttml:     `<?xml version="1.0" encoding="UTF-8"?><tt>...</tt>`,
			expected: "en",
		},
		{
			name:     "Empty TTML defaults to English",
			ttml:     "",
			expected: "en",
		},
		{
			name:     "Language code with region",
			ttml:     `<?xml version="1.0" encoding="UTF-8"?><tt xml:lang="en-US">...</tt>`,
			expected: "en-US",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectLanguageFromTTML(tt.ttml)
			if result != tt.expected {
				t.Errorf("Expected language %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestIsRTLLanguage(t *testing.T) {
	tests := []struct {
		name     string
		langCode string
		expected bool
	}{
		{
			name:     "Arabic is RTL",
			langCode: "ar",
			expected: true,
		},
		{
			name:     "Hebrew is RTL",
			langCode: "he",
			expected: true,
		},
		{
			name:     "Persian/Farsi is RTL",
			langCode: "fa",
			expected: true,
		},
		{
			name:     "Urdu is RTL",
			langCode: "ur",
			expected: true,
		},
		{
			name:     "Pashto is RTL",
			langCode: "ps",
			expected: true,
		},
		{
			name:     "Sindhi is RTL",
			langCode: "sd",
			expected: true,
		},
		{
			name:     "Uyghur is RTL",
			langCode: "ug",
			expected: true,
		},
		{
			name:     "Yiddish is RTL",
			langCode: "yi",
			expected: true,
		},
		{
			name:     "Kurdish is RTL",
			langCode: "ku",
			expected: true,
		},
		{
			name:     "Divehi is RTL",
			langCode: "dv",
			expected: true,
		},
		{
			name:     "English is not RTL",
			langCode: "en",
			expected: false,
		},
		{
			name:     "Spanish is not RTL",
			langCode: "es",
			expected: false,
		},
		{
			name:     "Japanese is not RTL",
			langCode: "ja",
			expected: false,
		},
		{
			name:     "Chinese is not RTL",
			langCode: "zh",
			expected: false,
		},
		{
			name:     "Empty string is not RTL",
			langCode: "",
			expected: false,
		},
		{
			name:     "Unknown language code is not RTL",
			langCode: "xx",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRTLLanguage(tt.langCode)
			if result != tt.expected {
				t.Errorf("Expected IsRTLLanguage(%q) = %v, got %v", tt.langCode, tt.expected, result)
			}
		})
	}
}
