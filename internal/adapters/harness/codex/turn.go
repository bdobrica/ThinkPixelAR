package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// StartTurn starts one Execution operation on the initialized thread. Success is
// vendor acceptance only. Event consumption/completion are separate operations.
// The caller owns current authority/fencing and must stop the child on failure.
func (c *Client) StartTurn(ctx context.Context, operation, inputID primitives.ID, text string) (id string, err error) {
	if !c.gate.TryLock() {
		return "", harness.ErrConflict
	}
	defer c.gate.Unlock()
	if c.threadID == "" || len(text) == 0 || len(text) > 16<<10 || !utf8.ValidString(text) {
		return "", harness.ErrInvalid
	}
	if _, err := primitives.ParseID(string(operation)); err != nil {
		return "", harness.ErrInvalid
	}
	if _, err := primitives.ParseID(string(inputID)); err != nil {
		return "", harness.ErrInvalid
	}
	sum := sha256.Sum256([]byte(string(inputID) + "\x00" + text))
	digest := hex.EncodeToString(sum[:])
	if c.turnOperation != "" {
		if c.turnOperation != string(operation) || c.turnDigest != digest {
			return "", harness.ErrConflict
		}
		if c.turnID == "" {
			return "", harness.ErrOutcomeUnknown
		}
		return c.turnID, nil
	}
	if ctx.Err() != nil {
		return "", harness.ErrOutcomeUnknown
	}
	c.turnOperation, c.turnDigest = string(operation), digest
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, c.Close)
	defer stop()
	defer func() {
		if ctx.Err() != nil {
			id, err = "", harness.ErrOutcomeUnknown
		}
		if err != nil {
			c.Close()
		}
	}()
	request, _ := json.Marshal(map[string]any{"id": 3, "method": "turn/start", "params": map[string]any{
		"threadId": c.threadID, "input": []any{map[string]any{"type": "text", "text": text}},
		"cwd": c.threadCWD, "approvalPolicy": "never", "sandboxPolicy": map[string]any{"type": "readOnly", "networkAccess": false},
	}})
	request = append(request, '\n')
	defer clear(request)
	if _, err := c.in.Write(request); err != nil {
		return "", harness.ErrOutcomeUnknown
	}
	for range 32 {
		raw, e := c.reader.ReadSlice('\n')
		if e != nil {
			return "", harness.ErrProtocol
		}
		frame, e := object(raw)
		clear(raw)
		if e != nil {
			return "", harness.ErrProtocol
		}
		if frame["id"] != nil {
			if string(frame["id"]) != "3" || len(frame) != 2 {
				return "", harness.ErrProtocol
			}
			result, e := object(frame["result"])
			if e != nil {
				return "", harness.ErrProtocol
			}
			turn, e := object(result["turn"])
			if e != nil {
				return "", harness.ErrProtocol
			}
			var turnID string
			if json.Unmarshal(turn["id"], &turnID) != nil || !ValidThreadID(turnID) || string(turn["status"]) != `"inProgress"` {
				return "", harness.ErrProtocol
			}
			c.turnID = turnID
			return turnID, nil
		}
		var method string
		if json.Unmarshal(frame["method"], &method) != nil || frame["params"] == nil {
			return "", harness.ErrProtocol
		}
		switch method {
		case "remoteControl/status/changed", "configWarning", "warning", "skills/changed", "mcpServer/startupStatus/updated", "thread/status/changed":
			// Only bounded startup/status hints may precede acceptance. Never publish raw data.
		default:
			return "", harness.ErrProtocol
		}
	}
	return "", harness.ErrProtocol
}
