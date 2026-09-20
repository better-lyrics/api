package httpapi

import (
	"encoding/json"
	"net/http"
)

// apiResponse centralizes the standard headers (X-Auth-Mode, X-Cache-Status,
// X-RateLimit-Type, X-Provider) set from request context.
type apiResponse struct {
	w           http.ResponseWriter
	r           *http.Request
	cacheStatus string
	provider    string
}

func respond(w http.ResponseWriter, r *http.Request) *apiResponse {
	return &apiResponse{w: w, r: r}
}

func (a *apiResponse) SetCacheStatus(status string) *apiResponse {
	a.cacheStatus = status
	return a
}

func (a *apiResponse) SetProvider(provider string) *apiResponse {
	a.provider = provider
	return a
}

func (a *apiResponse) writeHeaders() {
	a.w.Header().Set("Content-Type", "application/json")

	if a.cacheStatus != "" {
		a.w.Header().Set("X-Cache-Status", a.cacheStatus)
	}
	if a.provider != "" {
		a.w.Header().Set("X-Provider", a.provider)
	}

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

	if rateLimitType, ok := a.r.Context().Value(rateLimitTypeKey).(string); ok && rateLimitType != "" {
		a.w.Header().Set("X-RateLimit-Type", rateLimitType)
	}
}

func (a *apiResponse) JSON(data interface{}) error {
	a.writeHeaders()
	return json.NewEncoder(a.w).Encode(data)
}

func (a *apiResponse) Error(statusCode int, data interface{}) error {
	a.writeHeaders()
	a.w.WriteHeader(statusCode)
	return json.NewEncoder(a.w).Encode(data)
}
