package main

import (
	"context"
	"fmt"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/middleware"
	"lyrics-api-go/services/notifier"
	"lyrics-api-go/stats"
	"net/http"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
)

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getNotifierTypeName(n notifier.Notifier) string {
	switch n.(type) {
	case *notifier.EmailNotifier:
		return "email"
	case *notifier.TelegramNotifier:
		return "telegram"
	case *notifier.NtfyNotifier:
		return "ntfy"
	default:
		return "unknown"
	}
}

func setupNotifiers() []notifier.Notifier {
	var notifiers []notifier.Notifier

	if smtpHost := os.Getenv("NOTIFIER_SMTP_HOST"); smtpHost != "" {
		emailNotifier := &notifier.EmailNotifier{
			SMTPHost:     smtpHost,
			SMTPPort:     getEnvOrDefault("NOTIFIER_SMTP_PORT", "587"),
			SMTPUsername: os.Getenv("NOTIFIER_SMTP_USERNAME"),
			SMTPPassword: os.Getenv("NOTIFIER_SMTP_PASSWORD"),
			FromEmail:    os.Getenv("NOTIFIER_FROM_EMAIL"),
			ToEmail:      os.Getenv("NOTIFIER_TO_EMAIL"),
		}
		notifiers = append(notifiers, emailNotifier)
		log.Infof("%s Email notifier enabled", logcolors.LogTokenMonitor)
	}

	if botToken := os.Getenv("NOTIFIER_TELEGRAM_BOT_TOKEN"); botToken != "" {
		telegramNotifier := &notifier.TelegramNotifier{
			BotToken: botToken,
			ChatID:   os.Getenv("NOTIFIER_TELEGRAM_CHAT_ID"),
		}
		notifiers = append(notifiers, telegramNotifier)
		log.Infof("%s Telegram notifier enabled", logcolors.LogTokenMonitor)
	}

	if topic := os.Getenv("NOTIFIER_NTFY_TOPIC"); topic != "" {
		ntfyNotifier := &notifier.NtfyNotifier{
			Topic:  topic,
			Server: getEnvOrDefault("NOTIFIER_NTFY_SERVER", "https://ntfy.sh"),
		}
		notifiers = append(notifiers, ntfyNotifier)
		log.Infof("%s Ntfy.sh notifier enabled", logcolors.LogTokenMonitor)
	}

	return notifiers
}

func startTokenMonitor() {
	accounts, err := conf.GetTTMLAccounts()
	if err != nil {
		log.Warnf("%s Failed to get TTML accounts: %v", logcolors.LogTokenMonitor, err)
		return
	}

	if len(accounts) == 0 {
		log.Warnf("%s No TTML accounts configured, token monitoring disabled", logcolors.LogTokenMonitor)
		return
	}

	notifiers := setupNotifiers()

	if len(notifiers) == 0 {
		log.Infof("%s No notifiers configured, token monitoring disabled", logcolors.LogTokenMonitor)
		log.Infof("%s To enable notifications, configure at least one notifier (Email, Telegram, or Ntfy.sh)", logcolors.LogTokenMonitor)
		return
	}

	// Convert accounts to TokenInfo for the monitor
	tokens := make([]notifier.TokenInfo, len(accounts))
	for i, acc := range accounts {
		tokens[i] = notifier.TokenInfo{
			Name:        acc.Name,
			BearerToken: acc.BearerToken,
		}
	}

	log.Infof("%s Starting with %d account(s) and %d notifier(s) configured", logcolors.LogTokenMonitor, len(tokens), len(notifiers))

	monitor := notifier.NewTokenMonitor(notifier.MonitorConfig{
		Tokens:           tokens,
		WarningThreshold: 7,
		ReminderInterval: 24,
		StateFile:        "/tmp/ttml-pager.state",
		Notifiers:        notifiers,
	})

	monitor.Run(6 * time.Hour)
}

func limitMiddleware(next http.Handler, limiter *middleware.IPRateLimiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for API key to bypass rate limits
		apiKey := r.Header.Get("X-API-Key")
		if apiKey != "" && conf.Configuration.APIKey != "" && apiKey == conf.Configuration.APIKey {
			w.Header().Set("X-RateLimit-Bypass", "true")
			ctx := context.WithValue(r.Context(), rateLimitTypeKey, "bypass")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		limiters := limiter.GetLimiter(r.RemoteAddr)

		// Try normal tier first
		if limiters.Normal.Allow() {
			// Normal tier allows this request
			stats.Get().RecordRateLimit("normal")
			remainingNormal := limiters.GetNormalTokens()
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.GetNormalLimit()))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remainingNormal))
			w.Header().Set("X-RateLimit-Type", "normal")
			ctx := context.WithValue(r.Context(), rateLimitTypeKey, "normal")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Normal tier exceeded, try cached tier
		if limiters.Cached.Allow() {
			// Cached tier allows, but only for cached responses
			stats.Get().RecordRateLimit("cached")
			remainingCached := limiters.GetCachedTokens()
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.GetCachedLimit()))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remainingCached))
			w.Header().Set("X-RateLimit-Type", "cached")
			log.Debugf("%s IP %s exceeded normal tier, using cached tier", logcolors.LogRateLimit, r.RemoteAddr)
			ctx := context.WithValue(r.Context(), cacheOnlyModeKey, true)
			ctx = context.WithValue(ctx, rateLimitTypeKey, "cached")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Both tiers exceeded
		stats.Get().RecordRateLimit("exceeded")
		log.Warnf("%s IP %s exceeded both rate limit tiers", logcolors.LogRateLimit, r.RemoteAddr)
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.GetCachedLimit()))
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Type", "exceeded")
		w.Header().Set("Retry-After", "1")
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
	})
}
