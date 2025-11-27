# Duration Filtering Behavior Guide

This document describes how the lyrics API handles duration-based filtering and caching across different scenarios.

## Configuration

- **`DURATION_MATCH_DELTA_MS`**: Maximum allowed difference between requested and actual track duration (default: `2000ms` = 2 seconds)

---

## Scenario 1: Request WITH Duration - Exact Cache Hit

**Request:**
```
/getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran&al=Divide&d=234
```

**Cache Key:** `ttml_lyrics:Shape of You Ed Sheeran Divide 234s`

**Flow:**
1. Build cache key with duration → `ttml_lyrics:Shape of You Ed Sheeran Divide 234s`
2. Check cache → **HIT** (found JSON: `{"ttml":"...", "trackDurationMs": 234000}`)
3. Return cached TTML immediately

**Response:**
```json
{"ttml": "..."}
```

**Headers:** `X-Cache-Status: HIT`

---

## Scenario 2: Request WITH Duration - Cache Miss, API Success

**Request:**
```
/getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran&al=Divide&d=234
```

**Flow:**
1. Build cache key → `ttml_lyrics:Shape of You Ed Sheeran Divide 234s`
2. Check cache → **MISS**
3. Call API with `durationMs=234000`
4. **Strict Duration Filter:** API returns 10 tracks, only 3 are within ±2000ms of 234000ms
5. Score remaining 3 tracks by name/artist/album (duration NOT in scoring)
6. Best match: Track with duration 233712ms (diff: 288ms ✓)
7. Fetch TTML for best match
8. Cache with duration: `{"ttml":"...", "trackDurationMs": 233712}`
9. Return TTML

**Response:**
```json
{"ttml": "...", "score": 0.95}
```

**Headers:** `X-Cache-Status: MISS`

---

## Scenario 3: Request WITH Duration - Cache Miss, API Fails, Fallback Exists

**Request:**
```
/getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran&al=Divide&d=234
```

**Flow:**
1. Build cache key → `ttml_lyrics:Shape of You Ed Sheeran Divide 234s`
2. Check cache → **MISS**
3. Call API → **FAILS** (network error, rate limit, etc.)
4. Build fallback key (without album, WITH duration) → `ttml_lyrics:Shape of You Ed Sheeran  234s`
5. Check fallback cache → **HIT**
6. Return stale cached TTML

**Response:**
```json
{"ttml": "..."}
```

**Headers:** `X-Cache-Status: STALE`

---

## Scenario 4: Request WITH Duration - Cache Miss, API Fails, No Fallback

**Request:**
```
/getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran&al=Divide&d=234
```

**Flow:**
1. Build cache key → `ttml_lyrics:Shape of You Ed Sheeran Divide 234s`
2. Check cache → **MISS**
3. Call API → **FAILS**
4. Build fallback key → `ttml_lyrics:Shape of You Ed Sheeran  234s`
5. Check fallback cache → **MISS**
6. Return error

**Response:**
```json
{"error": "search failed: ..."}
```

**Headers:** `X-Cache-Status: MISS`
**Status:** `500`

---

## Scenario 5: Request WITHOUT Duration - Cache Hit

**Request:**
```
/getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran&al=Divide
```

**Cache Key:** `ttml_lyrics:Shape of You Ed Sheeran Divide`

**Flow:**
1. Build cache key (no duration) → `ttml_lyrics:Shape of You Ed Sheeran Divide`
2. Check cache → **HIT**
3. Return cached TTML

**Response:**
```json
{"ttml": "..."}
```

**Headers:** `X-Cache-Status: HIT`

---

## Scenario 6: Request WITHOUT Duration - Cache Miss, API Success

**Request:**
```
/getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran&al=Divide
```

**Flow:**
1. Build cache key → `ttml_lyrics:Shape of You Ed Sheeran Divide`
2. Check cache → **MISS**
3. Call API with `durationMs=0`
4. **No duration filter** - all tracks considered
5. Score all tracks by name/artist/album
6. Best match selected
7. Fetch TTML, cache with track duration: `{"ttml":"...", "trackDurationMs": 233712}`
8. Return TTML

**Response:**
```json
{"ttml": "...", "score": 0.98}
```

**Headers:** `X-Cache-Status: MISS`

---

## Scenario 7: Request WITHOUT Duration - Cache Miss, API Fails, Fallback Exists

**Request:**
```
/getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran&al=Divide
```

**Flow:**
1. Build cache key → `ttml_lyrics:Shape of You Ed Sheeran Divide`
2. Check cache → **MISS**
3. Call API → **FAILS**
4. Build fallback key (without album, no duration) → `ttml_lyrics:Shape of You Ed Sheeran `
5. Check fallback cache → **HIT**
6. Return stale cached TTML

