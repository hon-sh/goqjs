package hnapi

import (
	"net/http"
	"time"
)

// Config controls Store / Syncer behavior.
type Config struct {
	// DB is a SQLite URI. Empty uses MemoryDB.
	// File example: "file:./hn.db"
	DB string

	PoolSize int

	FirebaseBase string

	// HTTPClient is used by Syncer for Firebase. If nil, a client with
	// HTTPTimeout is created.
	HTTPClient *http.Client
	HTTPTimeout time.Duration

	TopTTL time.Duration

	DefaultLimit       int
	DefaultDepth       int
	PerLevel           int
	FetchConcurrency   int
}

// MemoryDB is a crawshaw/sqlitex-compatible in-memory URI (shared cache for pools).
// Bare ":memory:" does not work with multiple connections.
const MemoryDB = "file:hnapi.db?mode=memory&cache=shared"

// DefaultConfig returns sensible defaults for the hn-ssr demo.
func DefaultConfig() Config {
	return Config{
		DB:               MemoryDB,
		PoolSize:         4,
		FirebaseBase:     "https://hacker-news.firebaseio.com/v0",
		HTTPTimeout:      30 * time.Second,
		TopTTL:           time.Minute,
		DefaultLimit:     30,
		DefaultDepth:     2,
		PerLevel:         40,
		FetchConcurrency: 8,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.DB == "" {
		c.DB = d.DB
	}
	if c.PoolSize < 1 {
		c.PoolSize = d.PoolSize
	}
	if c.FirebaseBase == "" {
		c.FirebaseBase = d.FirebaseBase
	}
	if c.HTTPTimeout <= 0 {
		c.HTTPTimeout = d.HTTPTimeout
	}
	if c.TopTTL <= 0 {
		c.TopTTL = d.TopTTL
	}
	if c.DefaultLimit < 1 {
		c.DefaultLimit = d.DefaultLimit
	}
	if c.DefaultDepth < 0 {
		c.DefaultDepth = d.DefaultDepth
	}
	if c.PerLevel < 1 {
		c.PerLevel = d.PerLevel
	}
	if c.FetchConcurrency < 1 {
		c.FetchConcurrency = d.FetchConcurrency
	}
	return c
}
