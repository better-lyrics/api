package utils

import (
	"strings"
	"testing"
)

func TestCompressAndDecompressString(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{
			name: "Short string",
			text: "Hello, world!",
		},
		{
			name: "Longer JSON string",
			text: `{"name":"John Doe","age":30,"city":"New York"}`,
		},
		{
			name: "Empty string",
			text: "",
		},
		{
			name: "TTML-like content",
			text: `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns="http://www.w3.org/ns/ttml">
  <body>
    <div>
      <p begin="00:00:01.000" end="00:00:05.000">Hello world</p>
    </div>
  </body>
</tt>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := CompressString(tt.text)
			if err != nil {
				t.Fatalf("CompressString error: %v", err)
			}

			decompressed, err := DecompressString(compressed)
			if err != nil {
				t.Fatalf("DecompressString error: %v", err)
			}

			if decompressed != tt.text {
				t.Errorf("Expected decompressed string %q, got %q", tt.text, decompressed)
			}
		})
	}
}

func TestCompressionRatio(t *testing.T) {
	// Repetitive TTML content should compress well
	content := strings.Repeat(`<p begin="00:00:01.000" end="00:00:05.000">Hello world lyrics</p>`, 100)

	compressed, err := CompressString(content)
	if err != nil {
		t.Fatalf("CompressString error: %v", err)
	}

	ratio := float64(len(compressed)) / float64(len(content))
	t.Logf("Original: %d bytes, Compressed: %d bytes, Ratio: %.2f", len(content), len(compressed), ratio)

	// Repetitive content should compress to less than 10% of original
	if ratio > 0.1 {
		t.Errorf("Expected compression ratio < 0.1 for repetitive content, got %.2f", ratio)
	}
}

func TestInvalidBase64Decompression(t *testing.T) {
	invalidInput := "invalid_base64_string"

	_, err := DecompressString(invalidInput)
	if err == nil {
		t.Error("Expected error when decompressing invalid base64 string")
	}
}
