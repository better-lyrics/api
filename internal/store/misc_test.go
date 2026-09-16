package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCounts_ReflectLyricsInserts(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	if err := testStore.SetLyrics(ctx, "ttml_lyrics:a b", Key{Provider: "ttml", Song: "a", Artist: "b"}, CachedLyrics{TTML: "<tt>1</tt>"}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetLyrics(ctx, "kugou_lyrics:c d", Key{Provider: "kugou", Song: "c", Artist: "d"}, CachedLyrics{TTML: "[00:01]x"}); err != nil {
		t.Fatal(err)
	}
	counts, err := testStore.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts["ttml"] != 1 || counts["kugou"] != 1 {
		t.Errorf("counts: %v", counts)
	}
}

func TestStats_RoundTrip(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	if got, err := testStore.LoadStats(ctx); err != nil || got != nil {
		t.Fatalf("empty load: got=%s err=%v", got, err)
	}
	payload := json.RawMessage(`{"requests":42,"hits":7}`)
	if err := testStore.SaveStats(ctx, payload); err != nil {
		t.Fatal(err)
	}
	got, err := testStore.LoadStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]int
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["requests"] != 42 || m["hits"] != 7 {
		t.Errorf("stats: %v", m)
	}
}

func TestStorefront_SetGet(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	if _, ok, _ := testStore.GetStorefront(ctx, "hash1"); ok {
		t.Error("expected miss")
	}
	if err := testStore.SetStorefront(ctx, "hash1", "us"); err != nil {
		t.Fatal(err)
	}
	code, ok, err := testStore.GetStorefront(ctx, "hash1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if code != "us" {
		t.Errorf("code: %q", code)
	}
	if err := testStore.SetStorefront(ctx, "hash1", "in"); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := testStore.GetStorefront(ctx, "hash1"); code != "in" {
		t.Errorf("overwrite failed: %q", code)
	}
}
