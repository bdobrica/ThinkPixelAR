package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
)

const Kind = "codex-app-server"
const maxHandshakeBytes = 64 << 10

// Command is the pinned sandbox command. Bootstrap cannot add config overrides,
// alternate listeners or shell wrappers to this initial startup path.
func Command() []string {
	return []string{"/usr/local/bin/codex", "app-server", "--listen", "stdio://", "-c", "check_for_update_on_startup=false"}
}

// Client owns one supervised child's protocol pipes, not its process or authority.
// The supervisor must stop the child after a failed handshake. Raw frames and
// initialization metadata never become diagnostics or canonical runtime events.
type Client struct {
	gate                sync.Mutex
	closeOnce           sync.Once
	in                  io.WriteCloser
	out                 io.ReadCloser
	reader              *bufio.Reader
	attempted           bool
	initialized         bool
	threadAttempted     bool
	threadID, threadCWD string
}

// NewClient requires pipes whose Close interrupts pending reads/writes.
// The retained reader preserves any read-ahead for subsequent protocol work.
func NewClient(in io.WriteCloser, out io.ReadCloser) *Client {
	return &Client{in: in, out: out, reader: bufio.NewReaderSize(out, maxHandshakeBytes)}
}

func (*Client) String() string     { return "[restricted Codex protocol]" }
func (c *Client) GoString() string { return c.String() }

// Close releases protocol descriptors; only agentd owns process termination.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		_ = c.in.Close()
		_ = c.out.Close()
	})
}

// Initialize performs the stable initialize/initialized exchange exactly once.
// Success is protocol readiness only: it creates no thread and grants no authority.
func (c *Client) Initialize(ctx context.Context) (err error) {
	if !c.gate.TryLock() {
		return harness.ErrConflict
	}
	defer c.gate.Unlock()
	if c.attempted {
		return harness.ErrConflict
	}
	c.attempted = true
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, c.Close)
	defer stop()
	defer func() {
		if ctx.Err() != nil {
			err = harness.ErrOutcomeUnknown
		}
		if err != nil {
			c.Close()
		}
	}()
	if ctx.Err() != nil {
		return harness.ErrOutcomeUnknown
	}
	const initialize = `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"thinkpixelar","version":"0.1.0"},"capabilities":{"experimentalApi":false}}}` + "\n"
	if _, err := io.WriteString(c.in, initialize); err != nil {
		return harness.ErrOutcomeUnknown
	}
	raw, err := c.reader.ReadSlice('\n')
	if err != nil {
		return harness.ErrProtocol
	}
	defer clear(raw)
	response, err := object(raw)
	if err != nil || len(response) != 2 || string(response["id"]) != "1" {
		return harness.ErrProtocol
	}
	result, err := object(response["result"])
	if err != nil {
		return harness.ErrProtocol
	}
	for key := range result {
		switch key {
		case "userAgent", "platformFamily", "platformOs", "codexHome":
		default:
			return harness.ErrProtocol
		}
	}
	var identity string
	if json.Unmarshal(result["userAgent"], &identity) != nil || len(identity) > 1024 {
		return harness.ErrProtocol
	}
	token, _, _ := strings.Cut(identity, " ")
	if token != "thinkpixelar/"+Version {
		return harness.ErrIncompatible
	}
	if _, err := io.WriteString(c.in, "{\"method\":\"initialized\",\"params\":{}}\n"); err != nil {
		return harness.ErrOutcomeUnknown
	}
	c.initialized = true
	return nil
}

// object rejects ambiguous duplicate keys and trailing values in the consumed
// envelope and result. Size is bounded before parsing by the JSONL reader.
func object(raw []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return nil, harness.ErrProtocol
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || fields[name] != nil {
			return nil, harness.ErrProtocol
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, harness.ErrProtocol
		}
		fields[name] = value
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') {
		return nil, harness.ErrProtocol
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, harness.ErrProtocol
	}
	return fields, nil
}
