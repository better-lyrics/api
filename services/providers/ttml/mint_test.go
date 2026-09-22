package ttml

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func resetMintedToken() {
	mintedMu.Lock()
	minted = mintedBearer{}
	mintedExpiry = time.Time{}
	mintedMu.Unlock()
}

func TestClampMintTTL(t *testing.T) {
	tests := []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{"typical 120s", 120, 120 * time.Second},
		{"below floor", 30, mintMinTTL},
		{"zero", 0, mintMinTTL},
		{"negative", -50, mintMinTTL},
		{"at floor", 60, mintMinTTL},
		{"at ceiling", 3600, mintMaxTTL},
		{"above ceiling", 100000, mintMaxTTL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampMintTTL(tt.seconds); got != tt.want {
				t.Errorf("clampMintTTL(%d) = %v, want %v", tt.seconds, got, tt.want)
			}
		})
	}
}

func TestGetMintedBearer_MintsAndCaches(t *testing.T) {
	resetMintedToken()
	defer resetMintedToken()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent sent to minter")
		}
		fmt.Fprint(w, `{"storefront_id":"143478-2,31","token":"minted-abc","token_type":"Bearer","cache_ttl_seconds":120}`)
	}))
	defer srv.Close()

	old := mintTokenURL
	mintTokenURL = srv.URL
	defer func() { mintTokenURL = old }()

	mb, err := getMintedBearer()
	if err != nil {
		t.Fatalf("first mint: %v", err)
	}
	if mb.token != "minted-abc" {
		t.Errorf("token = %q, want %q", mb.token, "minted-abc")
	}

	// Second call within TTL must be served from cache, no extra mint.
	mb2, err := getMintedBearer()
	if err != nil {
		t.Fatalf("second mint: %v", err)
	}
	if mb2.token != "minted-abc" {
		t.Errorf("cached token = %q, want %q", mb2.token, "minted-abc")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("minter called %d times, want 1 (second call should hit cache)", n)
	}
}

func TestGetMintedBearer_RemintsAfterExpiry(t *testing.T) {
	resetMintedToken()
	defer resetMintedToken()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		fmt.Fprintf(w, `{"storefront_id":"143478-2,31","token":"minted-%d","token_type":"Bearer","cache_ttl_seconds":120}`, n)
	}))
	defer srv.Close()

	old := mintTokenURL
	mintTokenURL = srv.URL
	defer func() { mintTokenURL = old }()

	if _, err := getMintedBearer(); err != nil {
		t.Fatalf("first mint: %v", err)
	}

	// Force expiry (short TTL is the whole reason this lane needs its own refresh).
	mintedMu.Lock()
	mintedExpiry = time.Now().Add(-time.Second)
	mintedMu.Unlock()

	mb, err := getMintedBearer()
	if err != nil {
		t.Fatalf("re-mint: %v", err)
	}
	if mb.token != "minted-2" {
		t.Errorf("re-minted token = %q, want %q", mb.token, "minted-2")
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("minter called %d times, want 2", n)
	}
}

func TestGetMintedBearer_Errors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"non-200", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }},
		{"missing token", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"cache_ttl_seconds":120}`) }},
		{"bad json", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `not json`) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetMintedToken()
			defer resetMintedToken()

			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			old := mintTokenURL
			mintTokenURL = srv.URL
			defer func() { mintTokenURL = old }()

			if _, err := getMintedBearer(); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestGetMintedBearer_StorefrontBinding(t *testing.T) {
	tests := []struct {
		name           string
		storefrontID   string
		wantErr        bool
		wantStorefront string
	}{
		{name: "known storefront maps to catalog code", storefrontID: "143478-2,31", wantStorefront: "pl"},
		{name: "bare numeric without suffix", storefrontID: "143478", wantStorefront: "pl"},
		{name: "vietnam is a distinct id", storefrontID: "143471-2,31", wantStorefront: "vn"},
		{name: "unmapped numeric fails closed", storefrontID: "999999-2,31", wantErr: true},
		{name: "absent storefront fails closed", storefrontID: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetMintedToken()
			defer resetMintedToken()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"storefront_id":%q,"token":"minted-tok","token_type":"Bearer","cache_ttl_seconds":120}`, tt.storefrontID)
			}))
			defer srv.Close()

			old := mintTokenURL
			mintTokenURL = srv.URL
			defer func() { mintTokenURL = old }()

			mb, err := getMintedBearer()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for storefront_id %q, got storefront %q", tt.storefrontID, mb.storefront)
				}
				if mb.token != "" {
					t.Errorf("token leaked on failure: %q", mb.token)
				}
				mintedMu.RLock()
				cached := minted.token
				mintedMu.RUnlock()
				if cached != "" {
					t.Errorf("unusable bearer was cached: %q", cached)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mb.storefront != tt.wantStorefront {
				t.Errorf("storefront = %q, want %q", mb.storefront, tt.wantStorefront)
			}
			if mb.storefrontID != tt.storefrontID {
				t.Errorf("storefrontID = %q, want verbatim %q", mb.storefrontID, tt.storefrontID)
			}
		})
	}
}
