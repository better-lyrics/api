package providers

import (
	"context"
	"sync"
	"testing"
)

// mockProvider is a simple provider for testing
type mockProvider struct {
	name           string
	cacheKeyPrefix string
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) CacheKeyPrefix() string {
	return m.cacheKeyPrefix
}

func (m *mockProvider) FetchLyrics(ctx context.Context, song, artist, album string, durationMs int) (*LyricsResult, error) {
	return &LyricsResult{
		Provider: m.name,
		Lines:    []Line{{Words: "test lyrics"}},
	}, nil
}

func newMockProvider(name, prefix string) *mockProvider {
	return &mockProvider{name: name, cacheKeyPrefix: prefix}
}

func TestRegistry_Register(t *testing.T) {
	t.Run("Register single provider", func(t *testing.T) {
		r := &Registry{providers: make(map[string]Provider)}
		p := newMockProvider("test", "test_lyrics")

		r.Register(p)

		if !r.Has("test") {
			t.Error("Provider 'test' should be registered")
		}
	})

	t.Run("Register multiple providers", func(t *testing.T) {
		r := &Registry{providers: make(map[string]Provider)}

		r.Register(newMockProvider("kugou", "kugou_lyrics"))
		r.Register(newMockProvider("ttml", "ttml_lyrics"))
		r.Register(newMockProvider("legacy", "legacy_lyrics"))

		if len(r.providers) != 3 {
			t.Errorf("Expected 3 providers, got %d", len(r.providers))
		}
	})

	t.Run("Register overwrites existing provider", func(t *testing.T) {
		r := &Registry{providers: make(map[string]Provider)}

		r.Register(newMockProvider("test", "old_prefix"))
		r.Register(newMockProvider("test", "new_prefix"))

		p, err := r.Get("test")
		if err != nil {
			t.Fatalf("Failed to get provider: %v", err)
		}

		if p.CacheKeyPrefix() != "new_prefix" {
			t.Errorf("Expected new_prefix, got %s", p.CacheKeyPrefix())
		}
	})
}

func TestRegistry_Get(t *testing.T) {
	r := &Registry{providers: make(map[string]Provider)}
	r.Register(newMockProvider("kugou", "kugou_lyrics"))
	r.Register(newMockProvider("ttml", "ttml_lyrics"))

	t.Run("Get existing provider", func(t *testing.T) {
		p, err := r.Get("kugou")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if p.Name() != "kugou" {
			t.Errorf("Expected 'kugou', got %s", p.Name())
		}
	})

	t.Run("Get another existing provider", func(t *testing.T) {
		p, err := r.Get("ttml")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if p.Name() != "ttml" {
			t.Errorf("Expected 'ttml', got %s", p.Name())
		}
	})

	t.Run("Get non-existent provider returns error", func(t *testing.T) {
		_, err := r.Get("nonexistent")
		if err == nil {
			t.Error("Expected error for non-existent provider")
		}

		expectedErr := "provider not found: nonexistent"
		if err.Error() != expectedErr {
			t.Errorf("Expected error %q, got %q", expectedErr, err.Error())
		}
	})

	t.Run("Get empty name returns error", func(t *testing.T) {
		_, err := r.Get("")
		if err == nil {
			t.Error("Expected error for empty provider name")
		}
	})
}

func TestRegistry_List(t *testing.T) {
	t.Run("List empty registry", func(t *testing.T) {
		r := &Registry{providers: make(map[string]Provider)}
		names := r.List()

		if len(names) != 0 {
			t.Errorf("Expected empty list, got %v", names)
		}
	})

	t.Run("List with providers", func(t *testing.T) {
		r := &Registry{providers: make(map[string]Provider)}
		r.Register(newMockProvider("kugou", "kugou_lyrics"))
		r.Register(newMockProvider("ttml", "ttml_lyrics"))
		r.Register(newMockProvider("legacy", "legacy_lyrics"))

		names := r.List()

		if len(names) != 3 {
			t.Fatalf("Expected 3 names, got %d", len(names))
		}

		// Check all names are present (order not guaranteed)
		nameMap := make(map[string]bool)
		for _, name := range names {
			nameMap[name] = true
		}

		for _, expected := range []string{"kugou", "ttml", "legacy"} {
			if !nameMap[expected] {
				t.Errorf("Expected %q in list", expected)
			}
		}
	})
}

func TestRegistry_Has(t *testing.T) {
	r := &Registry{providers: make(map[string]Provider)}
	r.Register(newMockProvider("kugou", "kugou_lyrics"))

	tests := []struct {
		name     string
		provider string
		expected bool
	}{
		{"Existing provider", "kugou", true},
		{"Non-existent provider", "ttml", false},
		{"Empty name", "", false},
		{"Case sensitive", "Kugou", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.Has(tt.provider)
			if result != tt.expected {
				t.Errorf("Has(%q) = %v, expected %v", tt.provider, result, tt.expected)
			}
		})
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := &Registry{providers: make(map[string]Provider)}

	// Pre-register some providers
	for i := 0; i < 5; i++ {
		r.Register(newMockProvider("provider"+string(rune('0'+i)), "prefix"))
	}

	var wg sync.WaitGroup
	done := make(chan bool)

	// Concurrent reads
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				r.List()
				r.Has("provider0")
				r.Get("provider1")
			}
		}()
	}

	// Concurrent writes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				r.Register(newMockProvider("concurrent"+string(rune('a'+id)), "prefix"))
			}
		}(i)
	}

	go func() {
		wg.Wait()
		done <- true
	}()

	select {
	case <-done:
		// Success - no race conditions detected
	}
}

func TestGetRegistry_Singleton(t *testing.T) {
	r1 := GetRegistry()
	r2 := GetRegistry()

	if r1 != r2 {
		t.Error("GetRegistry should return the same instance")
	}
}

func TestGlobalConvenienceFunctions(t *testing.T) {
	// Note: These tests use the global registry, which may have providers
	// registered by init() functions. We test behavior rather than exact state.

	t.Run("Global Has", func(t *testing.T) {
		// Has should return consistent results
		_ = Has("some_provider")
		// Just checking it doesn't panic
	})

	t.Run("Global List", func(t *testing.T) {
		names := List()
		// Should return a slice (possibly with providers from init())
		if names == nil {
			t.Error("List() should not return nil")
		}
	})

	t.Run("Global Get for non-existent", func(t *testing.T) {
		_, err := Get("definitely_not_a_real_provider_xyz123")
		if err == nil {
			t.Error("Expected error for non-existent provider")
		}
	})
}

func TestProviderInterface(t *testing.T) {
	// Verify mock provider implements interface correctly
	var _ Provider = &mockProvider{}

	p := newMockProvider("test", "test_lyrics")

	t.Run("Name returns correct value", func(t *testing.T) {
		if p.Name() != "test" {
			t.Errorf("Name() = %q, expected %q", p.Name(), "test")
		}
	})

	t.Run("CacheKeyPrefix returns correct value", func(t *testing.T) {
		if p.CacheKeyPrefix() != "test_lyrics" {
			t.Errorf("CacheKeyPrefix() = %q, expected %q", p.CacheKeyPrefix(), "test_lyrics")
		}
	})

	t.Run("FetchLyrics returns result", func(t *testing.T) {
		result, err := p.FetchLyrics(context.Background(), "song", "artist", "", 0)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Provider != "test" {
			t.Errorf("Provider = %q, expected %q", result.Provider, "test")
		}
	})
}
