package config

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed gameConfig.json
var embeddedConfig []byte

// loadEmbeddedConfig loads the embedded gameConfig.json file
func loadEmbeddedConfig() (*JSONConfig, error) {
	var config JSONConfig
	err := json.Unmarshal(embeddedConfig, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse embedded config file: %w", err)
	}
	return &config, nil
}
