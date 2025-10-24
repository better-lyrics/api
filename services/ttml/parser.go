package ttml

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

// =============================================================================
// TTML PARSING FUNCTIONS
// =============================================================================

// parseTTMLTime parses TTML timestamp to milliseconds
func parseTTMLTime(timeStr string) (int64, error) {
	// Format: "0:00:12.34" or "12.34" or "12"
	parts := strings.Split(timeStr, ":")

	var hours, minutes, seconds float64
	var err error

	switch len(parts) {
	case 1:
		// Just seconds
		seconds, err = strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return 0, err
		}
	case 2:
		// Minutes:seconds
		minutes, _ = strconv.ParseFloat(parts[0], 64)
		seconds, err = strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return 0, err
		}
	case 3:
		// Hours:minutes:seconds
		hours, _ = strconv.ParseFloat(parts[0], 64)
		minutes, _ = strconv.ParseFloat(parts[1], 64)
		seconds, err = strconv.ParseFloat(parts[2], 64)
		if err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("invalid time format: %s", timeStr)
	}

	totalSeconds := hours*3600 + minutes*60 + seconds
	return int64(totalSeconds * 1000), nil
}

// Parse TTML directly to Lines (handles word-level TTML)
// Returns: lines, timingType, error
func parseTTMLToLines(ttmlContent string) ([]Line, string, error) {
	log.Debugf("[TTML Parser] Starting to parse TTML content (length: %d bytes)", len(ttmlContent))

	var ttml TTML
	if err := xml.Unmarshal([]byte(ttmlContent), &ttml); err != nil {
		log.Errorf("[TTML Parser] Failed to unmarshal XML: %v", err)
		return nil, "", fmt.Errorf("failed to parse TTML XML: %v", err)
	}

	// Detect timing type from the TTML root element
	timingType := strings.ToLower(ttml.Timing)
	if timingType == "" {
		timingType = "line" // Default to line if not specified
	}
	log.Debugf("[TTML Parser] Timing type: %s", timingType)

	log.Debugf("[TTML Parser] Successfully parsed XML structure")
	log.Debugf("[TTML Parser] Number of div sections found: %d", len(ttml.Body.Divs))

	var lines []Line

	// TTML is either word-level or line-level
	// Iterate through all div sections (Verse, Chorus, etc.)
	for divIdx, div := range ttml.Body.Divs {
		log.Debugf("[TTML Parser] Processing div %d (songPart: %s) with %d paragraphs", divIdx, div.SongPart, len(div.Paragraphs))

		for i, para := range div.Paragraphs {
			log.Debugf("[TTML Parser]   Processing paragraph %d: begin=%s, end=%s, spans=%d", i, para.Begin, para.End, len(para.Spans))

			// Check if this is word-level (with spans) or line-level (no spans)
			if len(para.Spans) > 0 {
				// Word-level TTML with <span> elements
				var syllables []Syllable
				var fullText string
				var earliestTime int64 = -1
				var latestEndTime int64 = 0

				// Process each span (word) in the paragraph
				for j, span := range para.Spans {
					wordText := strings.TrimSpace(span.Text)
					if wordText == "" {
						continue
					}

					startMs, err := parseTTMLTime(span.Begin)
					if err != nil {
						log.Warnf("[TTML Parser] Failed to parse span start time %s: %v", span.Begin, err)
						continue
					}

					endMs, err := parseTTMLTime(span.End)
					if err != nil {
						log.Warnf("[TTML Parser] Failed to parse span end time %s: %v", span.End, err)
						continue
					}

					durationMs := endMs - startMs

					// Track earliest and latest times
					if earliestTime == -1 || startMs < earliestTime {
						earliestTime = startMs
					}
					if endMs > latestEndTime {
						latestEndTime = endMs
					}

					// Create syllable with timing information
					syllable := Syllable{
						Text:      wordText,
						StartTime: strconv.FormatInt(startMs, 10),
						EndTime:   strconv.FormatInt(endMs, 10),
						Duration:  strconv.FormatInt(durationMs, 10),
					}
					syllables = append(syllables, syllable)

					// Build full text
					if j > 0 {
						fullText += " "
					}
					fullText += wordText

					log.Debugf("[TTML Parser]   Span %d: '%s' [%s - %s]", j, wordText, span.Begin, span.End)
				}

				if len(syllables) == 0 {
					log.Warnf("[TTML Parser] Skipping paragraph %d - no valid syllables extracted", i)
					continue
				}

				duration := latestEndTime - earliestTime

				line := Line{
					StartTimeMs: strconv.FormatInt(earliestTime, 10),
					EndTimeMs:   strconv.FormatInt(latestEndTime, 10),
					DurationMs:  strconv.FormatInt(duration, 10),
					Words:       fullText,
					Syllables:   syllables,
				}

				log.Debugf("[TTML Parser]   Created line %d: startMs=%s, endMs=%s, words='%s', syllables=%d", i, line.StartTimeMs, line.EndTimeMs, line.Words, len(line.Syllables))
				lines = append(lines, line)
			} else {
				// Line-level TTML without spans - extract text directly from paragraph
				// Extract text from the paragraph (strip XML tags)
				re := regexp.MustCompile(`<[^>]+>`)
				lineText := re.ReplaceAllString(para.Text, "")
				lineText = strings.TrimSpace(lineText)

				if lineText == "" {
					log.Warnf("[TTML Parser] Skipping paragraph %d - empty text", i)
					continue
				}

				startMs, err := parseTTMLTime(para.Begin)
				if err != nil {
					log.Warnf("[TTML Parser] Failed to parse line start time %s: %v", para.Begin, err)
					continue
				}

				endMs, err := parseTTMLTime(para.End)
				if err != nil {
					log.Warnf("[TTML Parser] Failed to parse line end time %s: %v", para.End, err)
					continue
				}

				durationMs := endMs - startMs

				// For line-level, create a single syllable for the entire line
				syllable := Syllable{
					Text:      lineText,
					StartTime: strconv.FormatInt(startMs, 10),
					EndTime:   strconv.FormatInt(endMs, 10),
					Duration:  strconv.FormatInt(durationMs, 10),
				}

				line := Line{
					StartTimeMs: strconv.FormatInt(startMs, 10),
					EndTimeMs:   strconv.FormatInt(endMs, 10),
					DurationMs:  strconv.FormatInt(durationMs, 10),
					Words:       lineText,
					Syllables:   []Syllable{syllable},
				}

				log.Debugf("[TTML Parser]   Created line-level line %d: startMs=%s, endMs=%s, words='%s'", i, line.StartTimeMs, line.EndTimeMs, line.Words)
				lines = append(lines, line)
			}
		}
	}

	log.Infof("[TTML Parser] Successfully extracted %d lines from TTML (type: %s)", len(lines), timingType)
	return lines, timingType, nil
}
