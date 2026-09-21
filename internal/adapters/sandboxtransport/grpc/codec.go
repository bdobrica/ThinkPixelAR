package grpctransport

import (
	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	"google.golang.org/protobuf/proto"
)

// codec retains standard protobuf wire encoding with stricter receive bounds.
// It is private to this dedicated service; no global codec registration occurs.
type codec struct{}

func (codec) Name() string { return "proto" }
func (codec) Marshal(v any) ([]byte, error) {
	m, ok := v.(*agentdv1.Envelope)
	if !ok || m == nil || proto.Size(m) > protocol.MaxFrameBytes {
		return nil, ErrTransport
	}
	raw, err := proto.Marshal(m)
	if err != nil {
		return nil, ErrTransport
	}
	return raw, nil
}
func (codec) Unmarshal(raw []byte, v any) error {
	m, ok := v.(*agentdv1.Envelope)
	if !ok || m == nil || len(raw) > protocol.MaxFrameBytes {
		return ErrTransport
	}
	if (proto.UnmarshalOptions{RecursionLimit: 16}).Unmarshal(raw, m) != nil || !protocol.KnownFields(m) {
		return ErrTransport
	}
	return nil
}
