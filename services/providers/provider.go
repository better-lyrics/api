package providers

import (
	"context"
	"fmt"
	"sync"
)

// Provider defines the interface that all lyrics providers must implement
type Provider interface {
	// Name returns the provider's identifier (e.g., "ttml", "kugou", "legacy")
	Name() string

	// FetchLyrics fetches lyrics for the given song
	// Parameters:
	//   - ctx: context for cancellation and timeouts
	//   - song: song name
	//   - artist: artist name
	//   - album: album name (optional, can be empty)
	//   - durationMs: expected track duration in milliseconds (optional, 0 means no filter)
	// Returns:
	//   - *LyricsResult: the lyrics result if found
	//   - error: any error that occurred
	FetchLyrics(ctx context.Context, song, artist, album string, durationMs int) (*LyricsResult, error)

	// CacheKeyPrefix returns the prefix used for cache keys (e.g., "ttml_lyrics", "kugou_lyrics")
	CacheKeyPrefix() string
}

// Registry holds all registered providers
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

var (
	globalRegistry *Registry
	registryOnce   sync.Once
)

// GetRegistry returns the global provider registry
func GetRegistry() *Registry {
	registryOnce.Do(func() {
		globalRegistry = &Registry{
			providers: make(map[string]Provider),
		}
	})
	return globalRegistry
}

// Register adds a provider to the registry
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

// Get retrieves a provider by name
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", name)
	}
	return p, nil
}

// List returns all registered provider names
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

// Has checks if a provider is registered
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.providers[name]
	return ok
}

// Register is a convenience function to register a provider in the global registry
func Register(p Provider) {
	GetRegistry().Register(p)
}

// Get is a convenience function to get a provider from the global registry
func Get(name string) (Provider, error) {
	return GetRegistry().Get(name)
}

// List is a convenience function to list all providers in the global registry
func List() []string {
	return GetRegistry().List()
}

// Has is a convenience function to check if a provider exists in the global registry
func Has(name string) bool {
	return GetRegistry().Has(name)
}
