package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"lyrics-api-go/config"
	"lyrics-api-go/internal/httpapi"
	"lyrics-api-go/internal/store"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/notifier"

	// Register providers via their init().
	_ "lyrics-api-go/services/providers/kugou"
	_ "lyrics-api-go/services/providers/legacy"
	_ "lyrics-api-go/services/providers/qq"
	"lyrics-api-go/services/providers/ttml"

	"github.com/joho/godotenv"
	log "github.com/sirupsen/logrus"
)

func main() {
	_ = godotenv.Load()

	cfg := config.Get()
	if cfg.FeatureFlags.PrettyLogs {
		log.SetFormatter(&log.TextFormatter{ForceColors: true, FullTimestamp: true, TimestampFormat: "15:04:05"})
	} else {
		log.SetFormatter(&log.JSONFormatter{})
	}
	log.SetOutput(os.Stdout)
	log.SetLevel(log.InfoLevel)

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("POSTGRES_DSN")
	}
	if dsn == "" {
		notifier.PublishServerStartupFailed("store", nil)
		log.Fatal("DATABASE_URL is not set")
	}

	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		notifier.PublishServerStartupFailed("store", err)
		log.Fatalf("Failed to connect to Postgres: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		notifier.PublishServerStartupFailed("migrate", err)
		log.Fatalf("Failed to run migrations: %v", err)
	}

	srv := httpapi.New(st, cfg)

	alertNotifiers := setupAlertNotifiers()
	if len(alertNotifiers) > 0 {
		alertHandler := notifier.NewAlertHandler(notifier.AlertConfig{
			Notifiers:        alertNotifiers,
			CooldownDuration: 15 * time.Minute,
		})
		alertHandler.Start()
		log.Infof("%s Alert handler initialized with %d notifier(s)", logcolors.LogNotifier, len(alertNotifiers))
	}

	ttml.StartBearerTokenMonitor()
	ttml.StartHealthCheckScheduler()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	activeAccounts, _ := cfg.GetTTMLAccounts()
	allAccounts, _ := cfg.GetAllTTMLAccounts()
	var outOfServiceNames []string
	for _, acc := range allAccounts {
		if acc.OutOfService {
			outOfServiceNames = append(outOfServiceNames, acc.Name)
		}
	}

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	log.Infof("%s Listening on port %s", logcolors.LogServer, port)
	notifier.PublishServerStarted(port, len(activeAccounts), outOfServiceNames)
	log.Fatal(httpServer.ListenAndServe())
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
