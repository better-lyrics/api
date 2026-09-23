//go:build conformance

package contracttest

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sync"
	"testing"
	"time"

	"lyrics-api-go/internal/store"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	pgOnce      sync.Once
	pgDSN       string
	pgContainer testcontainers.Container
	pgStore     *store.Store
	pgStartErr  error
)

// TestMain tears down the shared Postgres container (if any new-server test started it).
// Current-server tests never touch Postgres, so a plain `go test` run stays Docker-free
// unless a new-server test runs.
func TestMain(m *testing.M) {
	code := m.Run()
	if pgStore != nil {
		pgStore.Close()
	}
	if pgContainer != nil {
		_ = testcontainers.TerminateContainer(pgContainer)
	}
	os.Exit(code)
}

func ensurePostgres(t *testing.T) (string, *store.Store) {
	t.Helper()
	pgOnce.Do(func() {
		ctx := context.Background()
		ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("lyrics"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(60*time.Second),
			),
		)
		if err != nil {
			pgStartErr = fmt.Errorf("start postgres: %w", err)
			return
		}
		pgContainer = ctr
		dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			pgStartErr = fmt.Errorf("connection string: %w", err)
			return
		}
		pgDSN = dsn
		pgStore, err = store.New(ctx, dsn)
		if err != nil {
			pgStartErr = fmt.Errorf("open store: %w", err)
			return
		}
		if err := pgStore.Migrate(ctx); err != nil {
			pgStartErr = fmt.Errorf("migrate: %w", err)
			return
		}
	})
	if pgStartErr != nil {
		t.Fatalf("postgres setup: %v", pgStartErr)
	}
	return pgDSN, pgStore
}

func seedPostgres(t *testing.T, st *store.Store, lyrics []SeedLyrics, negatives []SeedNegative) {
	t.Helper()
	ctx := context.Background()
	if err := st.ClearAll(ctx); err != nil {
		t.Fatalf("clear: %v", err)
	}
	ttlCfg := store.NegativeTTL{DefaultDays: 7, NewSongThresholdDays: 30}

	for _, l := range lyrics {
		cacheKey := normalizedCacheKey(l.Song, l.Artist, l.Album, l.Duration)
		k := store.DeriveKey(cacheKey)
		if err := st.SetLyrics(ctx, cacheKey, k, store.CachedLyrics{
			TTML:            l.TTML,
			TrackDurationMs: l.TrackDurationMs,
			Score:           l.Score,
			Language:        l.Language,
			IsRTL:           l.IsRTL,
			Format:          k.Provider,
		}); err != nil {
			t.Fatalf("seed lyrics: %v", err)
		}
	}

	for _, n := range negatives {
		cacheKey := normalizedCacheKey(n.Song, n.Artist, n.Album, n.Duration)
		k := store.DeriveKey(cacheKey)
		if err := st.SetNegative(ctx, cacheKey, k, store.NegativeEntry{
			Reason:             n.Reason,
			ReleaseDate:        n.ReleaseDate,
			HasTimeSyncedKnown: n.HasTimeSyncedLyricsKnown,
		}, ttlCfg); err != nil {
			t.Fatalf("seed negative: %v", err)
		}
	}
}

func newServerBase(t *testing.T, env map[string]string, lyrics []SeedLyrics, negatives []SeedNegative) string {
	t.Helper()
	dsn, st := ensurePostgres(t)
	seedPostgres(t, st, lyrics, negatives)

	full := map[string]string{"DATABASE_URL": dsn}
	for k, v := range env {
		full[k] = v
	}
	bin := buildServer(t, "./cmd/api")
	return startServer(t, bin, full)
}

func TestConformanceNewSmoke(t *testing.T) {
	base := newServerBase(t, generalProfileEnv(), seedLyricsData, seedNegativeData)
	RunSpec(t, base, smokeScenarios())
}

func TestConformanceNewSeeded(t *testing.T) {
	base := newServerBase(t, generalProfileEnv(), seedLyricsData, seedNegativeData)
	RunSpec(t, base, seededGetLyricsScenarios())
}

func TestConformanceNewCORS(t *testing.T) {
	base := newServerBase(t, generalProfileEnv(), seedLyricsData, seedNegativeData)
	RunSpec(t, base, corsScenarios())
}

func TestConformanceNewAdmin(t *testing.T) {
	base := newServerBase(t, generalProfileEnv(), seedLyricsData, seedNegativeData)
	RunSpec(t, base, append(adminScenarios(), cacheGoneScenario(cacheGoneAlternatives)))
}

