package providers

import (
	"errors"
	"testing"
)

func TestIsRTLLanguage(t *testing.T) {
	tests := []struct {
		name     string
		langCode string
		expected bool
	}{
		// RTL languages
		{"Arabic", "ar", true},
		{"Persian/Farsi", "fa", true},
		{"Hebrew", "he", true},
		{"Urdu", "ur", true},
		{"Pashto", "ps", true},
		{"Sindhi", "sd", true},
		{"Uyghur", "ug", true},
		{"Yiddish", "yi", true},
		{"Kurdish", "ku", true},
		{"Divehi/Maldivian", "dv", true},

		// LTR languages (should return false)
		{"English", "en", false},
		{"Chinese", "zh", false},
		{"Japanese", "ja", false},
		{"Korean", "ko", false},
		{"Spanish", "es", false},
		{"French", "fr", false},
		{"German", "de", false},
		{"Russian", "ru", false},
		{"Portuguese", "pt", false},
		{"Italian", "it", false},

		// Edge cases
		{"Empty string", "", false},
		{"Unknown code", "xx", false},
		{"Uppercase (not normalized)", "AR", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRTLLanguage(tt.langCode)
			if result != tt.expected {
				t.Errorf("IsRTLLanguage(%q) = %v, expected %v", tt.langCode, result, tt.expected)
			}
		})
	}
}

func TestProviderError_Error(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		message  string
		err      error
		expected string
	}{
		{
			name:     "Without wrapped error",
			provider: "kugou",
			message:  "song search failed",
			err:      nil,
			expected: "kugou: song search failed",
		},
		{
			name:     "With wrapped error",
			provider: "ttml",
			message:  "API request failed",
			err:      errors.New("connection timeout"),
			expected: "ttml: API request failed: connection timeout",
		},
		{
			name:     "Empty provider name",
			provider: "",
			message:  "some error",
			err:      nil,
			expected: ": some error",
		},
		{
			name:     "Empty message",
			provider: "legacy",
			message:  "",
			err:      nil,
			expected: "legacy: ",
		},
		{
			name:     "Empty message with wrapped error",
			provider: "legacy",
			message:  "",
			err:      errors.New("underlying error"),
			expected: "legacy: : underlying error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pe := &ProviderError{
				Provider: tt.provider,
				Message:  tt.message,
				Err:      tt.err,
			}
			result := pe.Error()
			if result != tt.expected {
				t.Errorf("Error() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestProviderError_Unwrap(t *testing.T) {
	t.Run("With wrapped error", func(t *testing.T) {
		underlying := errors.New("underlying error")
		pe := &ProviderError{
			Provider: "kugou",
			Message:  "operation failed",
			Err:      underlying,
		}

		unwrapped := pe.Unwrap()
		if unwrapped != underlying {
			t.Errorf("Unwrap() = %v, expected %v", unwrapped, underlying)
		}

		// Test that errors.Is works correctly
		if !errors.Is(pe, underlying) {
			t.Error("errors.Is should find the underlying error")
		}
	})

	t.Run("Without wrapped error", func(t *testing.T) {
		pe := &ProviderError{
			Provider: "kugou",
			Message:  "no underlying",
			Err:      nil,
		}

		unwrapped := pe.Unwrap()
		if unwrapped != nil {
			t.Errorf("Unwrap() = %v, expected nil", unwrapped)
		}
	})
}

func TestNewProviderError(t *testing.T) {
	t.Run("Creates error with all fields", func(t *testing.T) {
		underlying := errors.New("network error")
		pe := NewProviderError("ttml", "request failed", underlying)

		if pe.Provider != "ttml" {
			t.Errorf("Provider = %q, expected %q", pe.Provider, "ttml")
		}
		if pe.Message != "request failed" {
			t.Errorf("Message = %q, expected %q", pe.Message, "request failed")
		}
		if pe.Err != underlying {
			t.Errorf("Err = %v, expected %v", pe.Err, underlying)
		}
	})

	t.Run("Creates error without wrapped error", func(t *testing.T) {
		pe := NewProviderError("legacy", "not found", nil)

		if pe.Provider != "legacy" {
			t.Errorf("Provider = %q, expected %q", pe.Provider, "legacy")
		}
		if pe.Message != "not found" {
			t.Errorf("Message = %q, expected %q", pe.Message, "not found")
		}
		if pe.Err != nil {
			t.Errorf("Err = %v, expected nil", pe.Err)
		}
	})
}

func TestProviderError_ErrorsAs(t *testing.T) {
	pe := NewProviderError("kugou", "test error", nil)

	var target *ProviderError
	if !errors.As(pe, &target) {
		t.Error("errors.As should match ProviderError")
	}

	if target.Provider != "kugou" {
		t.Errorf("Provider = %q, expected %q", target.Provider, "kugou")
	}
}
