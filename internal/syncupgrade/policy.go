package syncupgrade

import "time"

func syncRank(timing string) int {
	switch timing {
	case "word":
		return 3
	case "line":
		return 2
	case "none":
		return 1
	default:
		return 0
	}
}

func isUpgrade(oldTiming, newTiming string) bool {
	return syncRank(newTiming) > syncRank(oldTiming)
}

func checkDue(releaseDate string, lastChecked *time.Time, now time.Time, windowDays int) bool {
	rd, err := time.Parse("2006-01-02", releaseDate)
	if err != nil {
		return false
	}
	daysSinceRelease := int(now.Sub(rd).Hours() / 24)
	if daysSinceRelease > windowDays {
		return false
	}

	var interval time.Duration
	switch {
	case daysSinceRelease <= 3:
		interval = 12 * time.Hour
	case daysSinceRelease <= 7:
		interval = 24 * time.Hour
	case daysSinceRelease <= 14:
		interval = 48 * time.Hour
	default:
		interval = 72 * time.Hour
	}

	if lastChecked == nil {
		return true
	}
	return now.Sub(*lastChecked) >= interval
}
