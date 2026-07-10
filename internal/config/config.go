// Package config loads runtime configuration from environment variables.
//
// The package is environment-agnostic: Load accepts a getter function so it can
// be unit-tested without mutating the real process environment.
package config

import (
	"fmt"
	"strconv"
)

// Config holds all runtime settings for the agent binary.
type Config struct {
	GitHubToken  string
	LLMAPIKey    string
	LLMBaseURL   string
	LLMModel     string
	MaxIterations int
	LogLevel     string
	Port         int
}

// Defaults applied when the corresponding env var is unset.
const (
	DefaultLLMModel     = "glm-5.2"
	DefaultMaxIter      = 10
	DefaultLogLevel     = "INFO"
	DefaultPort         = 8080
)

// Load reads configuration using getenv (typically os.LookupEnv). Required
// fields (GITHUB_TOKEN, LLM_API_KEY, LLM_BASE_URL) produce an error when
// missing; callers that can tolerate an absent GITHUB_TOKEN (test mode without
// read calls) may ignore that error.
func Load(getenv func(string) (string, bool)) (Config, error) {
	var cfg Config
	var missing []string

	if v, ok := getenv("GITHUB_TOKEN"); ok && v != "" {
		cfg.GitHubToken = v
	} else {
		missing = append(missing, "GITHUB_TOKEN")
	}

	if v, ok := getenv("LLM_API_KEY"); ok && v != "" {
		cfg.LLMAPIKey = v
	} else {
		missing = append(missing, "LLM_API_KEY")
	}

	if v, ok := getenv("LLM_BASE_URL"); ok && v != "" {
		cfg.LLMBaseURL = v
	} else {
		missing = append(missing, "LLM_BASE_URL")
	}

	if v, ok := getenv("LLM_MODEL"); ok && v != "" {
		cfg.LLMModel = v
	} else {
		cfg.LLMModel = DefaultLLMModel
	}

	if v, ok := getenv("MAX_ITERATIONS"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("MAX_ITERATIONS must be a positive integer, got %q", v)
		}
		cfg.MaxIterations = n
	} else {
		cfg.MaxIterations = DefaultMaxIter
	}

	if v, ok := getenv("LOG_LEVEL"); ok && v != "" {
		cfg.LogLevel = v
	} else {
		cfg.LogLevel = DefaultLogLevel
	}

	if v, ok := getenv("PORT"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return Config{}, fmt.Errorf("PORT must be a valid TCP port, got %q", v)
		}
		cfg.Port = n
	} else {
		cfg.Port = DefaultPort
	}

	if len(missing) > 0 {
		return cfg, fmt.Errorf("required environment variables not set: %v", missing)
	}
	return cfg, nil
}
