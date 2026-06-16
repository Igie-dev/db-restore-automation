package powerprotect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"db-restore-automation/internal/inspect/common"
)

type Inspector struct{}

func (Inspector) Type() string { return "mssql_powerprotect" }

func (Inspector) Inspect(ctx context.Context, request common.Request) common.JobReport {
	job := request.Job
	report := common.JobReport{Name: job.Name, Type: "mssql_powerprotect", Enabled: job.Enabled}

	configuredRestore := request.Config.Tool("mssql_powerprotect", "ddbmsqlrc")
	configuredAdmin := request.Config.Tool("mssql_powerprotect", "msagentadmin")
	common.InspectExecutable(ctx, &report, "ddbmsqlrc", configuredRestore, defaultExecutable("ddbmsqlrc.exe"))
	if configuredAdmin != "" || runtime.GOOS == "windows" {
		common.InspectOptionalExecutable(ctx, &report, "msagentadmin", configuredAdmin, defaultExecutable("msagentadmin.exe"))
	}

	hostname, _ := os.Hostname()
	report.Pass("current hostname", "operating-system hostname", hostname)

	fqdn, instances, candidates, discoveryWarnings := inspectPlatform(ctx, job, request.Options)
	if fqdn == "" {
		report.Warn("current FQDN", "unable to determine a fully qualified domain name", hostname)
	} else {
		report.Pass("current FQDN", "current server identity", fqdn)
	}
	if len(instances) == 0 {
		report.Warn("SQL Server instances", "no SQL Server Database Engine services were discovered", "")
	} else {
		report.Pass("SQL Server instances", "discovered SQL Server services", strings.Join(instances, ", "))
	}
	for _, warning := range discoveryWarnings {
		report.Warn("PowerProtect discovery", warning, "")
	}

	lockboxPath := request.Config.ResolvePath(job.Value(common.ProviderSectionPaths("mssql_powerprotect", "lockbox_path")...))
	if lockboxPath == "" {
		lockboxPath = common.FirstExistingPath(
			`C:\Program Files\DPSAPPS\common\lockbox`,
			`C:\Program Files\EMC\DDBMA\config\lockbox`,
		)
	}
	common.InspectDirectory(&report, "PowerProtect lockbox", lockboxPath, !request.Options.Discover, false)

	credentialMethod := strings.ToLower(job.Value(common.ProviderSectionPaths("mssql_powerprotect", "credential_method")...))
	if credentialMethod == "" {
		credentialMethod = "lockbox"
	}
	if credentialMethod != "lockbox" {
		report.Fail("credential method", "only lockbox is supported for PowerProtect", credentialMethod)
	} else {
		report.Pass("credential method", "PowerProtect lockbox authentication configured", credentialMethod)
	}

	restoreType := strings.ToLower(job.Value(common.ProviderSectionPaths("mssql_powerprotect", "restore_type", "run_type")...))
	if restoreType == "" {
		restoreType = "normal"
	}
	if restoreType != "normal" {
		report.Warn("restore type", "non-default restore type must be verified against the installed Dell agent", restoreType)
	} else {
		report.Pass("restore type", "normal restore type configured", restoreType)
	}

	skipClientResolution := job.BoolValue(false, common.ProviderSectionPaths("mssql_powerprotect", "skip_client_resolution")...)
	report.Info("skip client resolution", "configured PowerProtect client-resolution behavior", fmt.Sprint(skipClientResolution))

	configured := map[string]string{
		"dd_host":     job.Value(common.ProviderSectionPaths("mssql_powerprotect", "dd_host", "device_host")...),
		"dd_user":     job.Value(common.ProviderSectionPaths("mssql_powerprotect", "dd_user", "ddboost_user")...),
		"device_path": job.Value(common.ProviderSectionPaths("mssql_powerprotect", "device_path", "dd_path")...),
		"client":      job.Value(common.ProviderSectionPaths("mssql_powerprotect", "client", "source_client")...),
	}
	for _, key := range []string{"dd_host", "dd_user", "device_path", "client"} {
		if configured[key] == "" {
			if request.Options.Discover {
				report.Info("configured "+key, "not configured in discovery mode", "")
			} else {
				report.Fail("configured "+key, "required value is not configured", "")
			}
		} else {
			report.Pass("configured "+key, "value is configured", configured[key])
		}
	}

	report.Candidates = append(report.Candidates, candidates...)
	for _, key := range []string{"dd_host", "dd_user", "device_path", "client"} {
		values := common.UniqueCandidateValues(candidates, key)
		if len(values) == 0 {
			report.Warn("discovered "+key, "no candidate was found in readable PowerProtect files", "")
			continue
		}
		report.Pass("discovered "+key, fmt.Sprintf("found %d unique candidate(s)", len(values)), strings.Join(values, ", "))
		if configured[key] != "" {
			if common.ContainsEqualFold(values, configured[key]) {
				report.Pass(key+" comparison", "configured value matches a discovered candidate", configured[key])
			} else {
				report.Warn(key+" comparison", "configured value does not match the discovered candidates", configured[key])
			}
		}
	}

	instanceName := job.Value(common.ProviderSectionPaths("mssql_powerprotect", "instance_name", "instance")...)
	if instanceName == "" {
		report.Fail("SQL instance name", "instance_name is not configured", "")
	} else if len(instances) > 0 && !common.ContainsEqualFold(instances, instanceName) && !strings.EqualFold(instanceName, "MSSQLSERVER") {
		report.Warn("SQL instance name", "configured instance was not found among discovered SQL services", instanceName)
	} else {
		report.Pass("SQL instance name", "instance name is configured", instanceName)
	}

	backupSet := job.Value(common.ProviderSectionPaths("mssql_powerprotect", "backup_set")...)
	backupTime := job.Value(common.ProviderSectionPaths("mssql_powerprotect", "backup_time")...)
	if backupSet == "" {
		report.Fail("backup set", "backup_set is not configured", "")
	} else {
		report.Pass("backup set", "backup set is configured", backupSet)
	}
	if backupTime == "" {
		report.Fail("backup time", "backup_time is not configured", "")
	} else {
		report.Pass("backup time", "backup time is configured", backupTime)
	}

	sourceDatabase := job.Value(common.ProviderSectionPaths("mssql_powerprotect", "source_database", "database")...)
	targetDatabase := job.Value(common.ProviderSectionPaths("mssql_powerprotect", "target_database", "target")...)
	if sourceDatabase == "" {
		report.Fail("source database", "source_database is not configured", "")
	} else {
		report.Pass("source database", "source database is configured", sourceDatabase)
	}
	if targetDatabase == "" {
		report.Fail("target database", "target_database is not configured", "")
	} else {
		report.Pass("target database", "target database is configured", targetDatabase)
	}

	relocations := readRelocations(job)
	if len(relocations) == 0 {
		report.Warn("relocation mappings", "no relocation entries were found", "")
	} else {
		seenLogical := map[string]struct{}{}
		seenPhysical := map[string]struct{}{}
		for _, relocation := range relocations {
			logicalKey := strings.ToLower(relocation.LogicalName)
			physicalKey := strings.ToLower(filepath.Clean(relocation.PhysicalPath))
			if relocation.LogicalName == "" || relocation.PhysicalPath == "" {
				report.Fail("relocation mapping", "logical_name and physical_path are required", fmt.Sprintf("%s -> %s", relocation.LogicalName, relocation.PhysicalPath))
				continue
			}
			if _, exists := seenLogical[logicalKey]; exists {
				report.Fail("relocation mapping", "duplicate logical_name", relocation.LogicalName)
			}
			if _, exists := seenPhysical[physicalKey]; exists {
				report.Fail("relocation mapping", "duplicate physical_path", relocation.PhysicalPath)
			}
			seenLogical[logicalKey] = struct{}{}
			seenPhysical[physicalKey] = struct{}{}
			common.InspectDirectory(&report, "relocation directory", filepath.Dir(relocation.PhysicalPath), true, true)
		}
	}

	if request.Options.TestConnection {
		if configured["dd_host"] != "" {
			report.Info("PowerProtect connectivity", "a provider-native catalog command is not run automatically; the Data Domain host value was inspected only", configured["dd_host"])
		} else {
			report.Warn("PowerProtect connectivity", "dd_host is unavailable", "")
		}
	} else {
		report.Info("connection test", "skipped; PowerProtect inspection remains read-only and does not invoke restore", "")
	}

	return report
}

func defaultExecutable(name string) string {
	paths := []string{
		filepath.Join(`C:\Program Files\DPSAPPS\MSAPPAGENT\bin`, name),
		filepath.Join(`C:\Program Files\EMC\DDBMA\bin`, name),
	}
	if found := common.FirstExistingPath(paths...); found != "" {
		return found
	}
	return name
}

type relocation struct {
	LogicalName  string
	PhysicalPath string
}

func readRelocations(job common.Job) []relocation {
	value, ok := common.FirstValue(job.Data, common.ProviderSectionPaths("mssql_powerprotect", "relocate", "relocations")...)
	if !ok {
		return nil
	}
	items, ok := common.AsSlice(value)
	if !ok {
		return nil
	}

	var result []relocation
	for _, item := range items {
		object, ok := common.AsStringMap(item)
		if !ok {
			continue
		}
		result = append(result, relocation{
			LogicalName:  common.FirstString(object, "logical_name", "logical"),
			PhysicalPath: common.FirstString(object, "physical_path", "path", "target_path"),
		})
	}
	return result
}
