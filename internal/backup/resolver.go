package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
		return "", fmt.Errorf(
			"backup_resolution_failed job=%q error=backup path is empty",
			strings.TrimSpace(job.Name),
		)
	}

	if filePattern == "" {
		return "", fmt.Errorf(
			"backup_resolution_failed job=%q error=backup file pattern is empty",
			strings.TrimSpace(job.Name),
		)
	}

	if err := validateFilePattern(filePattern); err != nil {
		return "", fmt.Errorf(
			"backup_resolution_failed job=%q error=%w",
			strings.TrimSpace(job.Name),
			err,
		)
	}

	absoluteBackupPath, err := filepath.Abs(backupPath)
	if err != nil {
		return "", fmt.Errorf(
			"backup_resolution_failed job=%q path=%q error=resolve absolute backup path: %w",
			strings.TrimSpace(job.Name),
			backupPath,
			err,
		)
	}

	absoluteBackupPath = filepath.Clean(absoluteBackupPath)

	directoryInfo, err := os.Stat(absoluteBackupPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf(
				"backup_resolution_failed job=%q path=%q error=backup path does not exist",
				strings.TrimSpace(job.Name),
				absoluteBackupPath,
			)
		}

		return "", fmt.Errorf(
			"backup_resolution_failed job=%q path=%q error=inspect backup path: %w",
			strings.TrimSpace(job.Name),
			absoluteBackupPath,
			err,
		)
	}

	if !directoryInfo.IsDir() {
		return "", fmt.Errorf(
			"backup_resolution_failed job=%q path=%q error=backup path is not a directory",
			strings.TrimSpace(job.Name),
			absoluteBackupPath,
		)
	}

	entries, err := os.ReadDir(absoluteBackupPath)
	if err != nil {
		return "", fmt.Errorf(
			"backup_resolution_failed job=%q path=%q error=read backup directory: %w",
			strings.TrimSpace(job.Name),
			absoluteBackupPath,
			err,
		)
	}

	var (
		selectedPath    string
		selectedName    string
		selectedInfo    os.FileInfo
		matchedEntries  int
		rejectedEntries int
	)

	for _, entry := range entries {
		entryName := entry.Name()

		matched, matchErr := filepath.Match(filePattern, entryName)
		if matchErr != nil {
			// The pattern was already validated, so reaching this condition
			// indicates an unexpected platform-specific matching failure.
			return "", fmt.Errorf(
				"backup_resolution_failed job=%q pattern=%q file=%q error=match backup file: %w",
				strings.TrimSpace(job.Name),
				filePattern,
				entryName,
				matchErr,
			)
		}

		if !matched {
			continue
		}

		matchedEntries++

		// Do not follow symbolic links. A symlink could point outside the
		// configured backup directory or to a file that changes unexpectedly.
		if entry.Type()&os.ModeSymlink != 0 {
			rejectedEntries++

			r.warnSkippedEntry(
				job,
				absoluteBackupPath,
				entryName,
				"symbolic_link_not_allowed",
				nil,
			)

			continue
		}

		entryInfo, infoErr := entry.Info()
		if infoErr != nil {
			rejectedEntries++

			r.warnSkippedEntry(
				job,
				absoluteBackupPath,
				entryName,
				"file_info_unavailable",
				infoErr,
			)

			continue
		}

		// Reject directories, sockets, devices, named pipes, and any other
		// non-regular filesystem entries.
		if !entryInfo.Mode().IsRegular() {
			rejectedEntries++

			r.warnSkippedEntry(
				job,
				absoluteBackupPath,
				entryName,
				"not_a_regular_file",
				nil,
			)

			continue
		}

		// A zero-byte dump cannot contain a usable database backup and often
		// means the backup operation failed or is not finished.
		if entryInfo.Size() <= 0 {
			rejectedEntries++

			r.warnSkippedEntry(
				job,
				absoluteBackupPath,
				entryName,
				"empty_backup_file",
				nil,
			)

			continue
		}

		candidatePath := filepath.Join(
			absoluteBackupPath,
			entryName,
		)

		if selectedPath == "" ||
			entryInfo.ModTime().After(selectedInfo.ModTime()) ||
			(entryInfo.ModTime().Equal(selectedInfo.ModTime()) &&
				entryName > selectedName) {
			selectedPath = candidatePath
			selectedName = entryName
			selectedInfo = entryInfo
		}
	}

	if selectedPath == "" {
		if matchedEntries == 0 {
			return "", fmt.Errorf(
				"backup_resolution_failed job=%q path=%q pattern=%q error=no matching backup file found",
				strings.TrimSpace(job.Name),
				absoluteBackupPath,
				filePattern,
			)
		}

		return "", fmt.Errorf(
			"backup_resolution_failed job=%q path=%q pattern=%q matched_entries=%d rejected_entries=%d error=no usable backup file found",
			strings.TrimSpace(job.Name),
			absoluteBackupPath,
			filePattern,
			matchedEntries,
			rejectedEntries,
		)
	}

	// Confirm that the selected file still exists and can be opened for
	// reading before returning it to the restore provider.
	selectedFile, err := os.Open(selectedPath)
	if err != nil {
		return "", fmt.Errorf(
			"backup_resolution_failed job=%q selected_backup=%q error=open selected backup: %w",
			strings.TrimSpace(job.Name),
			selectedPath,
			err,
		)
	}

	openedInfo, statErr := selectedFile.Stat()
	closeErr := selectedFile.Close()

	if statErr != nil {
		return "", fmt.Errorf(
			"backup_resolution_failed job=%q selected_backup=%q error=inspect selected backup: %w",
			strings.TrimSpace(job.Name),
			selectedPath,
			statErr,
		)
	}

	if closeErr != nil {
		return "", fmt.Errorf(
			"backup_resolution_failed job=%q selected_backup=%q error=close selected backup after readability check: %w",
			strings.TrimSpace(job.Name),
			selectedPath,
			closeErr,
		)
	}

	if !openedInfo.Mode().IsRegular() {
		return "", fmt.Errorf(
			"backup_resolution_failed job=%q selected_backup=%q error=selected backup is no longer a regular file",
			strings.TrimSpace(job.Name),
			selectedPath,
		)
	}

	if openedInfo.Size() <= 0 {
		return "", fmt.Errorf(
			"backup_resolution_failed job=%q selected_backup=%q error=selected backup is empty",
			strings.TrimSpace(job.Name),
			selectedPath,
		)
	}

	if r.Logger != nil {
		r.Logger.Info(fmt.Sprintf(
			"backup_resolution=success job=%q path=%q pattern=%q selected_backup=%q size_bytes=%d modified_at=%q matched_entries=%d rejected_entries=%d",
			strings.TrimSpace(job.Name),
			absoluteBackupPath,
			filePattern,
			selectedPath,
			openedInfo.Size(),
			openedInfo.ModTime().UTC().Format(time.RFC3339Nano),
			matchedEntries,
			rejectedEntries,
		))
	}

	return selectedPath, nil
}

