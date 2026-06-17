package oraclerman

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"db-restore-automation/internal/inspect/common"
)

const maxRMANCommandFileSize int64 = 4 * 1024 * 1024

var (
	restoreDatabasePattern = regexp.MustCompile(`(?is)\bRESTORE\s+DATABASE\b`)
	recoverDatabasePattern = regexp.MustCompile(`(?is)\bRECOVER\s+DATABASE\b`)
	backupCommandPattern   = regexp.MustCompile(`(?im)^\s*BACKUP\b`)
	deleteCommandPattern   = regexp.MustCompile(`(?im)^\s*DELETE(?:\s+NOPROMPT)?\b`)
	dropDatabasePattern    = regexp.MustCompile(`(?im)^\s*DROP\s+DATABASE\b`)
	hostCommandPattern     = regexp.MustCompile(`(?im)^\s*HOST\b`)
	inlinePasswordPattern  = regexp.MustCompile(`(?i)(\b(?:target|catalog)\s+[^\s/]+/)[^\s@]+(@[^\s;]+)?`)
	genericSecretPattern   = regexp.MustCompile(`(?i)([A-Za-z0-9_.#$-]+/)[^\s@]+(@[A-Za-z0-9_.:/-]+)?`)
)

type Inspector struct{}

type oracleInstanceCandidate struct {
	SID     string
	Home    string
	Source  string
	Running bool
}

func (Inspector) Type() string { return "oracle_rman" }

