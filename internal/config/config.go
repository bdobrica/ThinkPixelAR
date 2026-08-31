// Package config loads and validates trusted operator configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	maxConfigFileBytes    = 1 << 20
	maxDatabaseURLBytes   = 8 << 10
	maxListenAddressBytes = 255

	EnvConfigFile        = "THINKPIXELAR_CONFIG_FILE"
	EnvEnvironment       = "THINKPIXELAR_ENVIRONMENT"
	EnvListenAddress     = "THINKPIXELAR_HTTP_LISTEN_ADDRESS"
	EnvReadHeaderTimeout = "THINKPIXELAR_HTTP_READ_HEADER_TIMEOUT"
	EnvReadTimeout       = "THINKPIXELAR_HTTP_READ_TIMEOUT"
	EnvWriteTimeout      = "THINKPIXELAR_HTTP_WRITE_TIMEOUT"
	EnvIdleTimeout       = "THINKPIXELAR_HTTP_IDLE_TIMEOUT"
	EnvShutdownTimeout   = "THINKPIXELAR_HTTP_SHUTDOWN_TIMEOUT"
	EnvDatabaseURL       = "THINKPIXELAR_DATABASE_URL"
)

// Environment selects validation rules appropriate to the deployment.
type Environment string

const (
	EnvironmentDevelopment Environment = "development"
	EnvironmentTest        Environment = "test"
	EnvironmentProduction  Environment = "production"
)

// Duration is a duration that is represented as a string in configuration files.
type Duration time.Duration

func (d Duration) Duration() time.Duration { return time.Duration(d) }

func (d *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("duration must be a string")
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return errors.New("invalid duration")
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// Secret is a configuration value that redacts itself from text and JSON output.
// Reveal must be used explicitly at the narrow boundary that consumes the secret.
type Secret struct{ value string }

func NewSecret(value string) Secret { return Secret{value: value} }
func (s Secret) Reveal() string     { return s.value }
func (s Secret) IsSet() bool        { return s.value != "" }
func (s Secret) String() string     { return redactedValue(s.value) }
func (s Secret) GoString() string   { return s.String() }
func (s Secret) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

func (s *Secret) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("secret must be a string")
	}
	s.value = value
	return nil
}

func redactedValue(value string) string {
	if value == "" {
		return ""
	}
	return "[REDACTED]"
}

// Config is the process-level configuration owned by ThinkPixelAR.
type Config struct {
	Environment Environment    `json:"environment"`
	HTTP        HTTPConfig     `json:"http"`
	Database    DatabaseConfig `json:"database"`
}

type HTTPConfig struct {
	ListenAddress     string   `json:"listen_address"`
	ReadHeaderTimeout Duration `json:"read_header_timeout"`
	ReadTimeout       Duration `json:"read_timeout"`
	WriteTimeout      Duration `json:"write_timeout"`
	IdleTimeout       Duration `json:"idle_timeout"`
	ShutdownTimeout   Duration `json:"shutdown_timeout"`
}

type DatabaseConfig struct {
	URL Secret `json:"url"`
}

// Default returns conservative local-development defaults. Production
// validation rejects the loopback listener and missing database URL.
func Default() Config {
	return Config{
		Environment: EnvironmentDevelopment,
		HTTP: HTTPConfig{
			ListenAddress:     "127.0.0.1:8080",
			ReadHeaderTimeout: Duration(5 * time.Second),
			ReadTimeout:       Duration(30 * time.Second),
			WriteTimeout:      Duration(30 * time.Second),
			IdleTimeout:       Duration(2 * time.Minute),
			ShutdownTimeout:   Duration(30 * time.Second),
		},
	}
}

// Options supplies trusted I/O seams. Empty functions use the operating system.
type Options struct {
	FilePath  string
	LookupEnv func(string) (string, bool)
	ReadFile  func(string) ([]byte, error)
}

// Load applies defaults, then an optional JSON file, then environment overrides.
// A FilePath option takes precedence over THINKPIXELAR_CONFIG_FILE.
func Load(options Options) (Config, error) {
	lookupEnv := options.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	readFile := options.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}

	result := Default()
	path := options.FilePath
	if path == "" {
		path, _ = lookupEnv(EnvConfigFile)
	}
	if path != "" {
		data, err := readFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read configuration file: %w", err)
		}
		if len(data) > maxConfigFileBytes {
			return Config{}, errors.New("configuration file exceeds 1 MiB")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&result); err != nil {
			return Config{}, fmt.Errorf("decode configuration file: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return Config{}, errors.New("decode configuration file: trailing data")
		}
	}

	if err := applyEnvironment(&result, lookupEnv); err != nil {
		return Config{}, err
	}
	if err := result.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate configuration: %w", err)
	}
	return result, nil
}

