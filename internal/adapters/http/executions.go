package http

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	stdhttp "net/http"
	"strconv"
	"unicode/utf8"

	executions "github.com/bdobrica/ThinkPixelAR/internal/app/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

func createExecution(creator *executions.Creator, authenticate SessionAuthentication) stdhttp.HandlerFunc {
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
		if creator == nil {
			writeProblem(w, r, 503, "temporarily-unavailable", "Service Unavailable")
			return
		}
		media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" || (params["charset"] != "" && params["charset"] != "utf-8") {
			writeProblem(w, r, 415, "unsupported-media-type", "Unsupported Media Type")
			return
		}
		invalid := func() { writeProblem(w, r, 400, "invalid-request", "Invalid Request") }
		if len(r.Header.Values("Content-Type")) != 1 || len(r.Header.Values("Idempotency-Key")) != 1 || len(r.Header.Values("If-Match")) != 1 || r.URL.RawQuery != "" {
			invalid()
			return
		}
		tag := r.Header.Get("If-Match")
		if len(tag) < 3 || tag[0] != '"' || tag[len(tag)-1] != '"' || tag[1] < '1' || tag[1] > '9' {
			invalid()
			return
		}
		for _, ch := range tag[1 : len(tag)-1] {
			if ch < '0' || ch > '9' {
				invalid()
				return
			}
		}
		version, err := strconv.ParseUint(tag[1:len(tag)-1], 10, 63)
		if err != nil {
			invalid()
			return
		}
		sid, err := primitives.ParseID(r.PathValue("session_id"))
		if err != nil {
			invalid()
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody+1))
		if len(raw) > maxRequestBody {
			writeProblem(w, r, 413, "payload-too-large", "Payload Too Large")
			return
		}
		if err != nil || !utf8.Valid(raw) {
			invalid()
			return
		}
		normalized, err := canonical.Transform(raw)
		if err != nil {
			invalid()
			return
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(normalized, &fields) != nil || !jsonString(fields["input"]) {
			invalid()
			return
		}
		for k, v := range fields {
			if (k != "input" && k != "run_reference") || !jsonString(v) {
				invalid()
				return
			}
		}
		var request executions.CreateRequest
		if json.Unmarshal(normalized, &request) != nil {
			invalid()
			return
		}
		if len(request.RunReference) > 256 {
			invalid()
			return
		}
		result, err := creator.Create(r.Context(), caller, sid, version, r.Header.Get("Idempotency-Key"), request)
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
				writeProblem(w, r, 422, "unsupported-authority", "Unsupported Authority")
			default:
				writeProblem(w, r, 503, "temporarily-unavailable", "Service Unavailable")
			}
			return
		}
		w.Header().Set("Location", "/v1/executions/"+string(result.View.ID))
		if result.Replayed {
			w.Header().Set("Idempotency-Replayed", "true")
		}
		body, _ := json.Marshal(result.View)
		writeJSON(w, 201, string(body))
	}
}
