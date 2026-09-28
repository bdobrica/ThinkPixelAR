//go:build linux

// thinkpixel-resume-probe performs infrastructure-only Codex restoration. Its
// stdin is supplied by the trusted controller; it has no network listener.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/app/agentd"
	"golang.org/x/sys/unix"
)

func main() {
	idle := flag.Bool("idle", false, "remain quiescent and reap adopted descendants as container init")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *idle {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				for {
					pid, err := unix.Wait4(-1, nil, unix.WNOHANG, nil)
					if err != nil || pid <= 0 {
						break
					}
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var in agentd.ResumeProbe
	d := json.NewDecoder(io.LimitReader(os.Stdin, 4<<20))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF {
		os.Exit(1)
	}
	proof, err := agentd.ProbeResume(ctx, in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "resume probe failed")
		os.Exit(1)
	}
	if json.NewEncoder(os.Stdout).Encode(proof) != nil {
		os.Exit(1)
	}
}
