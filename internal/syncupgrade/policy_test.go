package syncupgrade

import (
	"testing"
	"time"
)

func TestSyncRank(t *testing.T) {
	if !(syncRank("word") > syncRank("line") && syncRank("line") > syncRank("none") && syncRank("none") >= syncRank("")) {
		t.Fatal("rank order wrong")
	}
}

func TestIsUpgrade(t *testing.T) {
	if !isUpgrade("line", "word") || isUpgrade("word", "line") || isUpgrade("line", "line") {
		t.Fatal("upgrade logic wrong")
	}
}

func TestCheckDue(t *testing.T) {
	now := time.Now()
	rel := now.AddDate(0, 0, -2).Format("2006-01-02")
	if !checkDue(rel, nil, now, 42) {
		t.Fatal("never-checked recent release must be due")
	}
	recent := now.Add(-1 * time.Hour)
	if checkDue(rel, &recent, now, 42) {
		t.Fatal("checked 1h ago on a 12h cadence must not be due")
	}
	old := now.AddDate(0, 0, -100).Format("2006-01-02")
	if checkDue(old, nil, now, 42) {
		t.Fatal("release past window must never be due")
	}
}
