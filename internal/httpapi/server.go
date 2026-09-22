package httpapi

import (
	"sync"

	"lyrics-api-go/config"
	"lyrics-api-go/internal/store"
)

type contextKey string

const (
	cacheOnlyModeKey          contextKey = "cacheOnlyMode"
	rateLimitTypeKey          contextKey = "rateLimitType"
	apiKeyRequiredForFreshKey contextKey = "apiKeyRequiredForFresh"
	apiKeyAuthenticatedKey    contextKey = "apiKeyAuthenticated"
	apiKeyInvalidKey          contextKey = "apiKeyInvalid"
)

// NoLyricsSentinel is stored as TTML content to permanently mark a track as having no lyrics.
const NoLyricsSentinel = "__NO_LYRICS__"

// Server holds the dependencies every handler needs. Handlers are methods on it,
// replacing the old package-level globals.
type Server struct {
	store *store.Store
	cfg   config.Config

	inFlight sync.Map
}

// InFlightRequest deduplicates concurrent fetches for the same cache key.
type inFlightRequest struct {
	wg     sync.WaitGroup
	result string
	score  float64
	err    error
}

func New(st *store.Store, cfg config.Config) *Server {
	return &Server{store: st, cfg: cfg}
}
