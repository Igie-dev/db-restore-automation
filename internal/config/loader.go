package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxRestoreConfigBytes int64 = 10 * 1024 * 1024

func Load(path string) (Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Config{}, fmt.Errorf(
			"config path is required",
		)
	}

	raw, err := readRestoreConfigFile(path)
	if err != nil {
		return Config{}, err
	}

	if len(bytes.TrimSpace(raw)) == 0 {
		return Config{}, fmt.Errorf(
			"config file is empty: %q",
			path,
		)
	}

	// Decode into a YAML node first so security-related fields can be
	// inspected before the document is converted into Config.
	var root yaml.Node

	if err := decodeSingleYAMLDocument(raw, &root, false); err != nil {
		return Config{}, fmt.Errorf(
			"config file is not valid YAML: %w",
			err,
		)
	}

	if err := validateConfigRootNode(&root); err != nil {
		return Config{}, err
	}

	if hasForbiddenPasswordField(&root, "") {
		return Config{}, fmt.Errorf(
			"passwords must not be stored in restore job YAML; use pgpass, MySQL login path/defaults file, Oracle Wallet, or Dell PowerProtect lockbox",
		)
	}

	// Decode a second time with KnownFields enabled. This prevents misspelled
	// or unsupported YAML fields from being silently ignored.
	var cfg Config

	if err := decodeSingleYAMLDocument(raw, &cfg, true); err != nil {
		return Config{}, fmt.Errorf(
			"decode config: %w",
			err,
		)
	}

	return cfg, nil
}

func readRestoreConfigFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf(
			"read config %q: %w",
			path,
			err,
		)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf(
			"inspect config file %q: %w",
			path,
			err,
		)
	}

	if !info.Mode().IsRegular() {
		_ = file.Close()

		return nil, fmt.Errorf(
			"config path is not a regular file: %q",
			path,
		)
	}

	if info.Size() > maxRestoreConfigBytes {
		_ = file.Close()

		return nil, fmt.Errorf(
			"config file %q is too large: size=%d maximum=%d",
			path,
			info.Size(),
			maxRestoreConfigBytes,
		)
	}

	raw, readErr := io.ReadAll(
		io.LimitReader(file, maxRestoreConfigBytes+1),
	)

	closeErr := file.Close()

	if readErr != nil {
		return nil, fmt.Errorf(
			"read config %q: %w",
			path,
			readErr,
		)
	}

	if closeErr != nil {
		return nil, fmt.Errorf(
			"close config file %q: %w",
			path,
			closeErr,
		)
	}

	// The file may have grown after Stat was called, so enforce the limit
	// again using the actual number of bytes read.
	if int64(len(raw)) > maxRestoreConfigBytes {
		return nil, fmt.Errorf(
			"config file %q is too large: maximum=%d",
			path,
			maxRestoreConfigBytes,
		)
	}

	return raw, nil
}

func decodeSingleYAMLDocument(
	raw []byte,
	target any,
	knownFields bool,
) error {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(knownFields)

	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf(
				"config file does not contain a YAML document",
			)
		}

		return err
	}

	// Configuration files must contain exactly one YAML document. Without
	// this check, content after a second "---" separator could be ignored.
	var extraDocument yaml.Node

	err := decoder.Decode(&extraDocument)
	switch {
	case errors.Is(err, io.EOF):
		return nil

	case err != nil:
		return fmt.Errorf(
			"read additional YAML document: %w",
			err,
		)

	default:
		return fmt.Errorf(
			"multiple YAML documents are not allowed",
		)
	}
}

func validateConfigRootNode(root *yaml.Node) error {
	if root == nil {
		return fmt.Errorf(
			"config file does not contain a YAML document",
		)
	}

	if root.Kind != yaml.DocumentNode {
		return fmt.Errorf(
			"config YAML root must be a document",
		)
	}

	if len(root.Content) != 1 {
		return fmt.Errorf(
			"config YAML document must contain exactly one root value",
		)
	}

	rootValue := root.Content[0]
	if rootValue == nil {
		return fmt.Errorf(
			"config YAML document has no root value",
		)
	}

	if rootValue.Kind != yaml.MappingNode {
		return fmt.Errorf(
			"config YAML root must be a mapping/object",
		)
	}

	return nil
}
