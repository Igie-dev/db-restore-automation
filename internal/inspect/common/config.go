package common

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Path string
	Dir  string
	Root map[string]any
	Jobs []Job
}

type Job struct {
	Data    map[string]any
	Name    string
	Type    string
	Enabled bool
}

func EmptyConfig() *Config {
	return &Config{Dir: ".", Root: map[string]any{}}
}

func LoadConfig(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("config path is required")
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}

	contents, err := os.ReadFile(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", absolutePath, err)
	}

	var root map[string]any
	decoder := yaml.NewDecoder(strings.NewReader(string(contents)))
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", absolutePath, err)
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("config %q must contain exactly one YAML document", absolutePath)
		}
		return nil, fmt.Errorf("parse additional YAML content in %q: %w", absolutePath, err)
	}
	if len(root) == 0 {
		return nil, fmt.Errorf("config %q is empty", absolutePath)
	}

	config := &Config{
		Path: absolutePath,
		Dir:  filepath.Dir(absolutePath),
		Root: normalizeMap(root),
	}

	jobsValue, ok := Lookup(config.Root, "jobs")
	if !ok {
		return nil, fmt.Errorf("config %q does not contain a jobs collection", absolutePath)
	}
	jobItems, ok := AsSlice(jobsValue)
	if !ok {
		return nil, fmt.Errorf("config jobs must be a YAML sequence")
	}

	for index, item := range jobItems {
		jobMap, ok := AsStringMap(item)
		if !ok {
			return nil, fmt.Errorf("config jobs[%d] must be an object", index)
		}

		name := FirstString(jobMap, "name", "job_name")
		if name == "" {
			name = fmt.Sprintf("job-%d", index+1)
		}

		config.Jobs = append(config.Jobs, Job{
			Data:    jobMap,
			Name:    name,
			Type:    NormalizeProviderType(FirstString(jobMap, "type", "provider", "provider_type")),
			Enabled: FirstBoolDefault(jobMap, true, "enabled"),
		})
	}

	return config, nil
}

func normalizeMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[strings.ToLower(strings.TrimSpace(key))] = normalizeValue(value)
	}
	return output
}

func normalizeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return normalizeMap(typed)
	case map[any]any:
		output := make(map[string]any, len(typed))
		for key, item := range typed {
			output[strings.ToLower(strings.TrimSpace(fmt.Sprint(key)))] = normalizeValue(item)
		}
		return output
	case []any:
		output := make([]any, len(typed))
		for index, item := range typed {
			output[index] = normalizeValue(item)
		}
		return output
	default:
		return value
	}
}

func AsStringMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return normalizeMap(typed), true
	case map[any]any:
		output := make(map[string]any, len(typed))
		for key, item := range typed {
			output[strings.ToLower(strings.TrimSpace(fmt.Sprint(key)))] = normalizeValue(item)
		}
		return output, true
	default:
		return nil, false
	}
}

func AsSlice(value any) ([]any, bool) {
	items, ok := value.([]any)
	return items, ok
}

func Lookup(data map[string]any, path string) (any, bool) {
	current := any(data)
	for _, part := range strings.Split(path, ".") {
		part = strings.ToLower(strings.TrimSpace(part))
		object, ok := AsStringMap(current)
		if !ok {
			return nil, false
		}
		value, exists := object[part]
		if !exists {
			return nil, false
		}
		current = value
	}
	return current, true
}

func FirstValue(data map[string]any, paths ...string) (any, bool) {
	for _, path := range paths {
		if value, ok := Lookup(data, path); ok {
			return value, true
		}
	}
	return nil, false
}

func FirstString(data map[string]any, paths ...string) string {
	value, ok := FirstValue(data, paths...)
	if !ok || value == nil {
		return ""
	}

	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case int, int32, int64, uint, uint32, uint64, float32, float64, bool:
		return strings.TrimSpace(fmt.Sprint(typed))
	default:
		return ""
	}
}

func FirstBoolDefault(data map[string]any, fallback bool, paths ...string) bool {
	value, ok := FirstValue(data, paths...)
	if !ok || value == nil {
		return fallback
	}

	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	}
	return fallback
}

func FirstIntDefault(data map[string]any, fallback int, paths ...string) int {
	value, ok := FirstValue(data, paths...)
	if !ok || value == nil {
		return fallback
	}

	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func FirstStringSlice(data map[string]any, paths ...string) []string {
	value, ok := FirstValue(data, paths...)
	if !ok || value == nil {
		return nil
	}

	items, ok := AsSlice(value)
	if !ok {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" {
			return nil
		}
		return []string{text}
	}

	result := make([]string, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(fmt.Sprint(item))
		if text != "" {
			result = append(result, text)
		}
	}
	return result
}

func (c *Config) Tool(providerType string, names ...string) string {
	providerType = NormalizeProviderType(providerType)
	paths := make([]string, 0, len(names)*4)
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		paths = append(paths,
			"tools."+providerType+"."+name,
			"tools."+name,
			providerType+".tools."+name,
		)
	}
	return FirstString(c.Root, paths...)
}

func (c *Config) ResolvePath(path string) string {
	path = strings.TrimSpace(os.ExpandEnv(path))
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	base := c.Dir
	if base == "" {
		base = "."
	}
	return filepath.Clean(filepath.Join(base, path))
}

func (j Job) Value(paths ...string) string {
	return FirstString(j.Data, paths...)
}

func (j Job) BoolValue(fallback bool, paths ...string) bool {
	return FirstBoolDefault(j.Data, fallback, paths...)
}

func (j Job) IntValue(fallback int, paths ...string) int {
	return FirstIntDefault(j.Data, fallback, paths...)
}

func (j Job) StringSlice(paths ...string) []string {
	return FirstStringSlice(j.Data, paths...)
}

func (j Job) Section(paths ...string) map[string]any {
	value, ok := FirstValue(j.Data, paths...)
	if !ok {
		return nil
	}
	result, _ := AsStringMap(value)
	return result
}
