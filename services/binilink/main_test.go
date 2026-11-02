package binilink

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"lyrics-api-go/internal/store"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var testStore *store.Store

func TestMain(m *testing.M) {
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
		fmt.Fprintf(os.Stderr, "start postgres container: %v\n", err)
		os.Exit(1)
	}

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
		_ = testcontainers.TerminateContainer(ctr)
		os.Exit(1)
	}

	testStore, err = store.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open store: %v\n", err)
		_ = testcontainers.TerminateContainer(ctr)
		os.Exit(1)
	}
	if err := testStore.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		_ = testcontainers.TerminateContainer(ctr)
		os.Exit(1)
	}

	code := m.Run()

	testStore.Close()
	_ = testcontainers.TerminateContainer(ctr)
	os.Exit(code)
}

func resetBiniLink(t *testing.T) {
	t.Helper()
	if _, err := testStore.Pool().Exec(context.Background(),
		`TRUNCATE binilink_links, video_map, song_metadata`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func statusOf(t *testing.T, videoID string) string {
	t.Helper()
	var status string
	if err := testStore.Pool().QueryRow(context.Background(),
		`SELECT status FROM binilink_links WHERE video_id = $1`, videoID).Scan(&status); err != nil {
		t.Fatalf("status of %s: %v", videoID, err)
	}
	return status
}

func attemptsOf(t *testing.T, videoID string) int {
	t.Helper()
	var attempts int
	if err := testStore.Pool().QueryRow(context.Background(),
		`SELECT attempts FROM binilink_links WHERE video_id = $1`, videoID).Scan(&attempts); err != nil {
		t.Fatalf("attempts of %s: %v", videoID, err)
	}
	return attempts
}

func nextAttemptOf(t *testing.T, videoID string) time.Time {
	t.Helper()
	var next time.Time
	if err := testStore.Pool().QueryRow(context.Background(),
		`SELECT next_attempt FROM binilink_links WHERE video_id = $1`, videoID).Scan(&next); err != nil {
		t.Fatalf("next_attempt of %s: %v", videoID, err)
	}
	return next
}
