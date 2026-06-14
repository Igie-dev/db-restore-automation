package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
)

type Resolver struct {
	Logger *logging.Logger
}

func (r Resolver) Latest(job config.JobConfig) (string, error) {
	if !job.UsesBackupFile() {
		return "", nil
	}
	backupPath := strings.TrimSpace(job.BackupPath)
	filePattern := strings.TrimSpace(job.FilePattern)
	if backupPath == "" {
		return "", fmt.Errorf("backup path is empty")
	}
	if filePattern == "" {
		return "", fmt.Errorf("backup file pattern is empty")
	}
	if strings.ContainsAny(filePattern, `/\`) || strings.Contains(filePattern, "..") {
		return "", fmt.Errorf("backup file pattern must be a simple filename pattern, not a path: %s", filePattern)
	}
	info, err := os.Stat(backupPath)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("backup path does not exist or is not a directory: %s", backupPath)
	}

	entries, err := os.ReadDir(backupPath)
	if err != nil {
		return "", fmt.Errorf("search backup files: %w", err)
	}

	var selected string
	var selectedMod int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matched, err := filepath.Match(filePattern, entry.Name())
		if err != nil {
			return "", fmt.Errorf("invalid file pattern %q: %w", filePattern, err)
		}
		if !matched {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		mod := info.ModTime().UnixNano()
		if selected == "" || mod > selectedMod {
			selected = filepath.Join(backupPath, entry.Name())
			selectedMod = mod
		}
	}
	if selected == "" {
		return "", fmt.Errorf("no backup file found: path=%s pattern=%s", backupPath, filePattern)
	}
	if r.Logger != nil {
		r.Logger.Info(fmt.Sprintf("backup_resolution=success path=%s pattern=%s selected_backup=%s", backupPath, filePattern, selected))
	}
	return selected, nil
}
