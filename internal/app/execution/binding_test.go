package execution

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
)

func TestLocalBindingIntegrity(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 123456789, time.UTC)
	runtime := session.RuntimeBinding{AuthorityMode: "LOCAL", AuthorityNamespace: authority.LocalIssuer,
		AgentID: "codex", AgentVersionID: "pinned-version", RuntimeSpecSchemaVersion: "v1",
		RuntimeSpec: []byte(`{"image":"pinned"}`), RuntimeProfileSchemaVersion: "v1", RuntimeProfileSnapshot: []byte(`{"profile":"bounded"}`)}
	runtime.RuntimeSpecDigest = sandbox.Digest(runtime.RuntimeSpec)
	runtime.RuntimeProfileDigest = sandbox.Digest(runtime.RuntimeProfileSnapshot)
	s, err := session.New("01890f3a-5b7c-7def-8000-000000000001", "01890f3a-5b7c-7def-8000-000000000002", runtime, now)
	if err != nil {
		t.Fatal(err)
	}
	g := authority.Grant{ID: "01890f3a-5b7c-7def-8000-000000000003", Mode: authority.LocalMode, Issuer: authority.LocalIssuer,
		TenantID: s.TenantID(), SessionID: s.ID(), SessionVersion: 1, Generation: 1,
		PrincipalDigest: sandbox.Digest([]byte("principal")), RequestDigest: sandbox.Digest([]byte("request")),
		Runtime: runtime, IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
	snapshot, _ := json.Marshal(g)
	r := persistence.LocalGrantRecord{ID: g.ID, SessionID: s.ID(), Snapshot: snapshot, Digest: sandbox.Digest(snapshot), State: "ACTIVE"}
	evidence, _ := json.Marshal(runtime)
	b := domain.Binding{SessionID: s.ID(), SessionGeneration: 1, AuthorityMode: "LOCAL", AuthorityNamespace: authority.LocalIssuer,
		AuthorityReference: string(g.ID), GrantDigest: r.Digest, AgentID: runtime.AgentID, AgentVersionID: runtime.AgentVersionID,
		AgentEvidence: evidence, AgentEvidenceDigest: sandbox.Digest(evidence)}
	for _, tc := range []struct {
		name   string
		change func(*domain.Binding, *persistence.LocalGrantRecord)
		valid  bool
	}{
		{"original", func(*domain.Binding, *persistence.LocalGrantRecord) {}, true},
		{"JSONB round trip", func(b *domain.Binding, _ *persistence.LocalGrantRecord) {
			var object map[string]any
			_ = json.Unmarshal(b.AgentEvidence, &object)
			b.AgentEvidence, _ = json.MarshalIndent(object, "", "  ")
		}, true},
		{"cancelled history", func(_ *domain.Binding, r *persistence.LocalGrantRecord) { r.State = "CANCELLED"; r.Version = 1 }, true},
		{"grant bytes", func(_ *domain.Binding, r *persistence.LocalGrantRecord) { r.Snapshot = []byte(`{}`) }, false},
		{"grant reference", func(b *domain.Binding, _ *persistence.LocalGrantRecord) { b.AuthorityReference = string(s.ID()) }, false},
		{"record identity", func(_ *domain.Binding, r *persistence.LocalGrantRecord) { r.ID = s.ID() }, false},
		{"record Session", func(_ *domain.Binding, r *persistence.LocalGrantRecord) { r.SessionID = g.ID }, false},
		{"grant digest", func(b *domain.Binding, _ *persistence.LocalGrantRecord) { b.GrantDigest = sandbox.Digest(nil) }, false},
		{"generation", func(b *domain.Binding, _ *persistence.LocalGrantRecord) { b.SessionGeneration++ }, false},
		{"agent version", func(b *domain.Binding, _ *persistence.LocalGrantRecord) { b.AgentVersionID = "latest" }, false},
		{"AG identity", func(b *domain.Binding, _ *persistence.LocalGrantRecord) { b.AuthorityMode = "THINKPIXEL_AG" }, false},
		{"external Run", func(b *domain.Binding, _ *persistence.LocalGrantRecord) { b.ExternalRunID = "unissued-run" }, false},
		{"evidence digest", func(b *domain.Binding, _ *persistence.LocalGrantRecord) { b.AgentEvidenceDigest = sandbox.Digest(nil) }, false},
		{"extra evidence", func(b *domain.Binding, _ *persistence.LocalGrantRecord) {
			b.AgentEvidence = append([]byte(`{"extra":true,`), b.AgentEvidence[1:]...)
		}, false},
		{"changed runtime with matching digest", func(b *domain.Binding, _ *persistence.LocalGrantRecord) {
			copy := runtime
			copy.RuntimeSpec = []byte(`{"image":"replacement"}`)
			copy.RuntimeSpecDigest = sandbox.Digest(copy.RuntimeSpec)
			b.AgentEvidence, _ = json.Marshal(copy)
			b.AgentEvidenceDigest = sandbox.Digest(b.AgentEvidence)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding, record := b, r
			tc.change(&binding, &record)
			e, err := domain.New(s.TenantID(), "01890f3a-5b7c-7def-8000-000000000004", binding, g.ExpiresAt.Truncate(time.Microsecond), now.Truncate(time.Microsecond))
			if err != nil {
				t.Fatal(err)
			}
			got, err := verifyLocalBinding(e, s, record)
			if tc.valid {
				if err != nil || got.ID != g.ID {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrInvalidBinding) {
				t.Fatal("accepted mismatched binding", err)
			}
		})
	}
	// Even an internally consistent grant record cannot replace the bound tenant,
	// runtime or deadline. Rehashing the altered snapshot is insufficient.
	for _, tc := range []struct {
		name   string
		change func(*authority.Grant)
	}{
		{"tenant", func(g *authority.Grant) { g.TenantID = g.ID }},
		{"Session", func(g *authority.Grant) { g.SessionID = g.ID }},
		{"deadline", func(g *authority.Grant) { g.ExpiresAt = g.ExpiresAt.Add(time.Second) }},
		{"future issuance", func(g *authority.Grant) { g.IssuedAt = g.IssuedAt.Add(time.Second) }},
		{"runtime", func(g *authority.Grant) { g.Runtime.AgentVersionID = "v2" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grant, record, binding := g, r, b
			tc.change(&grant)
			record.Snapshot, _ = json.Marshal(grant)
			record.Digest = sandbox.Digest(record.Snapshot)
			binding.GrantDigest = record.Digest
			e, err := domain.New(s.TenantID(), "01890f3a-5b7c-7def-8000-000000000004", binding, g.ExpiresAt, now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = verifyLocalBinding(e, s, record); !errors.Is(err, ErrInvalidBinding) {
				t.Fatal(err)
			}
		})
	}
}
