package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"unicode/utf8"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

const TurnCapability = "codex-turn.v1"
const MaxTurnTextBytes = 16 << 10
const MaxTurnPayloadBytes = 100 << 10 // JSON escaping can expand UTF-8 text.

// TurnInput is execution content, never configuration or authority.
type TurnInput struct {
	InputID        primitives.ID               `json:"input_id"`
	Classification runtimeevent.Classification `json:"classification"`
	Text           string                      `json:"text"`
}

func (TurnInput) String() string     { return "[restricted execution input]" }
func (v TurnInput) GoString() string { return v.String() }

func (v TurnInput) Validate() error {
	if _, err := primitives.ParseID(string(v.InputID)); err != nil {
		return ErrControl
	}
	if v.Classification != runtimeevent.Public && v.Classification != runtimeevent.Internal && v.Classification != runtimeevent.Confidential {
		return ErrControl
	}
	if len(v.Text) == 0 || len(v.Text) > MaxTurnTextBytes || !utf8.ValidString(v.Text) {
		return ErrControl
	}
	return nil
}
func DecodeTurnInput(raw []byte) (TurnInput, error) {
	var v TurnInput
	if len(raw) > MaxTurnPayloadBytes || json.Unmarshal(raw, &v) != nil || v.Validate() != nil {
		return TurnInput{}, ErrControl
	}
	canonical, _ := json.Marshal(v)
	if !bytes.Equal(canonical, raw) {
		return TurnInput{}, ErrControl
	}
	return v, nil
}

// ExecutionInput maps the currently bound Execution's inline text. Protected
// references and options require their own governed resolvers and are rejected.
func ExecutionInput(h harness.HarnessHandle, r harness.ExecuteRequest) ([]byte, error) {
	if r.Fence != h.Fence || r.Input.Schema != "text/plain" || r.Input.ArtifactReference != "" || len(r.Options) != 0 {
		return nil, ErrControl
	}
	v := TurnInput{r.InputID, r.Input.Classification, string(r.Input.Inline)}
	if v.Validate() != nil {
		return nil, ErrControl
	}
	return json.Marshal(v)
}
func TurnDigest(configuration, handle string, payload []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(TurnCapability + "\x00" + configuration + "\x00" + handle + "\x00"))
	_, _ = h.Write(payload)
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
func turnCommand(f *agentdv1.Envelope, configuration string) error {
	c := f.GetCommand()
	if c.Kind != agentdv1.Command_EXECUTE || c.ConfigurationDigest != configuration || c.PayloadSchema != TurnCapability || c.ArtifactReference != "" {
		return ErrControl
	}
	if _, err := DecodeTurnInput(c.Payload); err != nil {
		return err
	}
	if f.RequestDigest != TurnDigest(configuration, f.HarnessHandle, c.Payload) {
		return ErrControl
	}
	return nil
}
