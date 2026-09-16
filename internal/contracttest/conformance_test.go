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

func TestConformanceCurrentSeeded(t *testing.T) {
	bin := buildServer(t, ".")
	dbPath := filepath.Join(t.TempDir(), "seeded.db")
	if err := seedCacheDB(dbPath, seedLyricsData, seedNegativeData); err != nil {
		t.Fatalf("seed: %v", err)
	}
	env := generalProfileEnv()
	env["CACHE_DB_PATH"] = dbPath
	base := startServer(t, bin, env)
	Run(t, base, seededGetLyricsScenarios())
}
