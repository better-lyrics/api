package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func main() {
	boltPath := flag.String("bolt", "", "path to the legacy cache.db BoltDB snapshot (opened read-only)")
	statsPath := flag.String("stats", "", "optional path to the legacy stats.db BoltDB snapshot")
	dsn := flag.String("dsn", "", "target Postgres connection string")
	verify := flag.Bool("verify", false, "verify already-migrated data against the bolt snapshot instead of migrating")
	flag.Parse()

	if *boltPath == "" || *dsn == "" {
		fmt.Fprintln(os.Stderr, "usage: migrate --bolt <cache.db> --dsn <postgres dsn> [--stats <stats.db>] [--verify]")
		os.Exit(2)
	}

	ctx := context.Background()

	m, cleanup, err := NewMigrator(ctx, *boltPath, *statsPath, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	if *verify {
		result, err := m.Verify(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "verify: %v\n", err)
			os.Exit(1)
		}
		result.Print(os.Stdout)
		if !result.Passed {
			os.Exit(1)
		}
		return
	}

	result, err := m.Run(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	result.Print(os.Stdout)
}
