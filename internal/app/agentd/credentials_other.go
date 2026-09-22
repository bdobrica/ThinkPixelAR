//go:build !linux

package agentd

func LoadTransport() (*TransportBootstrap, error) { return nil, ErrConfig }
