package httpapi

import (
	"os"

	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/notifier"

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
		notifiers = append(notifiers, &notifier.EmailNotifier{
			SMTPHost:     smtpHost,
			SMTPPort:     getEnvOrDefault("NOTIFIER_SMTP_PORT", "587"),
			SMTPUsername: os.Getenv("NOTIFIER_SMTP_USERNAME"),
			SMTPPassword: os.Getenv("NOTIFIER_SMTP_PASSWORD"),
			FromEmail:    os.Getenv("NOTIFIER_FROM_EMAIL"),
			ToEmail:      os.Getenv("NOTIFIER_TO_EMAIL"),
		})
		log.Infof("%s Email notifier enabled", logcolors.LogNotifier)
	}

	if botToken := os.Getenv("NOTIFIER_TELEGRAM_BOT_TOKEN"); botToken != "" {
		notifiers = append(notifiers, &notifier.TelegramNotifier{
			BotToken: botToken,
			ChatID:   os.Getenv("NOTIFIER_TELEGRAM_CHAT_ID"),
		})
		log.Infof("%s Telegram notifier enabled", logcolors.LogNotifier)
	}

	if topic := os.Getenv("NOTIFIER_NTFY_TOPIC"); topic != "" {
		notifiers = append(notifiers, &notifier.NtfyNotifier{
			Topic:  topic,
			Server: getEnvOrDefault("NOTIFIER_NTFY_SERVER", "https://ntfy.sh"),
		})
		log.Infof("%s Ntfy.sh notifier enabled", logcolors.LogNotifier)
	}

	return notifiers
}
