package main

import (
	"encoding/json"
	"net/http"
)

// APIResponse handles consistent header setting and JSON responses.
// It centralizes the logic for setting X-Auth-Mode, X-Cache-Status,
// X-RateLimit-Type, and other standard headers based on request context.
type APIResponse struct {
	w           http.ResponseWriter
	r           *http.Request
	cacheStatus string
	provider    string
}

// Respond creates a response helper from request context
func Respond(w http.ResponseWriter, r *http.Request) *APIResponse {
	return &APIResponse{w: w, r: r}
}

// SetCacheStatus sets the X-Cache-Status header value
func (a *APIResponse) SetCacheStatus(status string) *APIResponse {
	a.cacheStatus = status
	return a
}

// SetProvider sets the X-Provider header value
func (a *APIResponse) SetProvider(provider string) *APIResponse {
	a.provider = provider
	return a
}

// writeHeaders sets all standard headers based on context
func (a *APIResponse) writeHeaders() {
	a.w.Header().Set("Content-Type", "application/json")

	if a.cacheStatus != "" {
		a.w.Header().Set("X-Cache-Status", a.cacheStatus)
	}
	if a.provider != "" {
		a.w.Header().Set("X-Provider", a.provider)
	}

	// Auth mode from context
	apiKeyAuthenticated, _ := a.r.Context().Value(apiKeyAuthenticatedKey).(bool)
	apiKeyInvalid, _ := a.r.Context().Value(apiKeyInvalidKey).(bool)
	apiKeyRequired, _ := a.r.Context().Value(apiKeyRequiredForFreshKey).(bool)

	if apiKeyAuthenticated {
		a.w.Header().Set("X-Auth-Mode", "authenticated")
	} else if apiKeyInvalid {
		a.w.Header().Set("X-Auth-Mode", "invalid")
	} else if apiKeyRequired {
		a.w.Header().Set("X-Auth-Mode", "cache")
	}

	// Rate limit type from context
	if rateLimitType, ok := a.r.Context().Value(rateLimitTypeKey).(string); ok && rateLimitType != "" {
		a.w.Header().Set("X-RateLimit-Type", rateLimitType)
	}
}

// JSON writes headers and encodes data as JSON (200 OK)
func (a *APIResponse) JSON(data interface{}) error {
	a.writeHeaders()
	return json.NewEncoder(a.w).Encode(data)
}

// Error writes headers, sets status code, and encodes error response
func (a *APIResponse) Error(statusCode int, data interface{}) error {
	a.writeHeaders()
	a.w.WriteHeader(statusCode)
	return json.NewEncoder(a.w).Encode(data)
}
