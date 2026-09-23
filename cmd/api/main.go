package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"lyrics-api-go/config"
	"lyrics-api-go/internal/httpapi"
	"lyrics-api-go/internal/store"
	"lyrics-api-go/internal/syncupgrade"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/cdn"
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
		err := errors.New("DATABASE_URL is not set")
		notifier.PublishServerStartupFailed("store", err)
		log.Fatal(err)
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

	loadStats(ctx, st)
	startStatsAutosave(ctx, st, 5*time.Minute)

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

	ttml.SetStorefrontBackend(
		func(hash string) string {
			sf, _, err := st.GetStorefront(ctx, hash)
			if err != nil {
				log.Warnf("%s storefront lookup failed: %v", logcolors.LogAccountInit, err)
			}
			return sf
		},
		func(hash, storefront string) {
			if err := st.SetStorefront(ctx, hash, storefront); err != nil {
				log.Warnf("%s storefront store failed: %v", logcolors.LogAccountInit, err)
			}
		},
	)

	ttml.StartBearerTokenMonitor()
	ttml.StartHealthCheckScheduler()
	cdn.Start(ctx, cfg)
	startNegativePurge(ctx, st)
	go syncupgrade.StartSyncUpgradeDetector(st, cfg)

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
