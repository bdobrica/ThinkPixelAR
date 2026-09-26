package control

import (
	"bytes"
	"encoding/json"
	"io"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// ThreadCapability extends START with one Codex thread and a correlated identity
// observation before acknowledgement. It does not permit turns or new authority.
const ThreadCapability = "codex-thread.v1"

type ThreadStarted struct {
	ProcessID primitives.ID `json:"process_id"`
	ThreadID  string        `json:"thread_id"`
}

func (ThreadStarted) String() string     { return "[harness thread identity]" }
func (v ThreadStarted) GoString() string { return v.String() }

func ThreadObservation(f *agentdv1.Envelope) (ThreadStarted, error) {
	var value ThreadStarted
	o := f.GetObservation()
	if o == nil || o.Kind != agentdv1.Observation_PROCESS_STATUS || o.PayloadSchema != ThreadCapability || o.ArtifactReference != "" || len(o.Payload) > 256 {
		return value, ErrControl
	}
	d := json.NewDecoder(bytes.NewReader(o.Payload))
	d.DisallowUnknownFields()
	if d.Decode(&value) != nil {
		return ThreadStarted{}, ErrControl
	}
	if _, err := d.Token(); err != io.EOF {
		return ThreadStarted{}, ErrControl
	}
	// Require canonical encoding to reject duplicate keys and ambiguous spelling.
	raw, _ := json.Marshal(value)
	if !bytes.Equal(raw, o.Payload) || !codex.ValidThreadID(value.ThreadID) {
		return ThreadStarted{}, ErrControl
	}
	if _, err := primitives.ParseID(string(value.ProcessID)); err != nil {
		return ThreadStarted{}, ErrControl
	}
	return value, nil
}
