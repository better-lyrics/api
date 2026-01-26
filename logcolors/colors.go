package logcolors

// ANSI color codes for log prefixes
const (
	Reset  = "\033[0m"
	Green  = "\033[32m"
	Blue   = "\033[34m"
	Purple = "\033[35m"
	Cyan   = "\033[36m"

	// Bright variants for more color variety
	BrightGreen   = "\033[92m"
	BrightBlue    = "\033[94m"
	BrightMagenta = "\033[95m"
	BrightCyan    = "\033[96m"

	// Red variants (for account names only)
	Red       = "\033[31m"
	BrightRed = "\033[91m"
)

// Cache-related log prefixes
const (
	LogCacheInit     = Blue + "[Cache:Init]" + Reset
	LogCache         = Blue + "[Cache]" + Reset
	LogCacheBackup   = Blue + "[Cache:Backup]" + Reset
	LogCacheClear    = Blue + "[Cache:Clear]" + Reset
	LogCacheBackups  = Blue + "[Cache:Backups]" + Reset
	LogCacheRestore  = Blue + "[Cache:Restore]" + Reset
	LogCacheLyrics   = Green + "[Cache:Lyrics]" + Reset
	LogCacheNegative = Cyan + "[Cache:Negative]" + Reset
	LogRevalidate    = Cyan + "[Revalidate]" + Reset
)

// Rate limiting log prefixes
const (
	LogRateLimit = Purple + "[RateLimit]" + Reset
	LogAPIKey    = Purple + "[APIKey]" + Reset
)

// CircuitBreakerPrefix returns a colored circuit breaker prefix with the given name
func CircuitBreakerPrefix(name string) string {
	return Purple + "[CircuitBreaker:" + name + "]" + Reset
}

// accountColors are the colors used for account names (rotating based on hash)
// 10 colors to distribute ~50 account names
var accountColors = []string{
	Green, Blue, Purple, Cyan, Red,
	BrightGreen, BrightBlue, BrightMagenta, BrightCyan, BrightRed,
}

// Account returns a colored account name for log messages
// Same account name always gets the same color
func Account(name string) string {
	// Simple hash: sum of bytes mod number of colors
	hash := 0
	for _, c := range name {
		hash += int(c)
	}
	color := accountColors[hash%len(accountColors)]
	return color + name + Reset
}

// Test/debug log prefixes
const (
	LogTestNotifications = Cyan + "[Test Notifications]" + Reset
)

// Server/Init log prefixes
const (
	LogServer = Green + "[Server]" + Reset
	LogConfig = Cyan + "[Config]" + Reset
	LogStats  = Blue + "[Stats]" + Reset
)

// Notification log prefixes
const (
	LogNotifier = Cyan + "[Notifier]" + Reset
)

// Provider service log prefixes
const (
	LogRequest        = Purple + "[Request]" + Reset
	LogSearch         = Blue + "[Search]" + Reset
	LogHTTP           = Cyan + "[HTTP]" + Reset
	LogMatch          = Green + "[Match]" + Reset
	LogSuccess        = Green + "[Success]" + Reset
	LogLyrics         = Blue + "[Lyrics]" + Reset
	LogDurationFilter = Cyan + "[Duration Filter]" + Reset
	LogQuarantine     = Purple + "[Quarantine]" + Reset
	LogAuthError      = Purple + "[Auth Error]" + Reset
	LogCircuitBreaker = Purple + "[CircuitBreaker]" + Reset
	LogFallback       = Cyan + "[Fallback]" + Reset
	LogBestMatch      = Green + "[Best Match]" + Reset
	LogTrackScore     = Cyan + "[Track Score]" + Reset
	LogTTMLParser     = Cyan + "[TTML Parser]" + Reset
	LogWarning        = Red + "[Warning]" + Reset
)

// Token and health check log prefixes
const (
	LogBearerToken  = Cyan + "[Bearer Token]" + Reset
	LogHealthCheck  = Cyan + "[Health Check]" + Reset
)
