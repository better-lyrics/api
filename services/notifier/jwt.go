package notifier

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// JWTClaims represents the decoded JWT claims
type JWTClaims struct {
	Issuer         string   `json:"iss"`
	IssuedAt       int64    `json:"iat"`
	ExpiresAt      int64    `json:"exp"`
	RootHTTPOrigin []string `json:"root_https_origin"`
}

// DecodeJWT decodes a JWT token without verification (since we just need to read expiration)
func DecodeJWT(token string) (*JWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	// Decode the payload (second part)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWT payload: %v", err)
	}

	var claims JWTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JWT claims: %v", err)
	}

	return &claims, nil
}

// GetExpirationDate returns the expiration date of the JWT
func GetExpirationDate(token string) (time.Time, error) {
	claims, err := DecodeJWT(token)
	if err != nil {
		return time.Time{}, err
	}

	return time.Unix(claims.ExpiresAt, 0), nil
}

// DaysUntilExpiration returns how many days until the token expires
func DaysUntilExpiration(token string) (int, error) {
	expiration, err := GetExpirationDate(token)
	if err != nil {
		return 0, err
	}

	duration := time.Until(expiration)
	days := int(duration.Hours() / 24)

	return days, nil
}

// IsExpiringSoon checks if token expires within the given number of days
func IsExpiringSoon(token string, daysThreshold int) (bool, int, error) {
	days, err := DaysUntilExpiration(token)
	if err != nil {
		return false, 0, err
	}

	return days <= daysThreshold && days >= 0, days, nil
}
