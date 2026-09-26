package codex

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
)

// ValidThreadID checks the pinned vendor's UUID representation without making
// it an AR identity or accepting filesystem paths in place of vendor IDs.
func ValidThreadID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// StartThread creates one thread per initialized process. The fixed read-only,
// no-approval policy grants no extra permissions; turn policy is separate work.
// A failed request is never retried because its vendor outcome may be unknown.
func (c *Client) StartThread(ctx context.Context, cwd string) (id string, err error) {
	if !c.gate.TryLock() {
		return "", harness.ErrConflict
	}
	defer c.gate.Unlock()
	if !c.initialized || !filepath.IsAbs(cwd) || filepath.Clean(cwd) != cwd || len(cwd) > 4096 || strings.ContainsAny(cwd, "\x00\r\n") {
		return "", harness.ErrInvalid
	}
	if c.threadAttempted {
		if c.threadCWD != cwd {
			return "", harness.ErrConflict
		}
		if c.threadID == "" {
			return "", harness.ErrOutcomeUnknown
		}
		return c.threadID, nil
	}
	if ctx.Err() != nil {
		return "", harness.ErrOutcomeUnknown
	}
	c.threadAttempted, c.threadCWD = true, cwd
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
	request, _ := json.Marshal(map[string]any{"id": 2, "method": "thread/start", "params": map[string]any{"cwd": cwd, "approvalPolicy": "never", "sandbox": "read-only", "ephemeral": false}})
	request = append(request, '\n')
	if _, err := c.in.Write(request); err != nil {
		return "", harness.ErrOutcomeUnknown
	}
	var responseID, notificationID string
	for range 32 {
		raw, readErr := c.reader.ReadSlice('\n')
		if readErr != nil {
			return "", harness.ErrProtocol
		}
		frame, decodeErr := object(raw)
		clear(raw)
		if decodeErr != nil {
			return "", harness.ErrProtocol
		}
		if frame["id"] != nil {
			if string(frame["id"]) != "2" || len(frame) != 2 || responseID != "" {
				return "", harness.ErrProtocol
			}
			result, err := object(frame["result"])
			if err != nil {
				return "", harness.ErrProtocol
			}
			var directory, approval string
			policy, policyErr := object(result["sandbox"])
			if json.Unmarshal(result["cwd"], &directory) != nil || directory != cwd || json.Unmarshal(result["approvalPolicy"], &approval) != nil || approval != "never" || policyErr != nil || string(policy["type"]) != `"readOnly"` || string(policy["networkAccess"]) != "false" {
				return "", harness.ErrProtocol
			}
			responseID, err = threadIdentity(result["thread"], cwd)
			if err != nil {
				return "", err
			}
		} else {
			var method string
			// The pinned server adds emission timestamps to notifications.
			if stamp, ok := frame["emittedAtMs"]; ok {
				var milliseconds int64
				if json.Unmarshal(stamp, &milliseconds) != nil || milliseconds <= 0 {
					return "", harness.ErrProtocol
				}
				delete(frame, "emittedAtMs")
			}
			if len(frame) != 2 || json.Unmarshal(frame["method"], &method) != nil || frame["params"] == nil {
				return "", harness.ErrProtocol
			}
			switch method {
			case "thread/started":
				params, err := object(frame["params"])
				if err != nil || notificationID != "" {
					return "", harness.ErrProtocol
				}
				notificationID, err = threadIdentity(params["thread"], cwd)
				if err != nil {
					return "", err
				}
			case "remoteControl/status/changed", "configWarning", "warning", "skills/changed", "mcpServer/startupStatus/updated":
				// Bounded startup hints are discarded, never logged or published.
			default:
				return "", harness.ErrProtocol
			}
		}
		if responseID != "" && notificationID != "" {
			if responseID != notificationID {
				return "", harness.ErrProtocol
			}
			c.threadID = responseID
			return responseID, nil
		}
	}
	return "", harness.ErrProtocol
}

func threadIdentity(raw []byte, cwd string) (string, error) {
	thread, err := object(raw)
	if err != nil {
		return "", harness.ErrProtocol
	}
	var id, directory, version string
	if json.Unmarshal(thread["id"], &id) != nil || !ValidThreadID(id) || json.Unmarshal(thread["cwd"], &directory) != nil || directory != cwd || json.Unmarshal(thread["cliVersion"], &version) != nil || version != Version || string(thread["ephemeral"]) != "false" {
		return "", harness.ErrProtocol
	}
	return id, nil
}
