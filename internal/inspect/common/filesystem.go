package common

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func InspectFile(report *JobReport, name, path string, required bool) bool {
	path = strings.TrimSpace(os.ExpandEnv(path))
	if path == "" {
		if required {
			report.Fail(name, "file path is not configured", "")
		} else {
			report.Info(name, "file is not configured", "")
		}
		return false
	}

	info, err := os.Stat(path)
	if err != nil {
		if required {
			report.Fail(name, fmt.Sprintf("file is not accessible: %v", err), path)
		} else {
			report.Warn(name, fmt.Sprintf("optional file is not accessible: %v", err), path)
		}
		return false
	}
	if info.IsDir() {
		report.Fail(name, "expected a file but found a directory", path)
		return false
	}

	file, err := os.Open(path)
	if err != nil {
		report.Fail(name, fmt.Sprintf("file is not readable: %v", err), path)
		return false
	}
	_ = file.Close()
	report.Pass(name, "file exists and is readable", path)
	return true
}

func InspectDirectory(report *JobReport, name, path string, required bool, writable bool) bool {
	path = strings.TrimSpace(os.ExpandEnv(path))
	if path == "" {
		if required {
			report.Fail(name, "directory path is not configured", "")
		} else {
			report.Info(name, "directory is not configured", "")
		}
		return false
	}

	info, err := os.Stat(path)
	if err != nil {
		if required {
			report.Fail(name, fmt.Sprintf("directory is not accessible: %v", err), path)
		} else {
			report.Warn(name, fmt.Sprintf("optional directory is not accessible: %v", err), path)
		}
		return false
	}
	if !info.IsDir() {
		report.Fail(name, "expected a directory but found a file", path)
		return false
	}

	if writable {
		testFile, err := os.CreateTemp(path, ".inspect-write-test-*")
		if err != nil {
			report.Fail(name, fmt.Sprintf("directory is not writable: %v", err), path)
			return false
		}
		testPath := testFile.Name()
		_ = testFile.Close()
		_ = os.Remove(testPath)
		report.Pass(name, "directory exists and is writable", path)
		return true
	}

	report.Pass(name, "directory exists", path)
	return true
}

func InspectName(report *JobReport, name, value string, pattern *regexp.Regexp, required bool) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		if required {
			report.Fail(name, "value is not configured", "")
		} else {
			report.Info(name, "value is not configured", "")
		}
		return false
	}
	if !pattern.MatchString(value) {
		report.Fail(name, fmt.Sprintf("value does not match required pattern %s", pattern.String()), value)
		return false
	}
	report.Pass(name, "value is valid", value)
	return true
}

func FindLatestBackup(directory, pattern string, allowedExtensions []string) (string, os.FileInfo, error) {
	directory = strings.TrimSpace(os.ExpandEnv(directory))
	if directory == "" {
		return "", nil, errors.New("backup directory is empty")
	}
	if pattern == "" {
		pattern = "*"
	}
	if filepath.Base(pattern) != pattern || strings.Contains(pattern, "..") {
		return "", nil, fmt.Errorf("backup pattern must be a simple filename pattern")
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", nil, err
	}

	allowed := make(map[string]struct{}, len(allowedExtensions))
	for _, extension := range allowedExtensions {
		allowed[strings.ToLower(extension)] = struct{}{}
	}

	type candidate struct {
		path string
		info os.FileInfo
	}
	var candidates []candidate
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matched, matchErr := filepath.Match(pattern, entry.Name())
		if matchErr != nil {
			return "", nil, fmt.Errorf("invalid backup pattern %q: %w", pattern, matchErr)
		}
		if !matched {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		if !HasAllowedExtension(path, allowed) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		candidates = append(candidates, candidate{path: path, info: info})
	}

	if len(candidates) == 0 {
		return "", nil, fmt.Errorf("no matching backup file found in %s", directory)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].info.ModTime().Equal(candidates[j].info.ModTime()) {
			return candidates[i].path > candidates[j].path
		}
		return candidates[i].info.ModTime().After(candidates[j].info.ModTime())
	})
	return candidates[0].path, candidates[0].info, nil
}

func HasAllowedExtension(path string, allowed map[string]struct{}) bool {
	lower := strings.ToLower(path)
	for extension := range allowed {
		if strings.HasSuffix(lower, extension) {
			return true
		}
	}
	return false
}

func InspectLatestBackup(report *JobReport, directory, pattern string, extensions []string) string {
	path, info, err := FindLatestBackup(directory, pattern, extensions)
	if err != nil {
		report.Fail("latest backup", err.Error(), directory)
		return ""
	}

	file, err := os.Open(path)
	if err != nil {
		report.Fail("latest backup", fmt.Sprintf("backup file is not readable: %v", err), path)
		return ""
	}
	_ = file.Close()

	report.Pass(
		"latest backup",
		fmt.Sprintf("latest matching backup; modified %s; size %d bytes", info.ModTime().Format("2006-01-02 15:04:05"), info.Size()),
		path,
	)
	return path
}

func DefaultPgpassPath() string {
	if configured := strings.TrimSpace(os.Getenv("PGPASSFILE")); configured != "" {
		return os.ExpandEnv(configured)
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		return filepath.Join(appData, "postgresql", "pgpass.conf")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pgpass")
}

func FirstExistingPath(paths ...string) string {
	for _, path := range paths {
		path = strings.TrimSpace(os.ExpandEnv(path))
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func ReadLinesLimited(ctx context.Context, path string, maxBytes int64, consume func(line string) bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	var reader io.Reader = file
	if maxBytes > 0 {
		reader = io.LimitReader(file, maxBytes)
	}

	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !consume(scanner.Text()) {
			return nil
		}
	}
	return scanner.Err()
}

func UniqueCandidateValues(candidates []Candidate, kind string) []string {
	seen := map[string]string{}
	for _, candidate := range candidates {
		if candidate.Kind != kind {
			continue
		}
		key := strings.ToLower(candidate.Value)
		if _, exists := seen[key]; !exists {
			seen[key] = candidate.Value
		}
	}
	values := make([]string, 0, len(seen))
	for _, value := range seen {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func ContainsEqualFold(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(expected)) {
			return true
		}
	}
	return false
}