func TestConformanceNewAuth(t *testing.T) {
	base := newServerBase(t, authProfileEnv(), seedLyricsData, seedNegativeData)
	RunSpec(t, base, authScenarios())
}

func TestConformanceNewRateLimit(t *testing.T) {
	base := newServerBase(t, rateLimitProfileEnv(), seedLyricsData, seedNegativeData)
	RunSpec(t, base, rateLimitScenarios())
}

func TestConformanceNewSpec(t *testing.T) {
	base := newServerBase(t, generalProfileEnv(), seedLyricsData, seedNegativeData)
	RunSpec(t, base, specScenarios())
}

func TestConformanceNewOverrideProvider(t *testing.T) {
	env := authProfileEnv()
	env["FF_CACHE_ONLY_MODE"] = "true"
	base := newServerBase(t, env, seedLyricsData, seedNegativeData)

	_, st := ensurePostgres(t)
	qqKey := "qq_lyrics:bad song bad artist [bad album] [311s]"
	if err := st.SetLyrics(context.Background(), qqKey, store.DeriveKey(qqKey), store.CachedLyrics{TTML: "<QrcInfos/>", Format: "qq"}); err != nil {
		t.Fatalf("seed qq lyrics: %v", err)
	}

	RunSpec(t, base, overrideProviderScenarios())
}

func overrideProviderScenarios() []Scenario {
	key := map[string]string{"X-API-Key": "test-api-key"}
	badSong := "s=Bad%20Song&a=Bad%20Artist&al=Bad%20Album"
	return []Scenario{
		{
			Name:       "qq_seeded_entry_served_before_override",
			Path:       "/qq/getLyrics?" + badSong + "&d=311",
			WantStatus: http.StatusOK,
			WantBody:   jbody(map[string]interface{}{"lyrics": "<QrcInfos/>", "provider": "qq"}),
		},
		{
			Name:       "override_provider_no_lyrics_marks_duration_variant",
			Path:       "/override?provider=qq&no_lyrics=true&" + badSong + "&d=312",
			Headers:    key,
			WantStatus: http.StatusOK,
			WantBody: jbody(map[string]interface{}{
				"updated":   1,
				"created":   false,
				"keys":      []string{"qq_lyrics:bad song bad artist [bad album] [311s]"},
				"no_lyrics": true,
			}),
		},
		{
			Name:       "qq_marked_entry_returns_404",
			Path:       "/qq/getLyrics?" + badSong + "&d=311",
			WantStatus: http.StatusNotFound,
			WantBody:   jbody(map[string]interface{}{"error": "No lyrics available for this track"}),
		},
		{
			Name:       "override_provider_no_lyrics_creates_marker_when_uncached",
			Path:       "/override?provider=qq&no_lyrics=true&s=Never%20Fetched&a=Tester",
			Headers:    key,
			WantStatus: http.StatusOK,
			WantBody: jbody(map[string]interface{}{
				"updated":   1,
				"created":   true,
				"keys":      []string{"qq_lyrics:never fetched tester"},
				"no_lyrics": true,
			}),
		},
		{
			Name:       "qq_created_marker_blocks_fetch",
			Path:       "/qq/getLyrics?s=Never%20Fetched&a=Tester",
			WantStatus: http.StatusNotFound,
			WantBody:   jbody(map[string]interface{}{"error": "No lyrics available for this track"}),
		},
		{
			Name:       "override_provider_leaves_default_cache_untouched",
			Path:       "/override?provider=qq&no_lyrics=true&s=Conformance%20Hit&a=Tester",
			Headers:    key,
			WantStatus: http.StatusOK,
		},
		{
			Name:       "default_entry_still_served_after_provider_override",
			Path:       "/getLyrics?s=Conformance%20Hit&a=Tester",
			WantStatus: http.StatusOK,
			WantBody:   jbody(map[string]interface{}{"ttml": "<tt>HIT</tt>"}),
		},
		{
			Name:          "override_unknown_provider_400",
			Path:          "/override?provider=nope&no_lyrics=true&s=Bad%20Song&a=Bad%20Artist",
			Headers:       key,
			WantStatus:    http.StatusBadRequest,
			WantBodyRegex: regexp.MustCompile(`"error":"invalid provider: nope"`),
		},
		{
			Name:       "override_provider_rejects_track_id",
			Path:       "/override?provider=qq&id=123&s=Bad%20Song&a=Bad%20Artist",
			Headers:    key,
			WantStatus: http.StatusBadRequest,
			WantBody:   jbody(map[string]interface{}{"error": "provider overrides only support no_lyrics=true or dry_run=true"}),
		},
	}
}
