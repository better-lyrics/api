package syncupgrade

import (
	"fmt"
	"testing"

	"lyrics-api-go/internal/store"
)

func fetchOK(body, etag string, notModified bool) condFetch {
	return func(trackID, ifNoneMatch string) (string, string, bool, error) {
		return body, etag, notModified, nil
	}
}

func constTiming(v string) func(string) string {
	return func(string) string { return v }
}

func TestDecideCandidate(t *testing.T) {
	line := store.SyncUpgradeCandidate{AppleTrackID: "1", TimingType: "line", AppleETag: `"e"`}
	word := store.SyncUpgradeCandidate{AppleTrackID: "1", TimingType: "word", AppleETag: `"e"`}

	act, err := decideCandidate(line, fetchOK("", `"e"`, true), constTiming("word"))
	if err != nil || act.Upgraded || act.NewTTML != "" {
		t.Fatalf("304 not-modified: %+v err=%v", act, err)
	}

	act, err = decideCandidate(line, fetchOK("<w/>", `"e2"`, false), constTiming("word"))
	if err != nil || !act.Upgraded || act.NewTTML != "<w/>" || act.NewTiming != "word" || act.NewETag != `"e2"` {
		t.Fatalf("line to word upgrade: %+v err=%v", act, err)
	}

	act, err = decideCandidate(line, fetchOK("<l/>", `"e2"`, false), constTiming("line"))
	if err != nil || act.Upgraded {
		t.Fatalf("line to line no-upgrade: %+v err=%v", act, err)
	}

	act, err = decideCandidate(word, fetchOK("<l/>", `"e2"`, false), constTiming("line"))
	if err != nil || act.Upgraded {
		t.Fatalf("word to line downgrade guard: %+v err=%v", act, err)
	}

	if _, err := decideCandidate(line,
		func(trackID, ifNoneMatch string) (string, string, bool, error) {
			return "", "", false, fmt.Errorf("boom")
		},
		constTiming("word")); err == nil {
		t.Fatal("fetch error must propagate")
	}
}
