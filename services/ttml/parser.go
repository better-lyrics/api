package ttml

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

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

	// Check both timing attributes (regular and itunes namespace)
	timingType := strings.ToLower(ttml.Timing)
	if timingType == "" {
		timingType = strings.ToLower(ttml.ITunesTiming)
	}
	if timingType == "" {
		timingType = "line" // Default to line if not specified
	}
	log.Debugf("[TTML Parser] Timing type: %s", timingType)

	agentMap := make(map[string]string)
	for _, agent := range ttml.Head.Metadata.Agents {
		agentMap[agent.ID] = agent.Type
	}
	log.Debugf("[TTML Parser] Found %d agents in metadata", len(agentMap))

	log.Debugf("[TTML Parser] Successfully parsed XML structure")
	log.Debugf("[TTML Parser] Number of div sections found: %d", len(ttml.Body.Divs))

	var lines []Line

	// Handle unsynced lyrics (timing="none")
	if timingType == "none" {
		log.Debugf("[TTML Parser] Processing unsynced lyrics")
		for divIdx, div := range ttml.Body.Divs {
			log.Debugf("[TTML Parser] Processing div %d with %d paragraphs", divIdx, len(div.Paragraphs))

			for i, para := range div.Paragraphs {
				// Remove HTML tags from paragraph text
				re := regexp.MustCompile(`<[^>]+>`)
				lineText := re.ReplaceAllString(para.Text, "")
				lineText = strings.TrimSpace(lineText)

				if lineText == "" {
					log.Debugf("[TTML Parser] Skipping empty paragraph %d", i)
					continue
				}

				// Create a line with no timing information
				line := Line{
					StartTimeMs: "0",
					EndTimeMs:   "0",
					DurationMs:  "0",
					Words:       lineText,
					Syllables:   []Syllable{}, // Empty for unsynced lyrics
				}

				log.Debugf("[TTML Parser] Created unsynced line %d: '%s'", i, lineText)
				lines = append(lines, line)
			}
		}
		log.Infof("[TTML Parser] Successfully extracted %d unsynced lines from TTML", len(lines))
		return lines, timingType, nil
	}

	// Handle synced lyrics (word-level or line-level)
	for divIdx, div := range ttml.Body.Divs {
		log.Debugf("[TTML Parser] Processing div %d (songPart: %s) with %d paragraphs", divIdx, div.SongPart, len(div.Paragraphs))

		for i, para := range div.Paragraphs {
			log.Debugf("[TTML Parser]   Processing paragraph %d: begin=%s, end=%s, spans=%d", i, para.Begin, para.End, len(para.Spans))

			if len(para.Spans) > 0 {
				// Extract full paragraph text (with HTML tags removed)
				re := regexp.MustCompile(`<[^>]+>`)
				fullText := re.ReplaceAllString(para.Text, "")
				fullText = strings.TrimSpace(fullText)

				var syllables []Syllable
				var earliestTime int64 = -1
				var latestEndTime int64 = 0
				var wordsIndex int = 0

				for j, span := range para.Spans {
					// Check if this span has nested spans (background vocals structure)
					if len(span.NestedSpans) > 0 && span.Role == "x-bg" {
						// Process nested spans with background flag
						for k, nestedSpan := range span.NestedSpans {
							syllableText := strings.TrimSpace(nestedSpan.Text)
							if syllableText == "" {
								continue
							}

							startMs, err := parseTTMLTime(nestedSpan.Begin)
							if err != nil {
								log.Warnf("[TTML Parser] Failed to parse nested span start time %s: %v", nestedSpan.Begin, err)
								continue
							}

							endMs, err := parseTTMLTime(nestedSpan.End)
							if err != nil {
								log.Warnf("[TTML Parser] Failed to parse nested span end time %s: %v", nestedSpan.End, err)
								continue
							}

							if earliestTime == -1 || startMs < earliestTime {
								earliestTime = startMs
							}
							if endMs > latestEndTime {
								latestEndTime = endMs
							}

							// Find where this syllable appears in the full text
							nextWordIndex := strings.Index(fullText[wordsIndex:], syllableText)
							if nextWordIndex < 0 {
								log.Errorf("[TTML Parser] Error parsing timings in paragraph %d, span %d, nested %d: syllable '%s' not found in remaining text starting at index %d", i, j, k, syllableText, wordsIndex)
								break
							}
							nextWordIndex += wordsIndex // Convert relative index to absolute

							// If there's gap text before this syllable, add it as zero-duration
							if nextWordIndex-wordsIndex > 0 {
								extraText := fullText[wordsIndex:nextWordIndex]
								log.Debugf("[TTML Parser]   Found gap text: '%s'", extraText)

								// Use timing and background status from first syllable or current if first
								var gapStartTime int64
								var gapIsBackground bool
								if len(syllables) > 0 {
									// Use the start time and background status of the FIRST syllable
									firstStartMs, _ := strconv.ParseInt(syllables[0].StartTime, 10, 64)
									gapStartTime = firstStartMs
									gapIsBackground = syllables[0].IsBackground
								} else {
									// First syllable, use current syllable's start time and true for background
									gapStartTime = startMs
									gapIsBackground = true
								}

								gapSyllable := Syllable{
									Text:         extraText,
									StartTime:    strconv.FormatInt(gapStartTime, 10),
									EndTime:      strconv.FormatInt(gapStartTime, 10), // Zero duration
									IsBackground: gapIsBackground,
								}
								syllables = append(syllables, gapSyllable)
								wordsIndex = nextWordIndex
							} else {
								log.Debugf("[TTML Parser]   No gap text before syllable")
							}

							// Add the actual syllable with background flag
							syllable := Syllable{
								Text:         syllableText,
								StartTime:    strconv.FormatInt(startMs, 10),
								EndTime:      strconv.FormatInt(endMs, 10),
								IsBackground: true, // Background vocal
							}
							syllables = append(syllables, syllable)
							wordsIndex += len(syllableText)

							log.Debugf("[TTML Parser]   Nested span %d.%d: '%s' [%s - %s] bg=true", j, k, syllableText, nestedSpan.Begin, nestedSpan.End)
						}
						continue
					}

					// Regular span processing (non-background)
					syllableText := strings.TrimSpace(span.Text)
					if syllableText == "" {
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

					if earliestTime == -1 || startMs < earliestTime {
						earliestTime = startMs
					}
					if endMs > latestEndTime {
						latestEndTime = endMs
					}

					// Check if this is a background vocal (legacy format)
					isBackground := span.Role == "x-bg"

					// Find where this syllable appears in the full text
					nextWordIndex := strings.Index(fullText[wordsIndex:], syllableText)
					if nextWordIndex < 0 {
						log.Errorf("[TTML Parser] Error parsing timings in paragraph %d, span %d: syllable '%s' not found in remaining text starting at index %d", i, j, syllableText, wordsIndex)
						break
					}
					nextWordIndex += wordsIndex // Convert relative index to absolute

					// If there's gap text before this syllable, add it as zero-duration
					if nextWordIndex-wordsIndex > 0 {
						extraText := fullText[wordsIndex:nextWordIndex]
						log.Debugf("[TTML Parser]   Found gap text: '%s'", extraText)

						// Use timing and background status from first syllable or current if first
						var gapStartTime int64
						var gapIsBackground bool
						if len(syllables) > 0 {
							// Use the start time and background status of the FIRST syllable
							firstStartMs, _ := strconv.ParseInt(syllables[0].StartTime, 10, 64)
							gapStartTime = firstStartMs
							gapIsBackground = syllables[0].IsBackground
						} else {
							// First syllable, use current syllable's start time and false for background
							gapStartTime = startMs
							gapIsBackground = false
						}

						gapSyllable := Syllable{
							Text:         extraText,
							StartTime:    strconv.FormatInt(gapStartTime, 10),
							EndTime:      strconv.FormatInt(gapStartTime, 10), // Zero duration
							IsBackground: gapIsBackground,
						}
						syllables = append(syllables, gapSyllable)
						wordsIndex = nextWordIndex
					} else {
						log.Debugf("[TTML Parser]   No gap text before syllable")
					}

					// Add the actual syllable
					syllable := Syllable{
						Text:         syllableText,
						StartTime:    strconv.FormatInt(startMs, 10),
						EndTime:      strconv.FormatInt(endMs, 10),
						IsBackground: isBackground,
					}
					syllables = append(syllables, syllable)
					wordsIndex += len(syllableText)

					log.Debugf("[TTML Parser]   Span %d: '%s' [%s - %s] role='%s' bg=%v", j, syllableText, span.Begin, span.End, span.Role, isBackground)
				}

				if len(syllables) == 0 {
					log.Warnf("[TTML Parser] Skipping paragraph %d - no valid syllables extracted", i)
					continue
				}

				duration := latestEndTime - earliestTime

				agent := para.Agent
				if agent != "" {
					if agentType, ok := agentMap[agent]; ok {
						agent = agentType + ":" + para.Agent
					}
				}

				line := Line{
					StartTimeMs: strconv.FormatInt(earliestTime, 10),
					EndTimeMs:   strconv.FormatInt(latestEndTime, 10),
					DurationMs:  strconv.FormatInt(duration, 10),
					Words:       fullText,
					Syllables:   syllables,
					Agent:       agent,
				}

				log.Debugf("[TTML Parser]   Created line %d: startMs=%s, endMs=%s, words='%s', syllables=%d, agent=%s", i, line.StartTimeMs, line.EndTimeMs, line.Words, len(line.Syllables), agent)
				lines = append(lines, line)
			} else {
				// Line-level TTML without spans
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

				agent := para.Agent
				if agent != "" {
					if agentType, ok := agentMap[agent]; ok {
						agent = agentType + ":" + para.Agent
					}
				}

				line := Line{
					StartTimeMs: strconv.FormatInt(startMs, 10),
					EndTimeMs:   strconv.FormatInt(endMs, 10),
					DurationMs:  strconv.FormatInt(durationMs, 10),
					Words:       lineText,
					Syllables:   []Syllable{}, // Empty for line-level lyrics
					Agent:       agent,
				}

				log.Debugf("[TTML Parser]   Created line-level line %d: startMs=%s, endMs=%s, words='%s', agent=%s", i, line.StartTimeMs, line.EndTimeMs, line.Words, agent)
				lines = append(lines, line)
			}
		}
	}

	log.Infof("[TTML Parser] Successfully extracted %d lines from TTML (type: %s)", len(lines), timingType)
	return lines, timingType, nil
}
