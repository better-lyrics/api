package httpapi

import (
	"fmt"
	"net/http"
	"strconv"

	"lyrics-api-go/logcolors"

	log "github.com/sirupsen/logrus"
)

const (
	biniLinkBackfillDefaultLimit = 5000
	biniLinkBackfillMaxLimit     = 10000
)

func (s *Server) biniLinkBackfill(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	q := r.URL.Query()
	limit := biniLinkBackfillDefaultLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > biniLinkBackfillMaxLimit {
			respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
				"error": fmt.Sprintf("limit must be between 1 and %d", biniLinkBackfillMaxLimit),
			})
			return
		}
		limit = n
	}

	seeded, next, err := s.store.SeedBiniLinkBackfill(r.Context(), q.Get("after"), limit, s.cfg.Configuration.DurationMatchDeltaMs)
	if err != nil {
		log.Errorf("%s BiniLink backfill after %q failed: %v", logcolors.LogBini, q.Get("after"), err)
		respond(w, r).Error(http.StatusInternalServerError, map[string]interface{}{
			"error": "backfill failed: " + err.Error(),
		})
		return
	}

	respond(w, r).JSON(map[string]interface{}{
		"seeded":     seeded,
		"next_after": next,
		"done":       next == "",
	})
}