func (Inspector) Inspect(ctx context.Context, request common.Request) common.JobReport {
	job := request.Job
	report := common.JobReport{
		Name:    job.Name,
		Type:    "oracle_rman",
		Enabled: job.Enabled,
	}

	configuredRMAN := expandAndTrim(request.Config.Tool("oracle_rman", "rman"))
	rmanExecutable := common.InspectExecutable(ctx, &report, "rman", configuredRMAN, "rman", "-version")

	oratabCandidates := discoverOratabCandidates()
	pmonCandidates := discoverPMONCandidates()
	allCandidates := mergeCandidates(pmonCandidates, oratabCandidates)
	appendOracleCandidates(&report, allCandidates)

	configuredSIDRaw := expandAndTrim(job.Value(common.ProviderSectionPaths("oracle_rman", "oracle_sid")...))
	environmentSID := expandAndTrim(os.Getenv("ORACLE_SID"))
	if environmentSID != "" && !isPlaceholder(environmentSID) {
		report.Candidates = append(report.Candidates, common.Candidate{
			Kind:   "oracle_sid",
			Value:  environmentSID,
			Source: "ORACLE_SID environment variable",
		})
	}
	resolvedSID := inspectAndResolveSID(
		&report,
		configuredSIDRaw,
		environmentSID,
		allCandidates,
		request.Options.Discover,
	)

	configuredHomeRaw := expandAndTrim(job.Value(common.ProviderSectionPaths("oracle_rman", "oracle_home")...))
	environmentHome := expandAndTrim(os.Getenv("ORACLE_HOME"))
	if environmentHome != "" && !isPlaceholder(environmentHome) {
		report.Candidates = append(report.Candidates, common.Candidate{
			Kind:   "oracle_home",
			Value:  environmentHome,
			Source: "ORACLE_HOME environment variable",
		})
	}
	derivedHome := deriveOracleHomeFromRMAN(rmanExecutable)
	if derivedHome != "" {
		report.Candidates = append(report.Candidates, common.Candidate{
			Kind:   "oracle_home",
			Value:  derivedHome,
			Source: "resolved RMAN executable",
		})
	}
	report.SortCandidates()
	resolvedHome := inspectAndResolveOracleHome(
		&report,
		configuredHomeRaw,
		environmentHome,
		derivedHome,
		resolvedSID,
		oratabCandidates,
		request.Options.Discover,
	)

	inspectOracleCandidateComparison(&report, resolvedSID, resolvedHome, allCandidates)

	tnsAdmin := resolveTNSAdmin(job, resolvedHome)
	inspectTNSAdmin(&report, tnsAdmin)

	credentialMethod := strings.ToLower(expandAndTrim(job.Value(
		common.ProviderSectionPaths("oracle_rman", "credential_method")...,
	)))
	if credentialMethod == "" {
		credentialMethod = "os_auth"
	}

	switch credentialMethod {
	case "os_auth":
		report.Pass("credential method", "operating-system authentication configured", credentialMethod)
		inspectOSAuthentication(&report)

	case "oracle_wallet":
		report.Pass("credential method", "Oracle Wallet authentication configured", credentialMethod)
		walletPath := request.Config.ResolvePath(job.Value(
			common.ProviderSectionPaths("oracle_rman", "wallet_path", "wallet_directory")...,
		))
		if walletPath == "" {
			report.Fail("Oracle Wallet", "wallet path is required for oracle_wallet authentication", "")
		} else {
			common.InspectDirectory(&report, "Oracle Wallet", walletPath, !request.Options.Discover, false)
		}

	default:
		report.Fail("credential method", "supported values are os_auth and oracle_wallet", credentialMethod)
	}

	target := expandAndTrim(job.Value(common.ProviderSectionPaths("oracle_rman", "target")...))
	targetValid := inspectRMANConnectionValue(
		&report,
		"RMAN target",
		target,
		request.Options.Discover,
		credentialMethod == "os_auth",
	)

	catalog := expandAndTrim(job.Value(common.ProviderSectionPaths("oracle_rman", "catalog")...))
	catalogValid := inspectRMANCatalog(&report, catalog)

	commandFile := request.Config.ResolvePath(job.Value(
		common.ProviderSectionPaths("oracle_rman", "command_file", "cmdfile")...,
	))
	common.InspectFile(&report, "RMAN command file", commandFile, !request.Options.Discover)
	if commandFile != "" {
		inspectRMANCommandFile(&report, commandFile)
	}

	logFile := request.Config.ResolvePath(job.Value(
		common.ProviderSectionPaths("oracle_rman", "log_file", "log")...,
	))
	if logFile == "" {
		report.Warn("RMAN log file", "log file is not configured", "")
	} else {
		common.InspectDirectory(&report, "RMAN log directory", filepath.Dir(logFile), true, true)
		report.Info("RMAN log file", "configured output file", logFile)
	}

	workingDirectory := request.Config.ResolvePath(job.Value(
		common.ProviderSectionPaths("oracle_rman", "working_directory", "work_dir")...,
	))
	if workingDirectory == "" && commandFile != "" {
		workingDirectory = filepath.Dir(commandFile)
	}
	if workingDirectory == "" {
		report.Warn("RMAN working directory", "working directory could not be resolved", "")
	} else {
		common.InspectDirectory(
			&report,
			"RMAN working directory",
			workingDirectory,
			!request.Options.Discover,
			false,
		)
	}

	restoreScope := strings.ToLower(expandAndTrim(job.Value(
		common.ProviderSectionPaths("oracle_rman", "restore_scope")...,
	)))
	if restoreScope == "" {
		restoreScope = "full_database"
	}
	if restoreScope != "full_database" {
		report.Fail("restore scope", "only full_database is currently supported", restoreScope)
	} else {
		report.Pass("restore scope", "supported restore scope", restoreScope)
	}

	if resolvedSID != "" && resolvedHome != "" {
		report.Info(
			"suggested RMAN environment",
			"review these values before updating YAML",
			fmt.Sprintf("oracle_home: %q; oracle_sid: %q", resolvedHome, resolvedSID),
		)
	}

	if request.Options.TestConnection {
		inspectRMANConnection(
			ctx,
			&report,
			rmanExecutable,
			target,
			catalog,
			resolvedHome,
			resolvedSID,
			tnsAdmin,
			workingDirectory,
			targetValid,
			catalogValid,
			request.Options.Discover,
		)
	} else {
		report.Info(
			"connection test",
			"skipped; use --test-connection to run a read-only RMAN SHOW ALL probe",
			"",
		)
	}

	return report
}

