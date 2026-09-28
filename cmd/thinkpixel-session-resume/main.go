//go:build linux

// thinkpixel-session-resume reconciles one operator-approved standalone resume.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sessionresume"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "session resume unavailable; retain the same operation/config for reconciliation")
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "", "protected operator resume configuration")
	cleanup := flag.Bool("cleanup", false, "reconcile an already abandoned candidate; preserve checkpoint and PVCs")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := sql.Open("pgx", os.Getenv("THINKPIXELAR_DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err = db.PingContext(check); err != nil {
		return err
	}
	result, err := sessionresume.Run(ctx, db, *path, *cleanup)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
