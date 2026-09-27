package http

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	stdhttp "net/http"

	executions "github.com/bdobrica/ThinkPixelAR/internal/app/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func signalExecution(signaler *executions.Signaler, authenticate SessionAuthentication) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if authenticate == nil {
			writeProblem(w, r, 401, "unauthorized", "Unauthorized")
			return
		}
		caller, err := authenticate(r)
		if err != nil {
			writeProblem(w, r, 401, "unauthorized", "Unauthorized")
			return
		}
		if signaler == nil {
			writeProblem(w, r, 503, "temporarily-unavailable", "Service Unavailable")
			return
		}
		media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" || (params["charset"] != "" && params["charset"] != "utf-8") {
			writeProblem(w, r, 415, "unsupported-media-type", "Unsupported Media Type")
			return
		}
		invalid := func() { writeProblem(w, r, 400, "invalid-request", "Invalid Request") }
		id, err := primitives.ParseID(r.PathValue("execution_id"))
		if err != nil || len(r.Header.Values("Content-Type")) != 1 || len(r.Header.Values("Idempotency-Key")) != 1 || r.URL.RawQuery != "" {
			invalid()
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, executions.MaxSignalBytes+1))
		if len(raw) > executions.MaxSignalBytes {
			writeProblem(w, r, 413, "payload-too-large", "Payload Too Large")
			return
		}
		if err != nil {
			invalid()
			return
		}
		request, err := executions.ParseSignal(raw)
		if err != nil {
			invalid()
			return
		}
		result, err := signaler.Signal(r.Context(), caller, id, r.Header.Get("Idempotency-Key"), request)
		if err != nil {
			switch {
			case errors.Is(err, executions.ErrInvalid):
				invalid()
			case errors.Is(err, executions.ErrNotFound):
				writeProblem(w, r, 404, "not-found", "Not Found")
			case errors.Is(err, executions.ErrDenied):
				writeProblem(w, r, 403, "forbidden", "Forbidden")
			case errors.Is(err, executions.ErrConflict):
				writeProblem(w, r, 409, "execution-conflict", "Execution Conflict")
			case errors.Is(err, executions.ErrUnsupported):
				writeProblem(w, r, 422, "unsupported-signal", "Unsupported Signal")
			default:
				writeProblem(w, r, 503, "temporarily-unavailable", "Service Unavailable")
			}
			return
		}
		if result.Replayed {
			w.Header().Set("Idempotency-Replayed", "true")
		}
		w.Header().Set("Location", result.Operation.ResourceLocation)
		body, _ := json.Marshal(result.Operation)
		writeJSON(w, 202, string(body))
	}
}
