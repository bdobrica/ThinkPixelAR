// Package agentd owns sandbox-local supervisor startup. It has no authority or
// Kubernetes dependency; all configuration is untrusted until AR binds it.
package agentd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
)

const BootstrapRoot = "/run/thinkpixel/bootstrap"
const MaxConfigBytes = 64 << 10

var ErrConfig = errors.New("agentd configuration rejected")

type HarnessConfig struct {
	Argv             []string `json:"argv"`
	WorkingDirectory string   `json:"working_directory"`
	StartTimeoutMS   uint32   `json:"start_timeout_ms"`
	StopGraceMS      uint32   `json:"stop_grace_ms"`
	KillWaitMS       uint32   `json:"kill_wait_ms"`
}

// Config contains only non-secret startup metadata. Transport material is read
// separately from fixed bootstrap files by the authenticated transport adapter.
type Config struct {
	Version              uint32                 `json:"version"`
	Endpoint             string                 `json:"endpoint"`
	ServerName           string                 `json:"server_name"`
	Binding              *agentdv1.Binding      `json:"binding"`
	BuildDigest          string                 `json:"build_digest"`
	AdapterKind          string                 `json:"adapter_kind"`
	AdapterDigest        string                 `json:"adapter_digest"`
	Protocol             *agentdv1.VersionRange `json:"protocol"`
	Capabilities         []string               `json:"capabilities"`
	RequiredCapabilities []string               `json:"required_capabilities"`
	Limits               *agentdv1.Limits       `json:"limits"`
	Harness              HarnessConfig          `json:"harness"`
}

// DecodeConfig rejects duplicate keys, unknown fields, trailing values, nulls
// and excessive nesting before applying semantic validation. Errors are closed.
func DecodeConfig(raw []byte) (Config, error) {
	fail := Config{}
	if len(raw) == 0 || len(raw) > MaxConfigBytes {
		return fail, ErrConfig
	}
	check := json.NewDecoder(bytes.NewReader(raw))
	if jsonValue(check, 0) != nil {
		return fail, ErrConfig
	}
	if _, err := check.Token(); err != io.EOF {
		return fail, ErrConfig
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil || c.Validate() != nil {
		return fail, ErrConfig
	}
	return c, nil
}
func (c Config) Validate() error {
	if c.Version != 1 {
		return ErrConfig
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(c.Endpoint, "#") || strings.HasSuffix(u.Host, ":") || u.Fragment != "" || u.RawPath != "" || u.Path != "" || u.Hostname() != c.ServerName || !dnsName(c.ServerName) || net.ParseIP(c.ServerName) != nil {
		return ErrConfig
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return ErrConfig
		}
	}
	// Reuse version/identity/capability/limit validation with a synthetic challenge.
	// This performs no authentication and is never used as a transport proof.
	nonce := make([]byte, 32)
	h := &agentdv1.Hello{Versions: c.Protocol, Binding: c.Binding, Challenge: nonce, BuildDigest: c.BuildDigest, AdapterKind: c.AdapterKind, AdapterDigest: c.AdapterDigest, SupportedCapabilities: c.Capabilities, RequiredCapabilities: c.RequiredCapabilities, Limits: c.Limits}
	e := protocol.Expected{Binding: c.Binding, Challenge: nonce, BuildDigest: c.BuildDigest, AdapterKind: c.AdapterKind, AdapterDigest: c.AdapterDigest, SupportedCapabilities: c.Capabilities, RequiredCapabilities: c.RequiredCapabilities, Limits: c.Limits}
	if c.Binding == nil {
		return ErrConfig
	}
	if _, err := protocol.Negotiate(h, e, c.Binding.SandboxBindingId, 1); err != nil {
		return ErrConfig
	}
	hc := c.Harness
	if len(hc.Argv) == 0 || len(hc.Argv) > 32 || hc.WorkingDirectory != "/workspace" || hc.StartTimeoutMS == 0 || hc.StartTimeoutMS > 60000 || hc.StopGraceMS == 0 || hc.StopGraceMS > 30000 || hc.KillWaitMS == 0 || hc.KillWaitMS > 5000 {
		return ErrConfig
	}
	exe := hc.Argv[0]
	if path.Clean(exe) != exe || (!strings.HasPrefix(exe, "/usr/bin/") && !strings.HasPrefix(exe, "/usr/local/bin/")) {
		return ErrConfig
	}
	switch path.Base(exe) {
	case "sh", "bash", "dash", "ash", "zsh", "ksh", "fish", "env", "cmd", "powershell", "pwsh":
		return ErrConfig
	}
	total := 0
	for _, arg := range hc.Argv {
		if len(arg) > 4096 || strings.ContainsAny(arg, "\x00\r\n") {
			return ErrConfig
		}
		total += len(arg)
	}
	if total > 16<<10 {
		return ErrConfig
	}
	return nil
}
func dnsName(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
func jsonValue(d *json.Decoder, depth int) error {
	if depth > 12 {
		return ErrConfig
	}
	t, err := d.Token()
	if err != nil || t == nil {
		return ErrConfig
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			s, ok := key.(string)
			if err != nil || !ok || s != strings.ToLower(s) || seen[s] {
				return ErrConfig
			}
			seen[s] = true
			if jsonValue(d, depth+1) != nil {
				return ErrConfig
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return ErrConfig
		}
	case '[':
		for d.More() {
			if jsonValue(d, depth+1) != nil {
				return ErrConfig
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return ErrConfig
		}
	default:
		return ErrConfig
	}
	return nil
}
