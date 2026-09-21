//go:build !linux

package agentd

func Load() (Config, error)          { return Config{}, ErrConfig }
func CheckCredentialExposure() error { return ErrConfig }
