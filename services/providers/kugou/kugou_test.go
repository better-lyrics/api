package kugou

import (
	"testing"

	"lyrics-api-go/services/providers"
)

func TestKugouProvider_Name(t *testing.T) {
	p := NewProvider()

	if p.Name() != ProviderName {
		t.Errorf("Name() = %q, expected %q", p.Name(), ProviderName)
	}

	if p.Name() != "kugou" {
		t.Errorf("Name() = %q, expected %q", p.Name(), "kugou")
	}
}

func TestKugouProvider_CacheKeyPrefix(t *testing.T) {
	p := NewProvider()

	if p.CacheKeyPrefix() != CachePrefix {
		t.Errorf("CacheKeyPrefix() = %q, expected %q", p.CacheKeyPrefix(), CachePrefix)
	}

	if p.CacheKeyPrefix() != "kugou_lyrics" {
		t.Errorf("CacheKeyPrefix() = %q, expected %q", p.CacheKeyPrefix(), "kugou_lyrics")
	}
}

func TestNewProvider(t *testing.T) {
	p := NewProvider()

	if p == nil {
		t.Fatal("NewProvider() returned nil")
	}

	// Verify it's a *KugouProvider
	_, ok := interface{}(p).(*KugouProvider)
	if !ok {
		t.Error("NewProvider() should return *KugouProvider")
	}
}

func TestKugouProvider_ImplementsInterface(t *testing.T) {
	// Verify that KugouProvider implements providers.Provider
	var _ providers.Provider = &KugouProvider{}
	var _ providers.Provider = NewProvider()
}

func TestConstants(t *testing.T) {
	t.Run("ProviderName constant", func(t *testing.T) {
		if ProviderName != "kugou" {
			t.Errorf("ProviderName = %q, expected %q", ProviderName, "kugou")
		}
	})

	t.Run("CachePrefix constant", func(t *testing.T) {
		if CachePrefix != "kugou_lyrics" {
			t.Errorf("CachePrefix = %q, expected %q", CachePrefix, "kugou_lyrics")
		}
	})
}
