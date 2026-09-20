package main

import (
	"lyrics-api-go/internal/store"
)

func deriveKey(cacheKey string) store.Key {
	return store.DeriveKey(cacheKey)
}
