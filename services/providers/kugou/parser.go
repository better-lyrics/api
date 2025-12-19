package kugou

import (
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"

	"lyrics-api-go/services/providers"
)

var (
	// LRC timestamp pattern: [mm:ss.xx] or [mm:ss:xx]
	lrcTimeRegex = regexp.MustCompile(`\[(\d{2}):(\d{2})[\.:]+(\d{2,3})\]`)

	// Metadata tags pattern: [tag:value]
	metadataRegex = regexp.MustCompile(`^\[([a-zA-Z]+):([^\]]*)\]$`)
)

// ParseLRC parses LRC format lyrics into Lines
// LRC format: [mm:ss.xx]lyrics text
func ParseLRC(lrcContent string) ([]providers.Line, map[string]string, error) {
	lines := []providers.Line{}
	metadata := make(map[string]string)

	// Split content into lines
	rawLines := strings.Split(lrcContent, "\n")

	for i, rawLine := range rawLines {
		rawLine = strings.TrimSpace(rawLine)
		if rawLine == "" {
			continue
		}

		// Check for metadata tags like [ar:Artist], [ti:Title], etc.
		if matches := metadataRegex.FindStringSubmatch(rawLine); len(matches) == 3 {
			tag := strings.ToLower(matches[1])
			value := strings.TrimSpace(matches[2])

			// Store metadata
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
			case "id", "hash", "sign", "qq", "total":
				// Skip internal tags
				continue
			}
			continue
		}

		// Parse timed lyrics line
		// Find all timestamps at the beginning
		timestamps := []int64{}
		text := rawLine

		for {
			loc := lrcTimeRegex.FindStringIndex(text)
			if loc == nil || loc[0] != 0 {
				break
			}

			match := lrcTimeRegex.FindStringSubmatch(text)
			if len(match) < 4 {
				break
			}

			minutes, _ := strconv.ParseInt(match[1], 10, 64)
			seconds, _ := strconv.ParseInt(match[2], 10, 64)
			millisPart := match[3]

			// Handle both [mm:ss.xx] (2 digits) and [mm:ss.xxx] (3 digits)
			millis, _ := strconv.ParseInt(millisPart, 10, 64)
			if len(millisPart) == 2 {
				millis *= 10 // Convert centiseconds to milliseconds
			}

			totalMs := minutes*60*1000 + seconds*1000 + millis
			timestamps = append(timestamps, totalMs)

			text = text[loc[1]:]
		}

		text = strings.TrimSpace(text)

		// Skip lines with no text or only timestamps
		if text == "" || len(timestamps) == 0 {
			continue
		}

		// Create a line for each timestamp (handles karaoke-style multiple timestamps)
		for _, startMs := range timestamps {
			// Calculate end time based on next line's start time
			endMs := startMs + 5000 // Default 5 second duration

			// Look ahead for next timed line to get actual end time
			for j := i + 1; j < len(rawLines); j++ {
				nextLine := strings.TrimSpace(rawLines[j])
				if nextLine == "" {
					continue
				}
				if nextMatch := lrcTimeRegex.FindStringSubmatch(nextLine); len(nextMatch) >= 4 {
					nextMin, _ := strconv.ParseInt(nextMatch[1], 10, 64)
					nextSec, _ := strconv.ParseInt(nextMatch[2], 10, 64)
					nextMillisPart := nextMatch[3]
					nextMillis, _ := strconv.ParseInt(nextMillisPart, 10, 64)
					if len(nextMillisPart) == 2 {
						nextMillis *= 10
					}
					endMs = nextMin*60*1000 + nextSec*1000 + nextMillis
					break
				}
			}

			durationMs := endMs - startMs
			if durationMs < 0 {
				durationMs = 5000 // Default to 5 seconds if calculation goes wrong
			}

			// Create syllables from words (LRC doesn't have word-level timing)
			words := strings.Fields(text)
			syllables := make([]providers.Syllable, len(words))
			wordDuration := durationMs / int64(len(words))

			for wi, word := range words {
				wordStart := startMs + int64(wi)*wordDuration
				wordEnd := wordStart + wordDuration
				syllables[wi] = providers.Syllable{
					Text:      word,
					StartTime: strconv.FormatInt(wordStart, 10),
					EndTime:   strconv.FormatInt(wordEnd, 10),
				}
			}

			line := providers.Line{
				StartTimeMs: strconv.FormatInt(startMs, 10),
				EndTimeMs:   strconv.FormatInt(endMs, 10),
				DurationMs:  strconv.FormatInt(durationMs, 10),
				Words:       text,
				Syllables:   syllables,
			}

			lines = append(lines, line)
		}
	}

	// Sort lines by start time (in case of multiple timestamps per line)
	sortLinesByStartTime(lines)

	return lines, metadata, nil
}

// sortLinesByStartTime sorts lines by their start time
func sortLinesByStartTime(lines []providers.Line) {
	for i := 0; i < len(lines)-1; i++ {
		for j := i + 1; j < len(lines); j++ {
			startI, _ := strconv.ParseInt(lines[i].StartTimeMs, 10, 64)
			startJ, _ := strconv.ParseInt(lines[j].StartTimeMs, 10, 64)
			if startI > startJ {
				lines[i], lines[j] = lines[j], lines[i]
			}
		}
	}
}

// DecodeBase64Content decodes base64-encoded LRC content
func DecodeBase64Content(encoded string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}

	// Remove BOM if present
	content := string(decoded)
	content = strings.TrimPrefix(content, "\ufeff")

	return content, nil
}

// StripLRCMetadata removes metadata tags from LRC content, keeping only timed lyrics
func StripLRCMetadata(lrcContent string) string {
	var cleanLines []string
	rawLines := strings.Split(lrcContent, "\n")

	for _, rawLine := range rawLines {
		rawLine = strings.TrimSpace(rawLine)
		if rawLine == "" {
			continue
		}

		// Skip metadata tags like [ar:Artist], [ti:Title], [id:xxx], etc.
		if matches := metadataRegex.FindStringSubmatch(rawLine); len(matches) == 3 {
			continue
		}

		// Keep only lines with timestamps
		if lrcTimeRegex.MatchString(rawLine) {
			cleanLines = append(cleanLines, rawLine)
		}
	}

	return strings.Join(cleanLines, "\n")
}

// DetectLanguage tries to detect language from LRC metadata or content
func DetectLanguage(metadata map[string]string, content string) string {
	// Check metadata first
	if lang, ok := metadata["language"]; ok && lang != "" {
		return normalizeLanguageCode(lang)
	}

	// Simple heuristic: check for Chinese characters
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

	return "en" // Default to English
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