func inspectAndResolveSID(
	report *common.JobReport,
	configured string,
	environment string,
	candidates []oracleInstanceCandidate,
	discoverMode bool,
) string {
	if configured != "" {
		if isPlaceholder(configured) {
			report.Fail("configured ORACLE_SID", "configured value appears to be a placeholder", configured)
		} else {
			report.Pass("configured ORACLE_SID", "Oracle SID is configured", configured)
			return configured
		}
	}

	if environment != "" && !isPlaceholder(environment) {
		report.Warn("ORACLE_SID", "using ORACLE_SID from the process environment because YAML is not usable", environment)
		return environment
	}

	databaseCandidates := uniqueDatabaseSIDs(candidates)

	switch len(databaseCandidates) {
	case 0:
		if discoverMode {
			report.Info("ORACLE_SID", "no database SID was discovered", "")
		} else {
			report.Fail("ORACLE_SID", "no valid YAML, environment, PMON, or oratab SID was found", "")
		}
		return ""

	case 1:
		report.Warn("ORACLE_SID", "not explicitly configured; using the only discovered database SID candidate", databaseCandidates[0])
		return databaseCandidates[0]

	default:
		report.Warn(
			"ORACLE_SID",
			"multiple database SID candidates were discovered; configure rman.oracle_sid explicitly",
			strings.Join(databaseCandidates, ", "),
		)
		return ""
	}
}

func inspectAndResolveOracleHome(
	report *common.JobReport,
	configured string,
	environment string,
	derivedFromRMAN string,
	resolvedSID string,
	oratabCandidates []oracleInstanceCandidate,
	discoverMode bool,
) string {
	if configured != "" {
		if isPlaceholder(configured) {
			report.Fail("configured ORACLE_HOME", "configured value appears to be a placeholder", configured)
		} else {
			common.InspectDirectory(report, "ORACLE_HOME", configured, !discoverMode, false)
			return configured
		}
	}

	if environment != "" && !isPlaceholder(environment) {
		report.Warn("ORACLE_HOME", "using ORACLE_HOME from the process environment because YAML is not usable", environment)
		common.InspectDirectory(report, "ORACLE_HOME", environment, !discoverMode, false)
		return environment
	}

	if derivedFromRMAN != "" {
		report.Warn("ORACLE_HOME", "derived from the resolved RMAN executable", derivedFromRMAN)
		common.InspectDirectory(report, "ORACLE_HOME", derivedFromRMAN, !discoverMode, false)
		return derivedFromRMAN
	}

	if resolvedSID != "" {
		for _, candidate := range oratabCandidates {
			if strings.EqualFold(candidate.SID, resolvedSID) && candidate.Home != "" {
				report.Warn("ORACLE_HOME", "resolved from oratab using the selected Oracle SID", candidate.Home)
				common.InspectDirectory(report, "ORACLE_HOME", candidate.Home, !discoverMode, false)
				return candidate.Home
			}
		}
	}

	uniqueHomes := uniqueOracleHomes(oratabCandidates)
	if len(uniqueHomes) == 1 {
		report.Warn("ORACLE_HOME", "not explicitly configured; using the only home discovered from oratab", uniqueHomes[0])
		common.InspectDirectory(report, "ORACLE_HOME", uniqueHomes[0], !discoverMode, false)
		return uniqueHomes[0]
	}

	if len(uniqueHomes) > 1 {
		report.Warn("ORACLE_HOME", "multiple Oracle homes were discovered; configure rman.oracle_home explicitly", strings.Join(uniqueHomes, ", "))
	} else if discoverMode {
		report.Info("ORACLE_HOME", "no Oracle home was discovered", "")
	} else {
		report.Fail("ORACLE_HOME", "no valid YAML, environment, RMAN-derived, or oratab Oracle home was found", "")
	}

	return ""
}

