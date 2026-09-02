package main

import (
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"
)

type pluginConfig struct {
	Enabled bool `yaml:"enabled"`
}

type configState struct {
	mu      sync.RWMutex
	enabled bool
}

var configured configState

func configure(raw []byte) error {
	cfg := pluginConfig{}
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("decode plugin config: %w", err)
		}
	}
	configured.mu.Lock()
	configured.enabled = cfg.Enabled
	configured.mu.Unlock()
	return nil
}

func pluginEnabled() bool {
	configured.mu.RLock()
	defer configured.mu.RUnlock()
	return configured.enabled
}

func resetConfig() {
	configured.mu.Lock()
	configured.enabled = false
	configured.mu.Unlock()
}
