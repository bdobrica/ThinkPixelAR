package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	stdhttp "net/http"
	"unicode/utf8"

	sessions "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

// SessionAuthentication is supplied by trusted HTTP composition. It must
// verify credentials and map tenant/principal; never trust identity headers.
type SessionAuthentication func(*stdhttp.Request) (sessions.Caller, error)

func createSession(creator *sessions.Creator, authenticate SessionAuthentication) stdhttp.HandlerFunc {
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
		if len(r.Header.Values("Idempotency-Key")) != 1 || len(r.Header.Values("Content-Type")) != 1 || r.URL.RawQuery != "" {
			writeProblem(w, r, 400, "invalid-request", "Invalid Request")
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody+1))
		if err != nil || len(raw) > maxRequestBody || !utf8.Valid(raw) {
			writeProblem(w, r, 400, "invalid-request", "Invalid Request")
			return
		}
		normalized, err := canonical.Transform(raw)
		if err != nil {
			writeProblem(w, r, 400, "invalid-request", "Invalid Request")
			return
		}
		// Canonicalization rejects duplicates. Explicit field validation also rejects
		// encoding/json's case-insensitive aliases and null scalar substitutions.
		var fields map[string]json.RawMessage
		if json.Unmarshal(normalized, &fields) != nil || len(fields) != 3 || !jsonString(fields["agent_runtime_spec_id"]) || !jsonString(fields["runtime_profile_id"]) {
			writeProblem(w, r, 400, "invalid-request", "Invalid Request")
			return
		}
		var source map[string]json.RawMessage
		if json.Unmarshal(fields["source"], &source) != nil || !jsonString(source["kind"]) {
			writeProblem(w, r, 400, "invalid-request", "Invalid Request")
			return
		}
		for k, v := range source {
			if (k != "kind" && k != "reference" && k != "digest") || !jsonString(v) {
				writeProblem(w, r, 400, "invalid-request", "Invalid Request")
				return
			}
		}
		decoder := json.NewDecoder(bytes.NewReader(normalized))
		decoder.DisallowUnknownFields()
		var request sessions.CreateRequest
		if decoder.Decode(&request) != nil {
			writeProblem(w, r, 400, "invalid-request", "Invalid Request")
			return
		}
		result, err := creator.Create(r.Context(), caller, r.Header.Get("Idempotency-Key"), request)
		if err != nil {
			switch {
			case errors.Is(err, sessions.ErrInvalid):
				writeProblem(w, r, 400, "invalid-request", "Invalid Request")
			case errors.Is(err, sessions.ErrForbidden):
				writeProblem(w, r, 403, "forbidden", "Forbidden")
			case errors.Is(err, sessions.ErrUnsupported):
				writeProblem(w, r, 422, "unsupported-session-configuration", "Unsupported Session Configuration")
			case errors.Is(err, sessions.ErrConflict):
				writeProblem(w, r, 409, "idempotency-key-conflict", "Idempotency Key Conflict")
			default:
				writeProblem(w, r, 503, "temporarily-unavailable", "Service Unavailable")
			}
			return
		}
		w.Header().Set("Location", "/v1/sessions/"+string(result.View.ID))
		if result.Replayed {
			w.Header().Set("Idempotency-Replayed", "true")
		}
		body, _ := json.Marshal(result.View)
		writeJSON(w, 201, string(body))
	}
}
func jsonString(raw []byte) bool { return len(raw) >= 2 && raw[0] == '"' }
