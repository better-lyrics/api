# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased] - 2024-11-29

### Added

#### Multi-Account Support
- **Comma-separated token configuration**: Support for multiple TTML API accounts via `TTML_BEARER_TOKENS` and `TTML_MEDIA_USER_TOKENS` environment variables
- **Backwards compatibility**: Legacy single-token env vars (`TTML_BEARER_TOKEN`, `TTML_MEDIA_USER_TOKEN`) still work as fallback
- **Startup validation**: Mismatch between bearer tokens and media user tokens count triggers a clear error message at startup
- **Account naming**: Accounts are automatically named `Account-1`, `Account-2`, etc. for logging and monitoring

#### Round-Robin Load Balancing
- **Even request distribution**: Requests are distributed evenly across all configured accounts using atomic round-robin selection
- **Thread-safe implementation**: Uses `sync/atomic` for lock-free concurrent access
- **Automatic failover**: On 401/429 errors, automatically skips to the next account and retries (up to 3 attempts or number of accounts, whichever is lower)

#### Health Check Endpoint
- **New `/health` endpoint**: Returns service health status
  - **Public access**: Shows basic status (`ok`/`degraded`/`unhealthy`), account count, and circuit breaker state
  - **Authenticated access**: Additionally shows detailed token expiration info for each account
- **Token status levels**: `healthy` (>7 days), `expiring_soon` (≤7 days), `expired`, `error`
- **Useful for**: Railway health checks, external monitoring, debugging

#### Token Expiration Monitoring
- **Multi-token monitoring**: Token monitor now checks all configured accounts
- **Aggregated notifications**: Single notification lists all expiring/expired tokens
- **Per-account status**: Notification messages include each account's name and days remaining

### Changed

#### Circuit Breaker Improvements
- **Scaled threshold**: Circuit breaker threshold now scales with account count (`base_threshold × num_accounts`)
- **Rationale**: With round-robin, each account may fail independently; scaling prevents premature circuit opening
- **Example**: With 3 accounts and base threshold of 5, circuit opens after 15 failures instead of 5

#### Account Manager Refactoring
- **New methods**:
  - `getNextAccount()`: Returns next account in round-robin sequence (thread-safe)
  - `skipCurrentAccount()`: Advances past a failing account (for 401/429 handling)
  - `accountCount()`: Returns number of configured accounts
  - `hasAccounts()`: Returns true if any accounts are configured
- **Panic prevention**: All methods safely handle empty account list without panicking

#### Logging Improvements
- **Load balancing visibility**: Startup log shows "Initialized N TTML account(s) with round-robin load balancing"
- **Circuit breaker details**: Shows scaled threshold calculation in logs
- **Error context**: 401/429 error logs now include which account failed

### Fixed

- **Empty accounts handling**: Prevented potential panics when no accounts are configured
- **Thread safety**: Account rotation is now safe for concurrent requests

### Configuration

#### New Environment Variables
```bash
# Multi-account support (comma-separated, preferred)
TTML_BEARER_TOKENS=token1,token2,token3
TTML_MEDIA_USER_TOKENS=media1,media2,media3
```

#### Legacy Environment Variables (still supported)
```bash
# Single account (backwards compatible)
TTML_BEARER_TOKEN=your_token
TTML_MEDIA_USER_TOKEN=your_media_token
```

### API Endpoints

#### GET /health
Returns service health status.

**Public Response:**
```json
{
  "status": "ok",
  "accounts": 3,
  "circuit_breaker": "CLOSED"
}
```

**Authenticated Response** (with `Authorization` header):
```json
{
  "status": "ok",
  "accounts": 3,
  "circuit_breaker": "CLOSED",
  "circuit_breaker_failures": 0,
  "tokens": [
    {
      "name": "Account-1",
      "status": "healthy",
      "expires": "2025-02-15 10:30:00",
      "days_remaining": 78
    },
    {
      "name": "Account-2",
      "status": "expiring_soon",
      "expires": "2024-12-05 10:30:00",
      "days_remaining": 6
    }
  ]
}
```

**Status Values:**
- `ok`: All systems healthy
- `degraded`: Circuit breaker open OR tokens expiring/expired
- `unhealthy`: No accounts configured

### Files Modified

- `config/config.go` - Added multi-account parsing and validation
- `services/ttml/account.go` - Refactored with round-robin and thread safety
- `services/ttml/types.go` - Changed `currentIndex` to `uint64` for atomic ops
- `services/ttml/client.go` - Round-robin selection, scaled circuit breaker
- `services/ttml/ttml.go` - Added empty accounts check
- `services/notifier/monitor.go` - Multi-token monitoring support
- `main.go` - Health endpoint, updated token monitor initialization
- `.env.example` - Updated with multi-account configuration examples
- `services/ttml/account_test.go` - Updated tests for new API

### Migration Guide

1. **No breaking changes**: Existing single-token configuration continues to work
2. **To add more accounts**:
   - Set `TTML_BEARER_TOKENS` and `TTML_MEDIA_USER_TOKENS` (comma-separated)
   - Remove or leave empty `TTML_BEARER_TOKEN` and `TTML_MEDIA_USER_TOKEN`
3. **Monitoring**: Use the new `/health` endpoint for service monitoring
