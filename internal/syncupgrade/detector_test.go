package syncupgrade

import (
	"fmt"
	"testing"
	"time"

	"lyrics-api-go/internal/store"
	ttml "lyrics-api-go/services/providers/ttml"
)

const (
	ttmlWord = `<tt itunes:timing="Word"><body><div><p><span begin="0s">hi</span></p></div></body></tt>`
	ttmlLine = `<tt itunes:timing="Line"><body><div><p begin="0s">hi</p></div></body></tt>`
	ttmlNone = `<tt itunes:timing="None"><body><div><p>hi</p></div></body></tt>`
)

func fetchOK(body, etag string, notModified bool) condFetch {
	return func(trackID, ifNoneMatch string) (string, string, bool, error) {
		return body, etag, notModified, nil
	}
}

func fetchErr() condFetch {
	return func(trackID, ifNoneMatch string) (string, string, bool, error) {
		return "", "", false, fmt.Errorf("boom")
	}
}

func fetchNever(t *testing.T) condFetch {
	return func(trackID, ifNoneMatch string) (string, string, bool, error) {
		t.Helper()
		t.Fatal("fetch must not be called")
		return "", "", false, nil
	}
}

func TestDecideCandidate(t *testing.T) {
	t.Run("regression: word content with empty column is never downgraded and skips fetch", func(t *testing.T) {
		cand := store.SyncUpgradeCandidate{AppleTrackID: "1", TimingType: "", TTML: ttmlWord, AppleETag: `"e"`}
		act, err := decideCandidate(cand, fetchNever(t), ttml.TimingType)
		if err != nil || act.Upgraded || !act.Bump || act.NewTiming != "word" {
			t.Fatalf("word/empty: %+v err=%v", act, err)
		}
	})

	t.Run("regression: non-upgrade relabels to the stored content timing, not the fetched one", func(t *testing.T) {
		cand := store.SyncUpgradeCandidate{AppleTrackID: "1", TimingType: "", TTML: ttmlLine, AppleETag: `"e"`}
		act, err := decideCandidate(cand, fetchOK(ttmlNone, `"e2"`, false), ttml.TimingType)
		if err != nil || act.Upgraded || !act.Bump || act.NewTiming != "line" {
			t.Fatalf("line vs none: %+v err=%v", act, err)
		}
	})

	t.Run("upgrade none to word rewrites lyrics", func(t *testing.T) {
		cand := store.SyncUpgradeCandidate{AppleTrackID: "1", TimingType: "none", TTML: ttmlNone, AppleETag: `"e"`}
		act, err := decideCandidate(cand, fetchOK(ttmlWord, `"e2"`, false), ttml.TimingType)
		if err != nil || !act.Upgraded || act.NewTTML != ttmlWord || act.NewTiming != "word" || act.NewETag != `"e2"` {
			t.Fatalf("none to word: %+v err=%v", act, err)
		}
	})

	t.Run("304 not-modified bumps and keeps the old etag", func(t *testing.T) {
		cand := store.SyncUpgradeCandidate{AppleTrackID: "1", TimingType: "line", TTML: ttmlLine, AppleETag: `"e"`}
		act, err := decideCandidate(cand, fetchOK("", "", true), ttml.TimingType)
		if err != nil || act.Upgraded || !act.Bump || act.NewTiming != "line" || act.NewETag != `"e"` {
			t.Fatalf("304: %+v err=%v", act, err)
		}
	})

	t.Run("regression: fetch error still bumps so the row cannot starve the queue", func(t *testing.T) {
		cand := store.SyncUpgradeCandidate{AppleTrackID: "1", TimingType: "line", TTML: ttmlLine, AppleETag: `"e"`}
		act, err := decideCandidate(cand, fetchErr(), ttml.TimingType)
		if err == nil {
			t.Fatal("fetch error must propagate for logging")
		}
		if act.Upgraded || !act.Bump || act.NewTiming != "line" || act.NewETag != `"e"` {
			t.Fatalf("error bump: %+v", act)
		}
	})
}

func TestSyncUpgradeInterval(t *testing.T) {
	cases := map[int]time.Duration{
		360: 360 * time.Minute,
		15:  15 * time.Minute,
		0:   360 * time.Minute,
		-5:  360 * time.Minute,
	}
	for mins, want := range cases {
		if got := syncUpgradeInterval(mins); got != want {
			t.Errorf("syncUpgradeInterval(%d) = %v, want %v", mins, got, want)
		}
	}
}
