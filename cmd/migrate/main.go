// Command migrate is the only supported entry point for PostgreSQL schema changes.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres/migrations"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const usageText = `Usage: migrate [flags] up

ThinkPixelAR PostgreSQL migrations run only through this explicit command.
API replicas never migrate automatically.

Flags:
  -database-url string  PostgreSQL connection URL (or THINKPIXELAR_DATABASE_URL)
  -timeout duration     Overall migration timeout (default 2m)
`

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { _, _ = io.WriteString(stderr, usageText) }
	databaseURL := flags.String("database-url", os.Getenv("THINKPIXELAR_DATABASE_URL"), "PostgreSQL connection URL")
	timeout := flags.Duration("timeout", 2*time.Minute, "overall migration timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() == 0 {
		_, _ = io.WriteString(stdout, usageText)
		return 0
	}

	if flags.NArg() != 1 || flags.Arg(0) != "up" {
		fmt.Fprintln(stderr, "migrate: expected command up")
		return 2
	}
	if *databaseURL == "" {
		fmt.Fprintln(stderr, "migrate: database URL is required")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "migrate: timeout must be positive")
		return 2
	}
	db, err := sql.Open("pgx", *databaseURL)
	if err != nil {
		fmt.Fprintln(stderr, "migrate: could not configure database")
		return 1
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := migrations.Up(ctx, db); err != nil {
		fmt.Fprintln(stderr, "migrate: failed; database details were suppressed")
		return 1
	}
	fprintln(stdout, "migrations applied successfully")
	return 0
}

func fprintln(w io.Writer, message string) { _, _ = fmt.Fprintln(w, message) }

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
