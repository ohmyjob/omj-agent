package config

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func Parse(r io.Reader) (Config, error) {
	cfg := Default()
	seen := map[string]bool{}
	scanner := bufio.NewScanner(r)

	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}

		key, value, found := strings.Cut(text, "=")
		if !found {
			return Config{}, fmt.Errorf("line %d: expected key = value", line)
		}

		key = strings.TrimSpace(key)
		value = unquote(strings.TrimSpace(value))

		if seen[key] {
			return Config{}, fmt.Errorf("line %d: duplicate key %q", line, key)
		}
		seen[key] = true

		if err := cfg.set(key, value); err != nil {
			return Config{}, fmt.Errorf("line %d: %w", line, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}

	return cfg, nil
}

func (c *Config) set(key, value string) error {
	var err error

	switch key {
	case "server_url":
		c.ServerURL = value
	case "machine_id":
		c.MachineID = value
	case "insecure_http":
		c.InsecureHTTP, err = strconv.ParseBool(value)
	case "log_level":
		c.LogLevel = value
	case "max_concurrent_runs":
		c.MaxConcurrentRuns, err = strconv.Atoi(value)
	case "max_timeout_seconds":
		c.MaxTimeoutSeconds, err = strconv.Atoi(value)
	case "max_output_bytes":
		c.MaxOutputBytes, err = strconv.ParseInt(value, 10, 64)
	case "run_as_allowed":
		c.RunAsAllowed = splitList(value)
	default:
		return fmt.Errorf("unknown key %q", key)
	}

	if err != nil {
		return fmt.Errorf("%s: invalid value %q", key, value)
	}

	return nil
}

// splitList keeps empty entries so that a stray comma is a validation error
// rather than a silently shortened allowlist.
func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}

	return parts
}

func unquote(value string) string {
	if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return value[1 : len(value)-1]
	}

	return value
}
