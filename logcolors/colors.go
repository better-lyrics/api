package logcolors

// ANSI color codes for log prefixes
const (
	Reset  = "\033[0m"
	Green  = "\033[32m"
	Blue   = "\033[34m"
	Purple = "\033[35m"
	Cyan   = "\033[36m"
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
)

// Rate limiting and monitoring log prefixes
const (
	LogRateLimit    = Purple + "[RateLimit]" + Reset
	LogTokenMonitor = Cyan + "[Token Monitor]" + Reset
)

// CircuitBreakerPrefix returns a colored circuit breaker prefix with the given name
func CircuitBreakerPrefix(name string) string {
	return Purple + "[CircuitBreaker:" + name + "]" + Reset
}

// Test/debug log prefixes
const (
	LogTestNotifications = Cyan + "[Test Notifications]" + Reset
)

// TTML service log prefixes
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
)
