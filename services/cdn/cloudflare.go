package cdn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"lyrics-api-go/config"
	"lyrics-api-go/logcolors"

	log "github.com/sirupsen/logrus"
)

const (
	maxTagsPerRequest = 100
	maxPending        = 1000
)

// Purger batches Cache-Tag purges so write bursts stay under Cloudflare's account-wide purge rate limit.
type Purger struct {
	zoneID  string
	token   string
	baseURL string
	client  *http.Client

	mu      sync.Mutex
	pending []string
	queued  map[string]bool
}

func New(zoneID, token, baseURL string) *Purger {
	return &Purger{
		zoneID:  zoneID,
		token:   token,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 10 * time.Second},
		queued:  make(map[string]bool),
	}
}

func (p *Purger) Enqueue(tags ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, tag := range tags {
		if tag == "" || p.queued[tag] {
			continue
		}
		if len(p.pending) >= maxPending {
			log.Warnf("%s Purge queue full, dropping tag %s", logcolors.LogCDN, tag)
			continue
		}
		p.queued[tag] = true
		p.pending = append(p.pending, tag)
	}
}

func (p *Purger) Pending() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending)
}

// Flush purges every pending tag. Tags from a failed request stay queued for the next flush.
func (p *Purger) Flush(ctx context.Context) error {
	for {
		batch := p.take()
		if len(batch) == 0 {
			return nil
		}
		if err := p.purge(ctx, batch); err != nil {
			p.Enqueue(batch...)
			return err
		}
		log.Infof("%s Purged %d cache tags", logcolors.LogCDN, len(batch))
	}
}

func (p *Purger) take() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := min(len(p.pending), maxTagsPerRequest)
	batch := p.pending[:n:n]
	p.pending = p.pending[n:]
	for _, tag := range batch {
		delete(p.queued, tag)
	}
	return batch
}

func (p *Purger) purge(ctx context.Context, tags []string) error {
	body, err := json.Marshal(map[string][]string{"tags": tags})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/zones/%s/purge_cache", p.baseURL, p.zoneID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("purge returned %d: %s", resp.StatusCode, msg)
	}
	return nil
}

func (p *Purger) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.Flush(ctx); err != nil {
				log.Warnf("%s Purge failed, %d tags queued for retry: %v", logcolors.LogCDN, p.Pending(), err)
			}
		}
	}
}

var active atomic.Pointer[Purger]

// Start enables Purge. It is a no-op when the Cloudflare zone or token is not configured.
func Start(ctx context.Context, cfg config.Config) {
	conf := cfg.Configuration
	if conf.CloudflareZoneID == "" || conf.CloudflareAPIToken == "" {
		log.Infof("%s Cloudflare purge disabled (CLOUDFLARE_ZONE_ID or CLOUDFLARE_API_TOKEN unset)", logcolors.LogCDN)
		return
	}
	p := New(conf.CloudflareZoneID, conf.CloudflareAPIToken, conf.CloudflareAPIBaseURL)
	active.Store(p)
	go p.run(ctx, time.Duration(max(conf.CloudflarePurgeIntervalSeconds, 1))*time.Second)
}

// Purge queues cache tags for the next flush. Safe to call when purging is disabled.
func Purge(tags ...string) {
	if p := active.Load(); p != nil {
		p.Enqueue(tags...)
	}
}
