package qq

import (
	"testing"

	"lyrics-api-go/services/providers"
)

func TestQQProvider_Name(t *testing.T) {
	p := NewProvider()

	if p.Name() != ProviderName {
		t.Errorf("Name() = %q, expected %q", p.Name(), ProviderName)
	}

	if p.Name() != "qq" {
		t.Errorf("Name() = %q, expected %q", p.Name(), "qq")
	}
}

func TestQQProvider_CacheKeyPrefix(t *testing.T) {
	p := NewProvider()

	if p.CacheKeyPrefix() != CachePrefix {
		t.Errorf("CacheKeyPrefix() = %q, expected %q", p.CacheKeyPrefix(), CachePrefix)
	}

	if p.CacheKeyPrefix() != "qq_lyrics" {
		t.Errorf("CacheKeyPrefix() = %q, expected %q", p.CacheKeyPrefix(), "qq_lyrics")
	}
}

func TestNewProvider(t *testing.T) {
	p := NewProvider()

	if p == nil {
		t.Fatal("NewProvider() returned nil")
	}

	_, ok := interface{}(p).(*QQProvider)
	if !ok {
		t.Error("NewProvider() should return *QQProvider")
	}
}

func TestQQProvider_ImplementsInterface(t *testing.T) {
	var _ providers.Provider = &QQProvider{}
	var _ providers.Provider = NewProvider()
}

func TestConstants(t *testing.T) {
	t.Run("ProviderName constant", func(t *testing.T) {
		if ProviderName != "qq" {
			t.Errorf("ProviderName = %q, expected %q", ProviderName, "qq")
		}
	})

	t.Run("CachePrefix constant", func(t *testing.T) {
		if CachePrefix != "qq_lyrics" {
			t.Errorf("CachePrefix = %q, expected %q", CachePrefix, "qq_lyrics")
		}
	})
}
