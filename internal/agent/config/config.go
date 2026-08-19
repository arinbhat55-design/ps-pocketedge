// Package config loads the agent's YAML config file, written by
// scripts/install-agent.sh on real hosts. CLI flags remain available for
// local development so a config file isn't required to run the agent.
package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    string `yaml:"server"`
	Token     string `yaml:"token"`
	StatePath string `yaml:"state_path"`
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