func inspectOracleCandidateComparison(
	report *common.JobReport,
	resolvedSID string,
	resolvedHome string,
	candidates []oracleInstanceCandidate,
) {
	if resolvedSID == "" {
		return
	}

	matched := false
	runningSIDs := make([]string, 0)
	discoveredSIDs := make([]string, 0)

	for _, candidate := range candidates {
		if !isInfrastructureSID(candidate.SID) {
			discoveredSIDs = append(discoveredSIDs, candidate.SID)
		}
		if candidate.Running && !isInfrastructureSID(candidate.SID) {
			runningSIDs = append(runningSIDs, candidate.SID)
		}

		if !strings.EqualFold(candidate.SID, resolvedSID) {
			continue
		}

		matched = true
		if candidate.Running {
			report.Pass("Oracle SID comparison", "configured or selected SID matches a running PMON instance", resolvedSID)
		} else {
			report.Pass("Oracle SID comparison", "configured or selected SID matches an oratab entry", resolvedSID)
		}

		if resolvedHome != "" && candidate.Home != "" {
			if samePath(resolvedHome, candidate.Home) {
				report.Pass("Oracle home comparison", "selected Oracle home matches the oratab entry for the SID", resolvedHome)
			} else {
				report.Warn(
					"Oracle home comparison",
					"selected Oracle home differs from the oratab entry for the SID",
					fmt.Sprintf("selected=%s; oratab=%s", resolvedHome, candidate.Home),
				)
			}
		}
	}

	if !matched {
		runningSIDs = uniqueStringsFold(runningSIDs)
		discoveredSIDs = uniqueStringsFold(discoveredSIDs)

		if len(runningSIDs) > 0 {
			report.Warn(
				"Oracle SID comparison",
				"selected SID does not match any running database PMON instance",
				"selected="+resolvedSID+"; running="+strings.Join(runningSIDs, ", "),
			)
		} else if len(discoveredSIDs) > 0 {
			report.Warn(
				"Oracle SID comparison",
				"selected SID does not match any SID discovered from the local Oracle configuration",
				"selected="+resolvedSID+"; discovered="+strings.Join(discoveredSIDs, ", "),
			)
		}
	}
}

func resolveTNSAdmin(job common.Job, oracleHome string) string {
	tnsAdmin := firstNonEmpty(
		job.Value(common.ProviderSectionPaths("oracle_rman", "tns_admin")...),
		os.Getenv("TNS_ADMIN"),
	)
	if tnsAdmin == "" && oracleHome != "" {
		tnsAdmin = filepath.Join(oracleHome, "network", "admin")
	}
	return tnsAdmin
}

func inspectTNSAdmin(report *common.JobReport, tnsAdmin string) {
	if tnsAdmin == "" {
		report.Info("TNS_ADMIN", "not configured; local target '/' does not normally require Oracle Net naming", "")
		return
	}

	common.InspectDirectory(report, "TNS_ADMIN", tnsAdmin, false, false)

	for _, fileName := range []string{"tnsnames.ora", "sqlnet.ora"} {
		path := filepath.Join(tnsAdmin, fileName)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			report.Pass(fileName, "Oracle Net configuration file found", path)
		} else {
			report.Info(fileName, "Oracle Net configuration file was not found", path)
		}
	}
}

func inspectOSAuthentication(report *common.JobReport) {
	currentUser, err := user.Current()
	if err != nil {
		report.Warn("OS authentication user", "current operating-system user could not be determined", err.Error())
		return
	}

	report.Info("OS authentication user", "current operating-system account", currentUser.Username)

	groupIDs, err := currentUser.GroupIds()
	if err != nil {
		report.Warn("Oracle administrative group", "current user groups could not be read", err.Error())
		return
	}

	groupNames := make([]string, 0, len(groupIDs))
	recognized := make([]string, 0)

	for _, groupID := range groupIDs {
		group, lookupErr := user.LookupGroupId(groupID)
		if lookupErr != nil {
			continue
		}

		groupNames = append(groupNames, group.Name)
		if isRecognizedOracleAdminGroup(group.Name) {
			recognized = append(recognized, group.Name)
		}
	}

	groupNames = uniqueStringsFold(groupNames)
	recognized = uniqueStringsFold(recognized)

	if len(recognized) > 0 {
		report.Pass("Oracle administrative group", "current account belongs to a commonly recognized Oracle administrative group", strings.Join(recognized, ", "))
		return
	}

	detail := ""
	if len(groupNames) > 0 {
		detail = strings.Join(groupNames, ", ")
	}
	report.Warn(
		"Oracle administrative group",
		"current account is not in a commonly recognized Oracle administrative group; confirm OS authentication manually",
		detail,
	)
}

func inspectRMANConnectionValue(
	report *common.JobReport,
	checkName string,
	value string,
	discoverMode bool,
	osAuth bool,
) bool {
	if value == "" {
		if discoverMode {
			report.Info(checkName, "not configured in discovery mode", "")
		} else {
			report.Fail(checkName, "target is not configured", "")
		}
		return false
	}

	if isPlaceholder(value) {
		report.Fail(checkName, "configured value appears to be a placeholder", "")
		return false
	}

	if hasInlineCredentials(value) {
		report.Fail(checkName, "inline credentials are not allowed; use OS authentication or Oracle Wallet", "<redacted>")
		return false
	}

	if value == "/" {
		if osAuth {
			report.Pass(checkName, "local operating-system authentication target configured", value)
		} else {
			report.Warn(checkName, "target '/' is normally used with os_auth", value)
		}
		return true
	}

	report.Pass(checkName, "target is configured without an inline password", value)
	return true
}

