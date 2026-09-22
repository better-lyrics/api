package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"lyrics-api-go/internal/store"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/notifier"
	"lyrics-api-go/stats"

	log "github.com/sirupsen/logrus"
)

func loadStats(ctx context.Context, st *store.Store) {
	raw, err := st.LoadStats(ctx)
	if err != nil {
		log.Warnf("%s Failed to load persisted stats: %v", logcolors.LogStats, err)
		return
	}
	if raw == nil {
		return
	}
	var p stats.PersistedStats
	if err := json.Unmarshal(raw, &p); err != nil {
		log.Warnf("%s Failed to parse persisted stats: %v", logcolors.LogStats, err)
		return
	}
	stats.Get().Restore(p)
	log.Infof("%s Loaded persisted stats (total requests: %d)", logcolors.LogStats, p.TotalRequests)
}

func startStatsAutosave(ctx context.Context, st *store.Store, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			data, err := json.Marshal(stats.Get().Serialize())
			if err != nil {
				log.Warnf("%s Failed to marshal stats: %v", logcolors.LogStats, err)
				continue
			}
			if err := st.SaveStats(ctx, data); err != nil {
				log.Warnf("%s Failed to save stats: %v", logcolors.LogStats, err)
			}
		}
	}()
	log.Infof("%s Started stats auto-save with interval %v", logcolors.LogStats, interval)
}

func setupAlertNotifiers() []notifier.Notifier {
	var notifiers []notifier.Notifier

	if smtpHost := os.Getenv("NOTIFIER_SMTP_HOST"); smtpHost != "" {
		notifiers = append(notifiers, &notifier.EmailNotifier{
			SMTPHost:     smtpHost,
			SMTPPort:     getEnvOrDefault("NOTIFIER_SMTP_PORT", "587"),
			SMTPUsername: os.Getenv("NOTIFIER_SMTP_USERNAME"),
			SMTPPassword: os.Getenv("NOTIFIER_SMTP_PASSWORD"),
			FromEmail:    os.Getenv("NOTIFIER_FROM_EMAIL"),
			ToEmail:      os.Getenv("NOTIFIER_TO_EMAIL"),
		})
	}
	if botToken := os.Getenv("NOTIFIER_TELEGRAM_BOT_TOKEN"); botToken != "" {
		notifiers = append(notifiers, &notifier.TelegramNotifier{
			BotToken: botToken,
			ChatID:   os.Getenv("NOTIFIER_TELEGRAM_CHAT_ID"),
		})
	}
	if topic := os.Getenv("NOTIFIER_NTFY_TOPIC"); topic != "" {
		notifiers = append(notifiers, &notifier.NtfyNotifier{
			Topic:  topic,
			Server: getEnvOrDefault("NOTIFIER_NTFY_SERVER", "https://ntfy.sh"),
		})
	}
	return notifiers
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
