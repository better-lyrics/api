//go:build conformance

package contracttest

import (
	"path/filepath"
	"testing"
)

func TestConformanceCurrent(t *testing.T) {
	bin := buildServer(t, ".")
	base := startServer(t, bin, generalProfileEnv())
	Run(t, base, smokeScenarios())
}

func seededDBPath(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "seeded.db")
	if err := seedCacheDB(dbPath, seedLyricsData, seedNegativeData); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return dbPath
}

func TestConformanceCurrentSeeded(t *testing.T) {
	bin := buildServer(t, ".")
	env := generalProfileEnv()
	env["CACHE_DB_PATH"] = seededDBPath(t)
	base := startServer(t, bin, env)
	Run(t, base, seededGetLyricsScenarios())
}

func TestConformanceCurrentCORS(t *testing.T) {
	bin := buildServer(t, ".")
	base := startServer(t, bin, generalProfileEnv())
	Run(t, base, corsScenarios())
}

func TestConformanceCurrentAdmin(t *testing.T) {
	bin := buildServer(t, ".")
	base := startServer(t, bin, generalProfileEnv())
	Run(t, base, adminScenarios())
}

func TestConformanceCurrentAuth(t *testing.T) {
	bin := buildServer(t, ".")
	env := authProfileEnv()
	env["CACHE_DB_PATH"] = seededDBPath(t)
	base := startServer(t, bin, env)
	Run(t, base, authScenarios())
}

func TestConformanceCurrentRateLimit(t *testing.T) {
	bin := buildServer(t, ".")
	env := rateLimitProfileEnv()
	env["CACHE_DB_PATH"] = seededDBPath(t)
	base := startServer(t, bin, env)
	Run(t, base, rateLimitScenarios())
}
