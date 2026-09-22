package agentdadmission

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"google.golang.org/protobuf/proto"
)

// Admission must not promote agentd observations into provider/compute facts.
// The semantic-policy fixture deliberately permits delivery: outer authority
// checks must hold even when the report's process-state enum is syntactically valid.
type reportStore struct {
	*fixture
	writes int
}

func (s *reportStore) RecordCompute(context.Context, sandbox.ComputeIntent, sandbox.ComputeObservation) error {
	s.writes++
	return ErrAdmission
}

func TestHostileProcessReportsCannotOverrideAuthority(t *testing.T) {
	for _, state := range []agentdv1.Heartbeat_ProcessState{agentdv1.Heartbeat_ABSENT, agentdv1.Heartbeat_STARTING, agentdv1.Heartbeat_RUNNING, agentdv1.Heartbeat_STOPPING, agentdv1.Heartbeat_EXITED, agentdv1.Heartbeat_FAILED} {
		for _, drift := range []string{"current", "obsolete-attempt", "released", "generation", "execution", "provider-lost", "unverified", "provider-replaced", "revoked", "expired", "old-epoch", "forged-binding", "semantic-denial"} {
			t.Run(state.String()+"/"+drift, func(t *testing.T) {
				_, f := newFixture(t)
				store := &reportStore{fixture: f}
				s, err := New(store, f, f, f, f)
				if err != nil {
					t.Fatal(err)
				}
				lease, err := s.Admit(context.Background(), f.peer, make([]byte, 32))
				if err != nil {
					t.Fatal(err)
				}
				defer lease.Close()
				e := frame(f)
				e.Body = &agentdv1.Envelope_Heartbeat{Heartbeat: &agentdv1.Heartbeat{ProcessState: state}}
				switch drift {
				case "obsolete-attempt":
					f.intent.Current = false
				case "released":
					f.intent.Desired = sandbox.ComputeReleased
				case "generation":
					f.intent.Binding.Request.Scope.Generation++
				case "execution":
					f.intent.Binding.Request.Scope.ExecutionID = f.intent.Binding.Request.Scope.SessionID
				case "provider-lost":
					f.status.State = sandbox.Unknown
				case "unverified":
					f.status.Effective.Verified = false
				case "provider-replaced":
					f.status.Handle.ProviderReference = "replacement"
				case "revoked":
					f.policyErr = ErrAdmission
				case "expired":
					f.intent.Binding.Request.Deadline = time.Now().Add(-time.Second)
				case "old-epoch":
					e.ConnectionEpoch--
				case "forged-binding":
					e.Binding.AttemptId = e.Binding.SessionId
				case "semantic-denial":
					f.frameErr = ErrAdmission
				}
				before, _ := json.Marshal([]any{f.intent, f.auth, f.status, f.peer, f.connection, f.version})
				report := proto.Clone(e)
				err = lease.Check(context.Background(), e)
				if drift == "current" {
					if err != nil || f.frames != 1 {
						t.Fatal("observation not delivered to policy", err)
					}
				} else {
					if err != ErrAdmission {
						t.Fatal("hostile report bypassed authority")
					}
					want := 0
					if drift == "semantic-denial" {
						want = 1
					}
					if f.frames != want {
						t.Fatal("obsolete report reached semantic policy")
					}
				}
				after, _ := json.Marshal([]any{f.intent, f.auth, f.status, f.peer, f.connection, f.version})
				if string(before) != string(after) || store.writes != 0 || !proto.Equal(e, report) {
					t.Fatal("report changed trusted intent or caller frame")
				}
			})
		}
	}
}
