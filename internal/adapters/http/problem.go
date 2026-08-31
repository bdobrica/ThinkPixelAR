package http

import (
	"encoding/json"
	stdhttp "net/http"
)

const problemBase = "https://thinkpixel.dev/problems/"

// Problem is the public RFC 7807 error envelope.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Instance  string `json:"instance,omitempty"`
	Code      string `json:"code"`
	RequestID string `json:"request_id"`
}

func writeProblem(w stdhttp.ResponseWriter, r *stdhttp.Request, status int, code, title string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{Type: problemBase + code, Title: title, Status: status, Instance: r.URL.Path, Code: code, RequestID: RequestIDFromContext(r.Context())})
}
