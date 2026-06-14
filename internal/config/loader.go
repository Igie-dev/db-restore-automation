package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return Config{}, fmt.Errorf("config file is not valid YAML: %w", err)
	}

	var cfg Config
	if err := root.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	if hasForbiddenPasswordField(&root, "") {
		return Config{}, fmt.Errorf("passwords must not be stored in restore job YAML; use pgpass, MySQL login path/defaults file, Oracle Wallet, or Dell PowerProtect lockbox")
	}

	return cfg, nil
}

