package store

import (
	"context"
	"testing"
)

func TestNew_PingsDatabase(t *testing.T) {
	ctx := context.Background()
	s, err := New(ctx, testDSN)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	if err := s.pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestNew_BadDSN(t *testing.T) {
	ctx := context.Background()
	if _, err := New(ctx, "postgres://bad:bad@127.0.0.1:1/nope?connect_timeout=1"); err == nil {
		t.Fatal("expected error for unreachable database, got nil")
	}
}