**Response:**
```json
{"ttml": "..."}
```

**Headers:** `X-Cache-Status: STALE`

---

## Scenario 8: Request WITHOUT Duration - No Album Provided

**Request:**
```
/getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran
```

**Cache Key:** `ttml_lyrics:Shape of You Ed Sheeran ` (trailing space from empty album)

**Flow:**
1. Build cache key → `ttml_lyrics:Shape of You Ed Sheeran `
2. Check cache → MISS/HIT
3. If miss, call API, no album in search query
4. No fallback keys generated (album was empty, nothing to fall back from)

---

## Scenario 9: Duration Filter - Tracks Filtered Out

**Request:**
```
/getLyrics?s=Bohemian%20Rhapsody&a=Queen&d=354
```

**API Returns:**

| Track | Duration | Diff from 354000ms | Status |
|-------|----------|-------------------|--------|
| Bohemian Rhapsody (Remastered) | 354320ms | 320ms | ✓ Pass |
| Bohemian Rhapsody (Live) | 380000ms | 26000ms | ✗ Rejected |
| Bohemian Rhapsody (Radio Edit) | 180000ms | 174000ms | ✗ Rejected |

**Flow:**
1. Duration filter with delta=2000ms
2. Only "Remastered" version passes (320ms < 2000ms)
3. Score only the filtered track
4. Return "Remastered" version

**Log:**
```
[Duration Filter] 1/3 tracks passed duration filter (delta: 2000ms)
```

---

## Scenario 10: Duration Filter - No Tracks Match

**Request:**
```
/getLyrics?s=Some%20Song&a=Artist&d=500
```

**API Returns:**

| Track | Duration | Diff from 500000ms | Status |
|-------|----------|-------------------|--------|
| Some Song | 240000ms | 260000ms | ✗ Rejected |
| Some Song (Extended) | 360000ms | 140000ms | ✗ Rejected |

**Flow:**
1. Duration filter with delta=2000ms
2. No tracks pass filter
3. Return error immediately (no API lyrics fetch attempted)

**Response:**
```json
{"error": "no tracks found within 2000ms of requested duration 500000ms"}
```

**Status:** `500`

---

## Scenario 11: Backwards Compatibility - Old Cache Format

**Cache contains old format:**
- Key: `ttml_lyrics:Shape of You Ed Sheeran Divide`
- Value: `"<tt xml:lang=\"en\">...</tt>"` (plain TTML string, not JSON)

**Request:**
```
/getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran&al=Divide
```

**Flow:**
1. Check cache → **HIT** (plain string)
2. `getCachedLyrics` tries JSON parse → fails
3. Falls back to treating value as plain TTML string
4. Returns TTML with `trackDurationMs=0`

**Response:**
```json
{"ttml": "<tt xml:lang=\"en\">...</tt>"}
```

**Headers:** `X-Cache-Status: HIT`

---

## Scenario 12: Different Durations = Different Cache Entries

| Request | Cache Key |
|---------|-----------|
| `/getLyrics?s=Song&a=Artist&d=180` | `ttml_lyrics:Song Artist  180s` |
| `/getLyrics?s=Song&a=Artist&d=240` | `ttml_lyrics:Song Artist  240s` |
| `/getLyrics?s=Song&a=Artist` | `ttml_lyrics:Song Artist ` |

All three are **separate cache entries** - duration requests are isolated from each other.

---

## Summary Table

| Duration in Request | Cache Hit | API Success | Fallback Available | Result |
|:-------------------:|:---------:|:-----------:|:------------------:|--------|
| ✓ | ✓ | - | - | Return cached (exact key) |
| ✓ | ✗ | ✓ | - | Filter → Score → Cache → Return |
| ✓ | ✗ | ✗ | ✓ (with same duration) | Return stale fallback |
| ✓ | ✗ | ✗ | ✗ | Error |
| ✗ | ✓ | - | - | Return cached |
| ✗ | ✗ | ✓ | - | Score → Cache → Return |
| ✗ | ✗ | ✗ | ✓ | Return stale fallback |
| ✗ | ✗ | ✗ | ✗ | Error |

---

## Scoring Weights (without duration)

When scoring tracks, duration is **not** part of the scoring system. It's handled as a strict pre-filter instead.

| Factor | Weight |
|--------|--------|
| Song Name | 50% |
| Artist Name | 37.5% |
| Album Name | 12.5% |

---

## Cache Format

**New format (JSON with duration metadata):**
```json
{
  "ttml": "<tt xml:lang=\"en\">...</tt>",
  "trackDurationMs": 233712
}
```

**Old format (plain TTML string, backwards compatible):**
```
<tt xml:lang="en">...</tt>
```

The system automatically detects and handles both formats.