func inspectRMANCatalog(report *common.JobReport, catalog string) bool {
	if catalog == "" {
		report.Info("RMAN catalog", "no recovery catalog configured; target control-file repository will be used", "")
		return true
	}

	if isPlaceholder(catalog) {
		report.Fail("RMAN catalog", "configured value appears to be a placeholder", "")
		return false
	}

	if hasInlineCredentials(catalog) {
		report.Fail("RMAN catalog", "inline credentials are not allowed; use an Oracle Wallet or external credential store", "<redacted>")
		return false
	}

	report.Pass("RMAN catalog", "recovery catalog is configured without an inline password", catalog)
	return true
}

func inspectRMANCommandFile(report *common.JobReport, commandFile string) {
	info, err := os.Stat(commandFile)
	if err != nil || info.IsDir() {
		return
	}

	if info.Size() > maxRMANCommandFileSize {
		report.Warn(
			"RMAN command-file inspection",
			"command file is larger than the safe static-inspection limit",
			fmt.Sprintf("path=%s; size=%d; limit=%d", commandFile, info.Size(), maxRMANCommandFileSize),
		)
		return
	}

	content, err := os.ReadFile(commandFile)
	if err != nil {
		report.Fail("RMAN command-file inspection", "command file could not be read", err.Error())
		return
	}

	text := stripRMANComments(string(content))
	upper := strings.ToUpper(text)

	if containsAnyPlaceholder(upper) {
		report.Fail("RMAN command-file placeholders", "placeholder content remains in the RMAN command file", commandFile)
	} else {
		report.Pass("RMAN command-file placeholders", "no known placeholder tokens were detected", commandFile)
	}

	if restoreDatabasePattern.MatchString(text) {
		report.Pass("RMAN restore command", "RESTORE DATABASE was found", commandFile)
	} else {
		report.Warn("RMAN restore command", "RESTORE DATABASE was not found; confirm the DBA-approved script implements the intended full restore", commandFile)
	}

	if recoverDatabasePattern.MatchString(text) {
		report.Pass("RMAN recover command", "RECOVER DATABASE was found", commandFile)
	} else {
		report.Warn("RMAN recover command", "RECOVER DATABASE was not found; confirm recovery is handled by the approved restore procedure", commandFile)
	}

	if backupCommandPattern.MatchString(text) {
		report.Fail("restore-only policy", "BACKUP command detected in the RMAN command file", commandFile)
	}
	if deleteCommandPattern.MatchString(text) {
		report.Fail("restore-only policy", "DELETE command detected in the RMAN command file", commandFile)
	}
	if dropDatabasePattern.MatchString(text) {
		report.Fail("restore-only policy", "DROP DATABASE command detected in the RMAN command file", commandFile)
	}
	if hostCommandPattern.MatchString(text) {
		report.Warn("RMAN HOST command", "HOST command detected; review the script because it can execute operating-system commands", commandFile)
	}
	if inlinePasswordPattern.MatchString(text) {
		report.Fail("RMAN command-file credentials", "possible inline credential detected; move credentials to OS authentication or Oracle Wallet", commandFile)
	}
}

