package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	config, err := Load(Options{LookupEnv: environment(nil)})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.Environment != EnvironmentDevelopment {
		t.Fatalf("Environment = %q", config.Environment)
	}
	if config.HTTP.ListenAddress != "127.0.0.1:8080" {
		t.Fatalf("ListenAddress = %q", config.HTTP.ListenAddress)
	}
	if config.HTTP.ReadHeaderTimeout.Duration() != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %v", config.HTTP.ReadHeaderTimeout.Duration())
	}
	if config.Database.URL.IsSet() {
		t.Fatal("database URL unexpectedly set")
	}
}

func TestLoadFileThenEnvironment(t *testing.T) {
	const fileSecret = "postgres://file-user:file-password@db/ar"
	const envSecret = "postgres://env-user:env-password@db/ar"
	file := `{
		"environment": "production",
		"http": {
			"listen_address": "0.0.0.0:9090",
			"read_header_timeout": "3s",
			"read_timeout": "20s",
			"write_timeout": "21s",
			"idle_timeout": "1m",
			"shutdown_timeout": "15s"
		},
		"database": {"url": "` + fileSecret + `"}
	}`

	config, err := Load(Options{
		FilePath: "config.json",
		ReadFile: func(path string) ([]byte, error) {
			if path != "config.json" {
				t.Fatalf("ReadFile path = %q", path)
			}
			return []byte(file), nil
		},
		LookupEnv: environment(map[string]string{
			EnvListenAddress: "[::]:8081",
			EnvReadTimeout:   "9s",
			EnvDatabaseURL:   envSecret,
		}),
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.HTTP.ListenAddress != "[::]:8081" {
		t.Fatalf("ListenAddress = %q", config.HTTP.ListenAddress)
	}
	if config.HTTP.ReadTimeout.Duration() != 9*time.Second {
		t.Fatalf("ReadTimeout = %v", config.HTTP.ReadTimeout.Duration())
	}
	if config.HTTP.WriteTimeout.Duration() != 21*time.Second {
		t.Fatalf("WriteTimeout = %v", config.HTTP.WriteTimeout.Duration())
	}
	if config.Database.URL.Reveal() != envSecret {
		t.Fatal("environment did not replace file secret")
	}
	if strings.Contains(fmt.Sprintf("%v %#v", config.Database.URL, config.Database.URL), envSecret) {
		t.Fatal("formatted secret leaked")
	}
}

func TestLoadConfigFileFromEnvironment(t *testing.T) {
	var readPath string
	_, err := Load(Options{
		LookupEnv: environment(map[string]string{EnvConfigFile: "operator.json"}),
		ReadFile: func(path string) ([]byte, error) {
			readPath = path
			return []byte(`{}`), nil
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if readPath != "operator.json" {
		t.Fatalf("ReadFile path = %q", readPath)
	}
}

func TestLoadRejectsInvalidInputsWithoutLeakingSecret(t *testing.T) {
	tests := map[string]Options{
		"unknown file field": {
			FilePath: "config.json",
			ReadFile: func(string) ([]byte, error) {
				return []byte(`{"unexpected":"value"}`), nil
			},
			LookupEnv: environment(nil),
		},
		"trailing file data": {
			FilePath:  "config.json",
			ReadFile:  func(string) ([]byte, error) { return []byte(`{} {}`), nil },
			LookupEnv: environment(nil),
		},
		"oversized file": {
			FilePath: "config.json",
			ReadFile: func(string) ([]byte, error) {
				return make([]byte, maxConfigFileBytes+1), nil
			},
			LookupEnv: environment(nil),
		},
		"invalid environment duration": {
			LookupEnv: environment(map[string]string{EnvReadTimeout: "not-a-duration"}),
		},
		"zero duration": {
			LookupEnv: environment(map[string]string{EnvReadTimeout: "0s"}),
		},
		"unknown environment": {
			LookupEnv: environment(map[string]string{EnvEnvironment: "staging"}),
		},
		"invalid database URL": {
			LookupEnv: environment(map[string]string{EnvDatabaseURL: "https://db.example/ar"}),
		},
	}

	for name, options := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(options)
			if err == nil {
				t.Fatal("Load() error = nil")
			}
		})
	}

	const secret = "do-not-print-this-password"
	_, err := Load(Options{
		FilePath: "config.json",
		ReadFile: func(string) ([]byte, error) {
			return nil, errors.New("read failed")
		},
		LookupEnv: environment(map[string]string{EnvDatabaseURL: secret}),
	})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe error = %v", err)
	}
}

func TestProductionValidationFailsClosed(t *testing.T) {
	tests := map[string]map[string]string{
		"loopback listener": {
			EnvEnvironment: string(EnvironmentProduction),
			EnvDatabaseURL: "postgres://user:password@db/ar",
		},
		"missing database": {
			EnvEnvironment:   string(EnvironmentProduction),
			EnvListenAddress: "0.0.0.0:8080",
		},
	}
	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(Options{LookupEnv: environment(values)}); err == nil {
				t.Fatal("Load() error = nil")
			}
		})
	}
}

func TestSecretJSONAndExactValueRedaction(t *testing.T) {
	const secret = "postgres://user:unique-canary@db/ar"
	config := Default()
	config.Database.URL = NewSecret(secret)

	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), secret) || !strings.Contains(string(encoded), "[REDACTED]") {
		t.Fatalf("unsafe JSON = %s", encoded)
	}

	redacted := config.RedactText("connection failed for " + secret + ": unique-canary")
	if strings.Contains(redacted, secret) || strings.Contains(redacted, "unique-canary") || redacted != "connection failed for [REDACTED]: [REDACTED]" {
		t.Fatalf("RedactText() = %q", redacted)
	}
}

func environment(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