func applyEnvironment(config *Config, lookup func(string) (string, bool)) error {
	stringValue(lookup, EnvEnvironment, func(value string) { config.Environment = Environment(value) })
	stringValue(lookup, EnvListenAddress, func(value string) { config.HTTP.ListenAddress = value })
	stringValue(lookup, EnvDatabaseURL, func(value string) { config.Database.URL = NewSecret(value) })

	durations := []struct {
		name   string
		target *Duration
	}{
		{EnvReadHeaderTimeout, &config.HTTP.ReadHeaderTimeout},
		{EnvReadTimeout, &config.HTTP.ReadTimeout},
		{EnvWriteTimeout, &config.HTTP.WriteTimeout},
		{EnvIdleTimeout, &config.HTTP.IdleTimeout},
		{EnvShutdownTimeout, &config.HTTP.ShutdownTimeout},
	}
	for _, item := range durations {
		if value, ok := lookup(item.name); ok {
			parsed, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("parse %s: invalid duration", item.name)
			}
			*item.target = Duration(parsed)
		}
	}
	return nil
}

func stringValue(lookup func(string) (string, bool), name string, set func(string)) {
	if value, ok := lookup(name); ok {
		set(value)
	}
}

// Validate rejects ambiguous or unsafe configurations.
func (c Config) Validate() error {
	switch c.Environment {
	case EnvironmentDevelopment, EnvironmentTest, EnvironmentProduction:
	default:
		return errors.New("environment must be development, test, or production")
	}

	host, port, err := net.SplitHostPort(c.HTTP.ListenAddress)
	if len(c.HTTP.ListenAddress) > maxListenAddressBytes {
		return errors.New("http.listen_address is too long")
	}
	if err != nil || port == "" {
		return errors.New("http.listen_address must be a host:port")
	}
	if parsedPort, err := strconv.ParseUint(port, 10, 16); err != nil || parsedPort == 0 {
		return errors.New("http.listen_address must use a valid non-zero port")
	}
	if c.Environment == EnvironmentProduction {
		ip := net.ParseIP(host)
		if host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return errors.New("production http.listen_address must not be loopback")
		}
		if !c.Database.URL.IsSet() {
			return errors.New("database.url is required in production")
		}
	}
	if c.Database.URL.IsSet() {
		if len(c.Database.URL.Reveal()) > maxDatabaseURLBytes {
			return errors.New("database.url is too long")
		}
		databaseURL, err := url.Parse(c.Database.URL.Reveal())
		if err != nil || (databaseURL.Scheme != "postgres" && databaseURL.Scheme != "postgresql") || databaseURL.Host == "" || databaseURL.Path == "" || databaseURL.Path == "/" || databaseURL.Fragment != "" {
			return errors.New("database.url must be a postgres URL with host and database name")
		}
	}

	durations := []struct {
		name  string
		value Duration
	}{
		{"http.read_header_timeout", c.HTTP.ReadHeaderTimeout},
		{"http.read_timeout", c.HTTP.ReadTimeout},
		{"http.write_timeout", c.HTTP.WriteTimeout},
		{"http.idle_timeout", c.HTTP.IdleTimeout},
		{"http.shutdown_timeout", c.HTTP.ShutdownTimeout},
	}
	for _, item := range durations {
		if item.value <= 0 {
			return fmt.Errorf("%s must be positive", item.name)
		}
		if time.Duration(item.value) > 10*time.Minute {
			return fmt.Errorf("%s must not exceed 10m", item.name)
		}
	}
	return nil
}

// RedactText removes exact loaded secret values from a diagnostic string.
func (c Config) RedactText(value string) string {
	if c.Database.URL.IsSet() {
		value = strings.ReplaceAll(value, c.Database.URL.Reveal(), "[REDACTED]")
		if databaseURL, err := url.Parse(c.Database.URL.Reveal()); err == nil && databaseURL.User != nil {
			if password, ok := databaseURL.User.Password(); ok && password != "" {
				value = strings.ReplaceAll(value, password, "[REDACTED]")
			}
		}
	}
	return value
}