func validateFilePattern(filePattern string) error {
	if strings.ContainsRune(filePattern, '\x00') {
		return fmt.Errorf(
			"backup file pattern contains a null character",
		)
	}

	if strings.ContainsAny(filePattern, "\r\n") {
		return fmt.Errorf(
			"backup file pattern must be on one line",
		)
	}

	// Backup patterns must match filenames inside the configured directory.
	// They must not contain either Linux or Windows path separators.
	if strings.ContainsAny(filePattern, `/\`) {
		return fmt.Errorf(
			"backup file pattern must be a filename pattern, not a path: %q",
			filePattern,
		)
	}

	if filepath.IsAbs(filePattern) {
		return fmt.Errorf(
			"backup file pattern must not be an absolute path: %q",
			filePattern,
		)
	}

	// Validate the pattern before scanning the directory so malformed
	// character classes are reported even when the directory is empty.
	if _, err := filepath.Match(filePattern, "pattern-validation-probe"); err != nil {
		return fmt.Errorf(
			"invalid backup file pattern %q: %w",
			filePattern,
			err,
		)
	}

	return nil
}

func (r Resolver) warnSkippedEntry(
	job config.JobConfig,
	backupPath string,
	entryName string,
	reason string,
	entryErr error,
) {
	if r.Logger == nil {
		return
	}

	entryPath := filepath.Join(
		backupPath,
		entryName,
	)

	if entryErr != nil {
		r.Logger.Warn(fmt.Sprintf(
			"backup_resolution=entry_skipped job=%q file=%q reason=%s error=%q",
			strings.TrimSpace(job.Name),
			entryPath,
			reason,
			entryErr.Error(),
		))

		return
	}

	r.Logger.Warn(fmt.Sprintf(
		"backup_resolution=entry_skipped job=%q file=%q reason=%s",
		strings.TrimSpace(job.Name),
		entryPath,
		reason,
	))
}
