package qq

import (
	"regexp"
	"strconv"
	"strings"

	"lyrics-api-go/services/providers"
)

var (
	// Line timing: [startMs,durationMs]text
	lineTimingRegex = regexp.MustCompile(`^\[(\d+),(\d+)\](.*)$`)

	// Word timing: text(startMs,durationMs) — timing follows the word it describes
	wordTimingRegex = regexp.MustCompile(`([^(]+)\((\d+),(\d+)\)`)

	// Metadata: [tag:value]
	metadataRegex = regexp.MustCompile(`^\[([a-zA-Z]+):([^\]]*)\]$`)
)

// ParseQRC parses QRC format lyrics into Lines with word-level timing
func ParseQRC(content string) ([]providers.Line, map[string]string, error) {
	var lines []providers.Line
	metadata := make(map[string]string)

	rawLines := strings.Split(content, "\n")

	for _, rawLine := range rawLines {
		rawLine = strings.TrimSpace(rawLine)
		if rawLine == "" {
			continue
		}

		// Check for metadata tags like [ti:Title], [ar:Artist]
		if matches := metadataRegex.FindStringSubmatch(rawLine); len(matches) == 3 {
			tag := strings.ToLower(matches[1])
			value := strings.TrimSpace(matches[2])

			switch tag {
			case "ar":
				metadata["artist"] = value
			case "ti":
				metadata["title"] = value
			case "al":
				metadata["album"] = value
			case "by":
				metadata["creator"] = value
			case "offset":
				metadata["offset"] = value
			}
			continue
		}

		// Parse timed lyrics: [startMs,durationMs]word-timing-content
		lineMatch := lineTimingRegex.FindStringSubmatch(rawLine)
		if lineMatch == nil {
			continue
		}

		lineStartMs, _ := strconv.ParseInt(lineMatch[1], 10, 64)
		lineDurationMs, _ := strconv.ParseInt(lineMatch[2], 10, 64)
		lineContent := lineMatch[3]
		lineEndMs := lineStartMs + lineDurationMs

		// Parse word-level timing from content
		wordMatches := wordTimingRegex.FindAllStringSubmatch(lineContent, -1)

		var syllables []providers.Syllable
		var fullText strings.Builder

		if len(wordMatches) > 0 {
			for _, wm := range wordMatches {
				wordText := wm[1]
				wordStartMs, _ := strconv.ParseInt(wm[2], 10, 64)
				wordDurationMs, _ := strconv.ParseInt(wm[3], 10, 64)

				// Trim trailing spaces for syllable text but preserve for full line
				trimmedText := strings.TrimSpace(wordText)
				if trimmedText == "" {
					continue
				}

				fullText.WriteString(wordText)

				syllables = append(syllables, providers.Syllable{
					Text:      trimmedText,
					StartTime: strconv.FormatInt(wordStartMs, 10),
					EndTime:   strconv.FormatInt(wordStartMs+wordDurationMs, 10),
				})
			}
		}

		text := strings.TrimSpace(fullText.String())
		if text == "" {
			continue
		}

		line := providers.Line{
			StartTimeMs: strconv.FormatInt(lineStartMs, 10),
			EndTimeMs:   strconv.FormatInt(lineEndMs, 10),
			DurationMs:  strconv.FormatInt(lineDurationMs, 10),
			Words:       text,
			Syllables:   syllables,
		}

		lines = append(lines, line)
	}

	return lines, metadata, nil
}

// DetectLanguage tries to detect language from QRC metadata or content
func DetectLanguage(metadata map[string]string, content string) string {
	if lang, ok := metadata["language"]; ok && lang != "" {
		return normalizeLanguageCode(lang)
	}

	for _, r := range content {
		if r >= '\u4e00' && r <= '\u9fff' {
			return "zh"
		}
		if r >= '\u3040' && r <= '\u309f' { // Hiragana
			return "ja"
		}
		if r >= '\u30a0' && r <= '\u30ff' { // Katakana
			return "ja"
		}
		if r >= '\uac00' && r <= '\ud7af' { // Korean
			return "ko"
		}
	}

	return "en"
}

// normalizeLanguageCode normalizes language names to ISO codes
func normalizeLanguageCode(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	switch lang {
	case "英语", "english", "eng":
		return "en"
	case "中文", "chinese", "chi", "普通话", "国语", "粤语":
		return "zh"
	case "日语", "japanese", "jpn":
		return "ja"
	case "韩语", "korean", "kor":
		return "ko"
	case "西班牙语", "spanish", "spa":
		return "es"
	case "法语", "french", "fra":
		return "fr"
	case "德语", "german", "ger":
		return "de"
	default:
		if len(lang) <= 3 {
			return lang
		}
		return "en"
	}
}
