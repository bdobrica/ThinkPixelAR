package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	stdhttp "net/http"
	"strconv"
	"sync"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/app/eventstream"
	"github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// Limits are per server replica. No proxy-supplied address is trusted.
// One event is pending per connection; no subscriber goroutine/queue can block
// the producer. Deployment-wide quotas belong at the authenticated ingress.
type streamLimits struct {
	mu                       sync.Mutex
	total                    int
	tenants, principals, ips map[string]int
}

func (l *streamLimits) acquire(tenant, principal, ip string) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= 128 || l.tenants[tenant] >= 32 || l.principals[principal] >= 4 || l.ips[ip] >= 16 {
		return nil, false
	}
	if l.tenants == nil {
		l.tenants = map[string]int{}
		l.principals = map[string]int{}
		l.ips = map[string]int{}
	}
	l.total++
	l.tenants[tenant]++
	l.principals[principal]++
	l.ips[ip]++
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.total--
		for _, item := range []struct {
			m map[string]int
			k string
		}{{l.tenants, tenant}, {l.principals, principal}, {l.ips, ip}} {
			item.m[item.k]--
			if item.m[item.k] == 0 {
				delete(item.m, item.k)
			}
		}
	}, true
}

type streamTiming struct{ poll, heartbeat, write, lifetime time.Duration }

var defaultStreamTiming = streamTiming{time.Second, 15 * time.Second, 5 * time.Second, 15 * time.Minute}

func sessionStream(reader *eventstream.Reader, authenticate SessionAuthentication, limits *streamLimits, timing streamTiming) stdhttp.HandlerFunc {
	var next eventNext
	if reader != nil {
		next = reader.Next
	}
	return eventStream(next, "session_id", authenticate, limits, timing)
}

type eventNext func(context.Context, session.Caller, primitives.ID, uint64) (eventstream.Result, error)

func executionStream(reader *eventstream.ExecutionReader, authenticate SessionAuthentication, limits *streamLimits, timing streamTiming) stdhttp.HandlerFunc {
	var next eventNext
	if reader != nil {
		next = reader.Next
	}
	return eventStream(next, "execution_id", authenticate, limits, timing)
}

func eventStream(read eventNext, pathKey string, authenticate SessionAuthentication, limits *streamLimits, timing streamTiming) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if authenticate == nil {
			writeProblem(w, r, 401, "unauthorized", "Unauthorized")
			return
		}
		caller, err := authenticate(r)
		if err != nil {
			writeProblem(w, r, 401, "unauthorized", "Unauthorized")
			return
		}
		if read == nil {
			writeProblem(w, r, 503, "temporarily-unavailable", "Service Unavailable")
			return
		}
		id, err := primitives.ParseID(r.PathValue(pathKey))
		values := r.Header.Values("Last-Event-ID")
		var after uint64
		if len(values) == 1 {
			after, err = parseEventSequence(values[0], err)
		}
		if err != nil || len(values) > 1 || r.URL.RawQuery != "" || r.Method != stdhttp.MethodGet {
			writeProblem(w, r, 400, "invalid-request", "Invalid Request")
			return
		}
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		release, ok := limits.acquire(string(caller.TenantID), string(caller.TenantID)+"/"+caller.PrincipalDigest, ip)
		if !ok {
			w.Header().Set("Retry-After", "5")
			writeProblem(w, r, 429, "rate-limited", "Too Many Requests")
			return
		}
		defer release()
		ctx, cancel := context.WithTimeout(r.Context(), timing.lifetime)
		defer cancel()
		r = r.WithContext(ctx)
		next := func() (eventstream.Result, error) {
			current, err := authenticate(r)
			if err != nil || current != caller {
				return eventstream.Result{}, eventstream.ErrNotFound
			}
			queryCtx, done := context.WithTimeout(ctx, 5*time.Second)
			defer done()
			return read(queryCtx, caller, id, after)
		}
		result, err := next()
		if err != nil {
			streamProblem(w, r, result, err)
			return
		}
		control := stdhttp.NewResponseController(w)
		deadline := func() time.Time {
			at := time.Now().Add(timing.write)
			if end, ok := ctx.Deadline(); ok && end.Before(at) {
				return end
			}
			return at
		}
		if err = control.SetWriteDeadline(deadline()); err != nil {
			writeProblem(w, r, 503, "temporarily-unavailable", "Service Unavailable")
			return
		}
		defer control.SetWriteDeadline(time.Time{})
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		write := func(frame string) bool {
			if control.SetWriteDeadline(deadline()) != nil {
				return false
			}
			if _, err := io.WriteString(w, frame); err != nil {
				slog.WarnContext(ctx, "event stream disconnected", "reason", "write-failed")
				return false
			}
			if control.Flush() != nil {
				slog.WarnContext(ctx, "event stream disconnected", "reason", "flush-failed")
				return false
			}
			// Idle connections must not retain a write deadline: HTTP/2 would
			// terminate the stream before the next heartbeat.
			return control.SetWriteDeadline(time.Time{}) == nil
		}
		if !write("retry: 1000\n\n") {
			return
		}
		nextHeartbeat := time.Now().Add(timing.heartbeat)
		for {
			if ctx.Err() != nil {
				return
			}
			if result.Sequence != 0 {
				if len(result.Data) != 0 && !write(fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", result.Sequence, result.Type, result.Data)) {
					return
				}
				after = result.Sequence
			} else {
				select {
				case <-ctx.Done():
					return
				case <-time.After(timing.poll):
				}
			}
			result, err = next()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				// Comments never impersonate durable events or advance the replay cursor.
				reason := "unavailable"
				if errors.Is(err, eventstream.ErrGap) {
					reason = "replay-gap"
				}
				if errors.Is(err, eventstream.ErrNotFound) {
					reason = "access-ended"
				}
				write(": stream-ended " + reason + "; reconnect from last received id\n\n")
				return
			}
			if time.Now().After(nextHeartbeat) {
				// Idle polling revalidates credentials/access every second; heartbeat is
				// deliberately separate from event identity and persisted history.
				if !write(": heartbeat\n\n") {
					return
				}
				nextHeartbeat = time.Now().Add(timing.heartbeat)
			}
		}
	}
}
func parseEventSequence(value string, previous error) (uint64, error) {
	if previous != nil {
		return 0, previous
	}
	n, err := strconv.ParseUint(value, 10, 63)
	if err != nil || strconv.FormatUint(n, 10) != value {
		return 0, eventstream.ErrInvalid
	}
	return n, nil
}
func streamProblem(w stdhttp.ResponseWriter, r *stdhttp.Request, result eventstream.Result, err error) {
	switch {
	case errors.Is(err, eventstream.ErrGap):
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(410)
		_ = json.NewEncoder(w).Encode(struct {
			Problem
			Earliest uint64 `json:"earliest_sequence"`
			Latest   uint64 `json:"latest_sequence"`
		}{Problem{Type: problemBase + "replay-gap", Title: "Event Replay Gap", Status: 410, Code: "replay-gap", RequestID: RequestIDFromContext(r.Context())}, result.Earliest, result.Latest})
	case errors.Is(err, eventstream.ErrInvalid):
		writeProblem(w, r, 400, "invalid-request", "Invalid Request")
	case errors.Is(err, eventstream.ErrNotFound):
		writeProblem(w, r, 404, "not-found", "Not Found")
	default:
		writeProblem(w, r, 503, "temporarily-unavailable", "Service Unavailable")
	}
}
