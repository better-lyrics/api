package legacy

import (
	"testing"

	"lyrics-api-go/services/providers"
)

func TestLegacyProvider_Name(t *testing.T) {
	p := NewProvider()

	if p.Name() != ProviderName {
		t.Errorf("Name() = %q, expected %q", p.Name(), ProviderName)
	}

	if p.Name() != "legacy" {
		t.Errorf("Name() = %q, expected %q", p.Name(), "legacy")
	}
}

func TestLegacyProvider_CacheKeyPrefix(t *testing.T) {
	p := NewProvider()

	if p.CacheKeyPrefix() != CachePrefix {
		t.Errorf("CacheKeyPrefix() = %q, expected %q", p.CacheKeyPrefix(), CachePrefix)
	}

	if p.CacheKeyPrefix() != "legacy_lyrics" {
		t.Errorf("CacheKeyPrefix() = %q, expected %q", p.CacheKeyPrefix(), "legacy_lyrics")
	}
}

func TestNewProvider(t *testing.T) {
	p := NewProvider()

	if p == nil {
		t.Fatal("NewProvider() returned nil")
	}

	// Verify it's a *LegacyProvider
	_, ok := interface{}(p).(*LegacyProvider)
	if !ok {
		t.Error("NewProvider() should return *LegacyProvider")
	}
}

func TestLegacyProvider_ImplementsInterface(t *testing.T) {
	// Verify that LegacyProvider implements providers.Provider
	var _ providers.Provider = &LegacyProvider{}
	var _ providers.Provider = NewProvider()
}

func TestConstants(t *testing.T) {
	t.Run("ProviderName constant", func(t *testing.T) {
		if ProviderName != "legacy" {
			t.Errorf("ProviderName = %q, expected %q", ProviderName, "legacy")
		}
	})

	t.Run("CachePrefix constant", func(t *testing.T) {
		if CachePrefix != "legacy_lyrics" {
			t.Errorf("CachePrefix = %q, expected %q", CachePrefix, "legacy_lyrics")
		}
	})
}

func TestLegacyLineStructure(t *testing.T) {
	// Test that LegacyLine has expected fields
	ll := LegacyLine{
		StartTimeMs: "1000",
		DurationMs:  "5000",
		Words:       "Hello world",
		Syllables:   []string{"Hello", "world"},
		EndTimeMs:   "6000",
	}

	if ll.StartTimeMs != "1000" {
		t.Errorf("StartTimeMs = %q, expected %q", ll.StartTimeMs, "1000")
	}
	if ll.DurationMs != "5000" {
		t.Errorf("DurationMs = %q, expected %q", ll.DurationMs, "5000")
	}
	if ll.Words != "Hello world" {
		t.Errorf("Words = %q, expected %q", ll.Words, "Hello world")
	}
	if len(ll.Syllables) != 2 {
		t.Errorf("Syllables len = %d, expected %d", len(ll.Syllables), 2)
	}
	if ll.EndTimeMs != "6000" {
		t.Errorf("EndTimeMs = %q, expected %q", ll.EndTimeMs, "6000")
	}
}

func TestLyricsDataStructure(t *testing.T) {
	ld := LyricsData{
		SyncType: "LINE_SYNCED",
		Lines: []LegacyLine{
			{Words: "Line 1"},
			{Words: "Line 2"},
		},
		IsRtlLanguage: false,
		Language:      "en",
	}

	if ld.SyncType != "LINE_SYNCED" {
		t.Errorf("SyncType = %q, expected %q", ld.SyncType, "LINE_SYNCED")
	}
	if len(ld.Lines) != 2 {
		t.Errorf("Lines count = %d, expected %d", len(ld.Lines), 2)
	}
	if ld.IsRtlLanguage != false {
		t.Error("IsRtlLanguage should be false")
	}
	if ld.Language != "en" {
		t.Errorf("Language = %q, expected %q", ld.Language, "en")
	}
}

func TestTrackItemStructure(t *testing.T) {
	ti := TrackItem{
		ID:         "spotify:track:123",
		Name:       "Test Song",
		DurationMs: 230000,
	}
	ti.Album.Name = "Test Album"
	ti.Artists = append(ti.Artists, struct {
		Name string `json:"name"`
	}{Name: "Test Artist"})

	if ti.ID != "spotify:track:123" {
		t.Errorf("ID = %q, expected %q", ti.ID, "spotify:track:123")
	}
	if ti.Name != "Test Song" {
		t.Errorf("Name = %q, expected %q", ti.Name, "Test Song")
	}
	if ti.DurationMs != 230000 {
		t.Errorf("DurationMs = %d, expected %d", ti.DurationMs, 230000)
	}
	if ti.Album.Name != "Test Album" {
		t.Errorf("Album.Name = %q, expected %q", ti.Album.Name, "Test Album")
	}
	if len(ti.Artists) != 1 || ti.Artists[0].Name != "Test Artist" {
		t.Error("Artists not set correctly")
	}
}

func TestTokenDataStructure(t *testing.T) {
	td := TokenData{
		AccessToken:                      "test_token_123",
		AccessTokenExpirationTimestampMs: 1700000000000,
	}

	if td.AccessToken != "test_token_123" {
		t.Errorf("AccessToken = %q, expected %q", td.AccessToken, "test_token_123")
	}
	if td.AccessTokenExpirationTimestampMs != 1700000000000 {
		t.Errorf("AccessTokenExpirationTimestampMs = %d, expected %d",
			td.AccessTokenExpirationTimestampMs, int64(1700000000000))
	}
}

func TestOAuthTokenResponseStructure(t *testing.T) {
	otr := OAuthTokenResponse{
		AccessToken: "oauth_token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
	}

	if otr.AccessToken != "oauth_token" {
		t.Errorf("AccessToken = %q, expected %q", otr.AccessToken, "oauth_token")
	}
	if otr.TokenType != "Bearer" {
		t.Errorf("TokenType = %q, expected %q", otr.TokenType, "Bearer")
	}
	if otr.ExpiresIn != 3600 {
		t.Errorf("ExpiresIn = %d, expected %d", otr.ExpiresIn, 3600)
	}
}