func inspectRMANConnection(
	ctx context.Context,
	report *common.JobReport,
	rmanExecutable string,
	target string,
	catalog string,
	oracleHome string,
	oracleSID string,
	tnsAdmin string,
	workingDirectory string,
	targetValid bool,
	catalogValid bool,
	discoverMode bool,
) {
	if discoverMode {
		report.Warn("RMAN connection test", "not executed in --discover mode because no complete configured job is available", "")
		return
	}
	if rmanExecutable == "" {
		report.Fail("RMAN connection test", "RMAN executable could not be resolved", "")
		return
	}
	if !targetValid || !catalogValid {
		report.Fail("RMAN connection test", "not executed because target or catalog validation failed", "")
		return
	}
	if target == "/" && oracleSID == "" {
		report.Fail("RMAN connection test", "ORACLE_SID is required for local target '/' authentication", "")
		return
	}

	arguments := []string{"target", target}
	if catalog != "" {
		arguments = append(arguments, "catalog", catalog)
	}

	command := exec.CommandContext(ctx, rmanExecutable, arguments...)
	command.Stdin = strings.NewReader("SHOW ALL;\nEXIT;\n")
	command.Env = oracleEnvironment(oracleHome, oracleSID, tnsAdmin)
	if workingDirectory != "" {
		if info, err := os.Stat(workingDirectory); err == nil && info.IsDir() {
			command.Dir = workingDirectory
		}
	}

	var stdout limitedBuffer
	var stderr limitedBuffer
	stdout.limit = 1024 * 1024
	stderr.limit = 1024 * 1024
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()
	combined := strings.TrimSpace(stdout.String() + "\n" + stderr.String())
	safeDetail := summarizeRMANOutput(combined)

	if err != nil {
		if ctx.Err() != nil {
			report.Fail("RMAN connection test", "read-only RMAN SHOW ALL probe was cancelled or timed out", ctx.Err().Error())
			return
		}

		report.Fail("RMAN connection test", "read-only RMAN SHOW ALL probe failed", safeDetail)
		return
	}

	upper := strings.ToUpper(combined)
	if strings.Contains(upper, "RMAN-") || strings.Contains(upper, "ORA-") {
		report.Warn("RMAN connection test", "RMAN exited but reported Oracle or RMAN messages that require review", safeDetail)
		return
	}

	if strings.Contains(upper, "CONNECTED TO TARGET DATABASE") {
		report.Pass("RMAN target connection", "connected to the target database", oracleSID)
	} else {
		report.Warn("RMAN target connection", "RMAN completed but the standard target connection confirmation was not detected", safeDetail)
	}

	if strings.Contains(upper, "CONFIGURE") || strings.Contains(upper, "RETENTION POLICY") {
		report.Pass("RMAN SHOW ALL", "read-only RMAN configuration probe completed successfully", safeDetail)
	} else {
		report.Warn("RMAN SHOW ALL", "RMAN completed but SHOW ALL output was not recognized", safeDetail)
	}
}

func discoverOratabCandidates() []oracleInstanceCandidate {
	if runtime.GOOS == "windows" {
		return nil
	}

	paths := []string{"/etc/oratab", "/var/opt/oracle/oratab"}
	candidates := make([]oracleInstanceCandidate, 0)

	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			parts := strings.Split(line, ":")
			if len(parts) < 2 {
				continue
			}

			sid := strings.TrimSpace(parts[0])
			home := expandAndTrim(parts[1])
			if sid == "" || sid == "*" || home == "" || isPlaceholder(sid) || isPlaceholder(home) {
				continue
			}

			candidates = append(candidates, oracleInstanceCandidate{
				SID:    sid,
				Home:   home,
				Source: path,
			})
		}

		_ = file.Close()
	}

	return deduplicateCandidates(candidates)
}

