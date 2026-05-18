package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		GRPCPort int `yaml:"grpc_port"`
	} `yaml:"server"`

	Limiter LimiterConfig `yaml:"limiter"`
}

type LimiterConfig struct {
	Algorithm string `yaml:"algorithm"`

	TokenBucket struct {
		Capacity   float64 `yaml:"capacity"`
		RefillRate float64 `yaml:"refill_rate"`
	} `yaml:"token_bucket"`

	FixedWindow struct {
		Limit         int64 `yaml:"limit"`
		WindowSeconds int64 `yaml:"window_seconds"`
	} `yaml:"fixed_window"`

	SlidingWindow struct {
		Limit         int64 `yaml:"limit"`
		WindowSeconds int64 `yaml:"window_seconds"`
	} `yaml:"sliding_window"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
