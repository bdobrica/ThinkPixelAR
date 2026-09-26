// Package control implements the closed process-control.v1 wire capability.
// Process observations never establish Harness readiness or Execution success.
package control

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"sync"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"google.golang.org/protobuf/proto"
)

const Capability = "process-control.v1"

var ErrControl = errors.New("agentd process control rejected")

// Digest covers the stable mutation meaning, excluding connection/sequence/time
// metadata so the same operation can be reconciled after reconnect.
func Digest(kind agentdv1.Command_Kind, config, handle string) string {
	sum := sha256.Sum256([]byte(Capability + "\x00" + strconv.Itoa(int(kind)) + "\x00" + config + "\x00" + handle))
	return "sha256:" + hex.EncodeToString(sum[:])
}
func Command(f *agentdv1.Envelope, configuration string) error {
	if f == nil || f.GetCommand() == nil {
		return ErrControl
	}
	c := f.GetCommand()
	if c.Kind == agentdv1.Command_EXECUTE {
		if err := turnCommand(f, configuration); err != nil {
			return err
		}
	} else {
		if c.ConfigurationDigest != configuration || c.PayloadSchema != Capability || len(c.Payload) != 0 || c.ArtifactReference != "" {
			return ErrControl
		}
		switch c.Kind {
		case agentdv1.Command_START, agentdv1.Command_STOP, agentdv1.Command_RESTART, agentdv1.Command_INTERRUPT, agentdv1.Command_STATUS:
		default:
			return ErrControl
		}
	}
	if _, err := primitives.ParseID(f.HarnessHandle); err != nil {
		return ErrControl
	}
	if _, err := primitives.ParseID(f.OperationId); err != nil {
		return ErrControl
	}
	if (c.Kind != agentdv1.Command_EXECUTE && f.RequestDigest != Digest(c.Kind, configuration, f.HarnessHandle)) || f.SentUnixMs <= 0 || f.DeadlineUnixMs <= f.SentUnixMs || f.DeadlineUnixMs <= time.Now().UnixMilli() {
		return ErrControl
	}
	return nil
}

// Sequences validates a single direction of one accepted connection. A duplicate
// identical frame is allowed only at the latest sequence; mutation idempotency
// remains the dispatcher's operation ledger, not this bounded stream guard.
type Sequences struct {
	mu       sync.Mutex
	sequence uint64
	last     []byte
}

func (s *Sequences) Check(f *agentdv1.Envelope) error {
	if f == nil {
		return ErrControl
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(f)
	if err != nil {
		return ErrControl
	}
	sum := sha256.Sum256(raw)
	if f.Sequence == s.sequence && s.sequence != 0 && string(sum[:]) == string(s.last) {
		return nil
	}
	if s.sequence == ^uint64(0) || f.Sequence != s.sequence+1 {
		return ErrControl
	}
	s.sequence = f.Sequence
	s.last = append(s.last[:0], sum[:]...)
	return nil
}