func discoverPMONCandidates() []oracleInstanceCandidate {
	if runtime.GOOS != "linux" {
		return nil
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	candidates := make([]oracleInstanceCandidate, 0)
	const prefix = "ora_pmon_"

	for _, entry := range entries {
		if !entry.IsDir() || !isDigits(entry.Name()) {
			continue
		}

		content, readErr := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if readErr != nil {
			continue
		}

		processName := strings.TrimSpace(string(content))
		if !strings.HasPrefix(strings.ToLower(processName), prefix) {
			continue
		}

		sid := strings.TrimSpace(processName[len(prefix):])
		if sid == "" {
			continue
		}

		candidates = append(candidates, oracleInstanceCandidate{
			SID:     sid,
			Source:  "running process " + processName,
			Running: true,
		})
	}

	return deduplicateCandidates(candidates)
}

func appendOracleCandidates(report *common.JobReport, candidates []oracleInstanceCandidate) {
	seen := make(map[string]struct{})

	for _, candidate := range candidates {
		if candidate.SID != "" {
			key := "oracle_sid|" + strings.ToLower(candidate.SID) + "|" + strings.ToLower(candidate.Source)
			if _, exists := seen[key]; !exists {
				report.Candidates = append(report.Candidates, common.Candidate{
					Kind:   "oracle_sid",
					Value:  candidate.SID,
					Source: candidate.Source,
				})
				seen[key] = struct{}{}
			}
		}

		if candidate.Home != "" {
			key := "oracle_home|" + strings.ToLower(candidate.Home) + "|" + strings.ToLower(candidate.Source)
			if _, exists := seen[key]; !exists {
				report.Candidates = append(report.Candidates, common.Candidate{
					Kind:   "oracle_home",
					Value:  candidate.Home,
					Source: candidate.Source,
				})
				seen[key] = struct{}{}
			}
		}
	}

	report.SortCandidates()
}

func mergeCandidates(groups ...[]oracleInstanceCandidate) []oracleInstanceCandidate {
	merged := make([]oracleInstanceCandidate, 0)
	for _, group := range groups {
		merged = append(merged, group...)
	}
	return deduplicateCandidates(merged)
}

func deduplicateCandidates(candidates []oracleInstanceCandidate) []oracleInstanceCandidate {
	seen := make(map[string]struct{})
	result := make([]oracleInstanceCandidate, 0, len(candidates))

	for _, candidate := range candidates {
		key := strings.ToLower(candidate.SID) + "|" + strings.ToLower(candidate.Home) + "|" + strings.ToLower(candidate.Source)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, candidate)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Running != result[j].Running {
			return result[i].Running
		}
		if !strings.EqualFold(result[i].SID, result[j].SID) {
			return strings.ToLower(result[i].SID) < strings.ToLower(result[j].SID)
		}
		return strings.ToLower(result[i].Source) < strings.ToLower(result[j].Source)
	})

	return result
}

func uniqueDatabaseSIDs(candidates []oracleInstanceCandidate) []string {
	values := make([]string, 0)
	for _, candidate := range candidates {
		if candidate.SID == "" || isInfrastructureSID(candidate.SID) {
			continue
		}
		values = append(values, candidate.SID)
	}
	return uniqueStringsFold(values)
}

func uniqueOracleHomes(candidates []oracleInstanceCandidate) []string {
	values := make([]string, 0)
	for _, candidate := range candidates {
		if candidate.Home != "" {
			values = append(values, candidate.Home)
		}
	}
	return uniqueStringsFold(values)
}

func uniqueStringsFold(values []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i]) < strings.ToLower(result[j])
	})
	return result
}

func deriveOracleHomeFromRMAN(rmanExecutable string) string {
	if rmanExecutable == "" {
		return ""
	}

	cleaned := filepath.Clean(rmanExecutable)
	base := strings.ToLower(filepath.Base(cleaned))
	if base != "rman" && base != "rman.exe" {
		return ""
	}

	binDirectory := filepath.Dir(cleaned)
	if !strings.EqualFold(filepath.Base(binDirectory), "bin") {
		return ""
	}

	return filepath.Dir(binDirectory)
}

func oracleEnvironment(oracleHome string, oracleSID string, tnsAdmin string) []string {
	environment := append([]string(nil), os.Environ()...)
	if oracleHome != "" {
		environment = setEnvironmentValue(environment, "ORACLE_HOME", oracleHome)
		currentPath := environmentValue(environment, "PATH")
		binPath := filepath.Join(oracleHome, "bin")
		if currentPath == "" {
			currentPath = binPath
		} else {
			currentPath = binPath + string(os.PathListSeparator) + currentPath
		}
		environment = setEnvironmentValue(environment, "PATH", currentPath)
	}
	if oracleSID != "" {
		environment = setEnvironmentValue(environment, "ORACLE_SID", oracleSID)
	}
	if tnsAdmin != "" {
		environment = setEnvironmentValue(environment, "TNS_ADMIN", tnsAdmin)
	}
	return environment
}

func setEnvironmentValue(environment []string, key string, value string) []string {
	prefix := strings.ToUpper(key) + "="
	result := make([]string, 0, len(environment)+1)
	replaced := false

	for _, item := range environment {
		if strings.HasPrefix(strings.ToUpper(item), prefix) {
			if !replaced {
				result = append(result, key+"="+value)
				replaced = true
			}
			continue
		}
		result = append(result, item)
	}

	if !replaced {
		result = append(result, key+"="+value)
	}
	return result
}

