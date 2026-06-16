//go:build windows

package powerprotect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"db-restore-automation/internal/inspect/common"
)

var patterns = map[string][]*regexp.Regexp{
	"dd_host": {
		regexp.MustCompile(`(?i)(?:NSR_DFA_SI_DD_HOST|DEVICE_HOST|DD_HOST|--ddhost)\s*(?:=|:|\s)\s*["']?([^"'\s,;]+)`),
	},
	"dd_user": {
		regexp.MustCompile(`(?i)(?:NSR_DFA_SI_DD_USER|DDBOOST_USER|DD_USER|--dduser)\s*(?:=|:|\s)\s*["']?([^"'\s,;]+)`),
	},
	"device_path": {
		regexp.MustCompile(`(?i)(?:NSR_DFA_SI_DEVICE_PATH|DEVICE_PATH|DD_PATH|--ddpath)\s*(?:=|:|\s)\s*["']?([^"',;\s]+)`),
	},
	"client": {
		regexp.MustCompile(`(?i)(?:NSR_CLIENT|CLIENT|SOURCE_CLIENT)\s*(?:=|:)\s*["']?([^"'\s,;]+)`),
		regexp.MustCompile(`(?i)client\s+["']([^"']+)["']`),
	},
}

var allowedExtensions = map[string]struct{}{
	".log": {}, ".txt": {}, ".cfg": {}, ".conf": {}, ".ini": {},
	".xml": {}, ".json": {}, ".yaml": {}, ".yml": {}, ".out": {},
	".cmd": {}, ".bat": {}, ".ps1": {},
}

func inspectPlatform(ctx context.Context, job common.Job, options common.Options) (string, []string, []common.Candidate, []string) {
	fqdn := currentFQDN(ctx)
	instances := discoverSQLServerInstances(ctx)
	roots := searchRoots(job)
	candidates, warnings := scanCandidates(ctx, roots, options.MaxScanFileSize, options.MaxScanMatches)
	return fqdn, instances, candidates, warnings
}

func currentFQDN(ctx context.Context) string {
	command := `$c=Get-CimInstance Win32_ComputerSystem; if($c.PartOfDomain){"$($c.DNSHostName).$($c.Domain)"}else{$c.DNSHostName}`
	output, err := common.RunReadOnlyCommand(ctx, "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-Command", command}, nil, "")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

func discoverSQLServerInstances(ctx context.Context) []string {
	command := `Get-Service | Where-Object {$_.Name -eq 'MSSQLSERVER' -or $_.Name -like 'MSSQL$*'} | ForEach-Object { if($_.Name -eq 'MSSQLSERVER'){'MSSQLSERVER'}else{$_.Name.Substring(6)} }`
	output, err := common.RunReadOnlyCommand(ctx, "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-Command", command}, nil, "")
	if err != nil {
		return nil
	}
	var values []string
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			values = append(values, line)
		}
	}
	sort.Strings(values)
	return values
}

func searchRoots(job common.Job) []string {
	roots := job.StringSlice(common.ProviderSectionPaths("mssql_powerprotect", "inspect_paths", "search_paths")...)
	if len(roots) == 0 {
		roots = []string{
			`C:\Program Files\DPSAPPS`,
			`C:\ProgramData\Dell`,
			`C:\ProgramData\EMC`,
			`C:\Program Files\EMC`,
		}
	}
	var existing []string
	for _, root := range roots {
		root = strings.TrimSpace(os.ExpandEnv(root))
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			existing = append(existing, root)
		}
	}
	return existing
}

func scanCandidates(ctx context.Context, roots []string, maxFileSize int64, maxMatches int) ([]common.Candidate, []string) {
	if len(roots) == 0 {
		return nil, []string{"no common PowerProtect search directory exists on this machine"}
	}
	if maxFileSize <= 0 {
		maxFileSize = 20 * 1024 * 1024
	}
	if maxMatches <= 0 {
		maxMatches = 500
	}

	var candidates []common.Candidate
	var warnings []string
	seen := map[string]struct{}{}
	stop := false

	for _, root := range roots {
		if stop {
			break
		}
		walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if entry.IsDir() {
				return nil
			}
			if _, ok := allowedExtensions[strings.ToLower(filepath.Ext(path))]; !ok {
				return nil
			}
			info, statErr := entry.Info()
			if statErr != nil || info.Size() <= 0 || info.Size() > maxFileSize {
				return nil
			}

			readErr := common.ReadLinesLimited(ctx, path, maxFileSize, func(line string) bool {
				for kind, kindPatterns := range patterns {
					for _, pattern := range kindPatterns {
						matches := pattern.FindStringSubmatch(line)
						if len(matches) < 2 {
							continue
						}
						value := strings.Trim(strings.TrimSpace(matches[1]), `"'`)
						if value == "" || common.LooksSensitive(value) {
							continue
						}
						key := kind + "\x00" + strings.ToLower(value) + "\x00" + path
						if _, exists := seen[key]; exists {
							continue
						}
						seen[key] = struct{}{}
						candidates = append(candidates, common.Candidate{
							Kind:         kind,
							Value:        value,
							Source:       path,
							ModifiedTime: info.ModTime(),
						})
						if len(candidates) >= maxMatches {
							stop = true
							return false
						}
					}
				}
				return true
			})
			if readErr != nil && readErr != context.Canceled && readErr != context.DeadlineExceeded {
				warnings = append(warnings, fmt.Sprintf("unable to scan %s: %v", path, readErr))
			}
			if stop {
				return filepath.SkipAll
			}
			return nil
		})
		if walkErr != nil && walkErr != context.Canceled && walkErr != context.DeadlineExceeded {
			warnings = append(warnings, fmt.Sprintf("unable to walk %s: %v", root, walkErr))
		}
	}

	if stop {
		warnings = append(warnings, fmt.Sprintf("scan stopped after reaching the maximum of %d candidate matches", maxMatches))
	}
	return candidates, warnings
}
