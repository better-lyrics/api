package binilink

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
)

type Outcome string

const (
	OutcomeLinked       Outcome = "linked"
	OutcomeExists       Outcome = "exists"
	OutcomeConflict     Outcome = "conflict"
	OutcomeUnresolved   Outcome = "unresolved"
	OutcomeNoISRC       Outcome = "no_isrc"
	OutcomeInvalid      Outcome = "invalid"
	OutcomeUnauthorized Outcome = "unauthorized"
	OutcomeRetry        Outcome = "retry"
)

const ingressPath = "/api/v1/ingress/youtube"

// Post submits one video to Apple track association. The returned duration is the
// server's Retry-After, only meaningful with OutcomeRetry.
func Post(client *http.Client, baseURL, key, userAgent, videoID, appleTrackID string) (Outcome, time.Duration) {
	body, err := json.Marshal(map[string]string{"youtube_id": videoID, "apple_music_id": appleTrackID})
	if err != nil {
		return OutcomeInvalid, 0
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+ingressPath, bytes.NewReader(body))
	if err != nil {
		return OutcomeInvalid, 0
	}
	req.Header.Set("X-Ingress-Key", key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return OutcomeRetry, 0
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	switch resp.StatusCode {
	case http.StatusCreated:
		return OutcomeLinked, 0
	case http.StatusOK:
		return OutcomeExists, 0
	case http.StatusConflict:
		return OutcomeConflict, 0
	case http.StatusNotFound:
		return OutcomeUnresolved, 0
	case http.StatusUnprocessableEntity:
		return OutcomeNoISRC, 0
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return OutcomeInvalid, 0
	case http.StatusUnauthorized:
		return OutcomeUnauthorized, 0
	}
	return OutcomeRetry, retryAfter(resp.Header.Get("Retry-After"))
}

func retryAfter(header string) time.Duration {
	secs, err := strconv.Atoi(header)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