func environmentValue(environment []string, key string) string {
	prefix := strings.ToUpper(key) + "="
	for _, item := range environment {
		if strings.HasPrefix(strings.ToUpper(item), prefix) {
			return item[len(prefix):]
		}
	}
	return ""
}

func stripRMANComments(content string) string {
	lines := strings.Split(content, "\n")
	cleaned := make([]string, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "--") {
			continue
		}

		if index := strings.Index(line, " #"); index >= 0 {
			line = line[:index]
		}
		if index := strings.Index(line, " --"); index >= 0 {
			line = line[:index]
		}
		cleaned = append(cleaned, line)
	}

	return strings.Join(cleaned, "\n")
}

func containsAnyPlaceholder(value string) bool {
	placeholders := []string{
		"REPLACE_WITH_",
		"REPLACE-ME",
		"REPLACE_ME",
		"CHANGEME",
		"CHANGE_ME",
		"<ORACLE_SID>",
		"<ORACLE_HOME>",
		"<DBID>",
		"<BACKUP_PATH>",
		"YOUR_ORACLE_SID",
		"YOUR_ORACLE_HOME",
		"TODO",
	}

	upper := strings.ToUpper(value)
	for _, placeholder := range placeholders {
		if strings.Contains(upper, placeholder) {
			return true
		}
	}
	return false
}

func isPlaceholder(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	return containsAnyPlaceholder(trimmed)
}

func hasInlineCredentials(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "/" {
		return false
	}

	slash := strings.Index(trimmed, "/")
	if slash < 0 {
		return false
	}

	// Wallet syntax such as "user/@service" has an empty password segment.
	remaining := trimmed[slash+1:]
	return remaining != "" && !strings.HasPrefix(remaining, "@")
}

func isInfrastructureSID(sid string) bool {
	upper := strings.ToUpper(strings.TrimSpace(sid))
	return strings.HasPrefix(upper, "+ASM") ||
		strings.HasPrefix(upper, "+APX") ||
		upper == "-MGMTDB" ||
		strings.Contains(upper, "MGMTDB")
}

func isRecognizedOracleAdminGroup(groupName string) bool {
	name := strings.ToLower(strings.TrimSpace(groupName))
	return name == "dba" ||
		name == "oper" ||
		name == "backupdba" ||
		name == "asmdba" ||
		name == "asmadmin" ||
		strings.HasSuffix(name, "dba")
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func samePath(left string, right string) bool {
	left = filepath.Clean(expandAndTrim(left))
	right = filepath.Clean(expandAndTrim(right))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func expandAndTrim(value string) string {
	return strings.TrimSpace(os.ExpandEnv(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if expanded := expandAndTrim(value); expanded != "" {
			return expanded
		}
	}
	return ""
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	if buffer.limit <= 0 || buffer.Buffer.Len() >= buffer.limit {
		return originalLength, nil
	}

	remaining := buffer.limit - buffer.Buffer.Len()
	if len(data) > remaining {
		data = data[:remaining]
	}
	_, _ = buffer.Buffer.Write(data)
	return originalLength, nil
}

func summarizeRMANOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return "no RMAN output was captured"
	}

	output = inlinePasswordPattern.ReplaceAllString(output, `${1}<redacted>${2}`)
	output = genericSecretPattern.ReplaceAllString(output, `${1}<redacted>${2}`)

	lines := strings.Split(output, "\n")
	selected := make([]string, 0, 20)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		upper := strings.ToUpper(line)
		if strings.Contains(upper, "CONNECTED TO TARGET DATABASE") ||
			strings.Contains(upper, "RETENTION POLICY") ||
			strings.HasPrefix(upper, "CONFIGURE ") ||
			strings.Contains(upper, "RMAN-") ||
			strings.Contains(upper, "ORA-") ||
			strings.Contains(upper, "LRM-") {
			selected = append(selected, line)
		}
		if len(selected) >= 20 {
			break
		}
	}

	if len(selected) == 0 {
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			selected = append(selected, line)
			if len(selected) >= 10 {
				break
			}
		}
	}

	result := strings.Join(selected, " | ")
	if len(result) > 3000 {
		result = result[:3000] + "..."
	}
	return result
}
