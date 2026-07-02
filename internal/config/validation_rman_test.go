package config

import (
	"strings"
	"testing"
)

func rmanTestJob() JobConfig {
	enabled := true

	return JobConfig{
		Name:    "sales_rman_restore",
		Enabled: &enabled,
		Type:    TypeOracleRMAN,
		RMAN: RMANConfig{
			Target:      "/",
			CommandFile: "/opt/restore/rman/restore_sales.rman",
			LogFile:     "/opt/restore/logs/sales_rman.log",
			OracleHome:  "/u01/app/oracle/product/19c/dbhome_1",
			OracleSID:   "SALESTST",
		},
	}
}

func rmanTestConfig() Config {
	return Config{
		Tools: ToolsConfig{
			OracleRMAN: OracleRMANToolsConfig{
				RMAN: "rman",
			},
		},
	}
}

func runRMANValidation(t *testing.T, job JobConfig) []string {
	t.Helper()

	var validationErrors []string

	validateOracleRMANJob(
		rmanTestConfig(),
		job,
		strings.TrimSpace(job.Name),
		&validationErrors,
	)

	return validationErrors
}

func assertHasError(
	t *testing.T,
	validationErrors []string,
	substring string,
) {
	t.Helper()

	for _, message := range validationErrors {
		if strings.Contains(message, substring) {
			return
		}
	}

	t.Errorf(
		"expected a validation error containing %q, got %v",
		substring,
		validationErrors,
	)
}

func TestValidateOracleRMANJobAcceptsOSAuth(t *testing.T) {
	if errs := runRMANValidation(t, rmanTestJob()); len(errs) != 0 {
		t.Errorf("expected no validation errors, got %v", errs)
	}
}

func TestValidateOracleRMANJobAcceptsWallet(t *testing.T) {
	job := rmanTestJob()
	job.RMAN.CredentialMethod = "oracle_wallet"
	job.RMAN.Target = "/@SALESTST"
	job.RMAN.Catalog = "/@RCOCAT"

	if errs := runRMANValidation(t, job); len(errs) != 0 {
		t.Errorf("expected no validation errors, got %v", errs)
	}
}

func TestValidateOracleRMANJobRejectsPromptingTarget(t *testing.T) {
	job := rmanTestJob()
	job.RMAN.Target = "sys@ORCL"

	assertHasError(
		t,
		runRMANValidation(t, job),
		"rman.target must be \"/\"",
	)
}

func TestValidateOracleRMANJobRejectsWalletTargetMismatch(t *testing.T) {
	job := rmanTestJob()
	job.RMAN.CredentialMethod = "oracle_wallet"
	job.RMAN.Target = "/"

	assertHasError(
		t,
		runRMANValidation(t, job),
		"rman.target must use the Oracle Wallet form",
	)
}

func TestValidateOracleRMANJobRejectsPromptingCatalog(t *testing.T) {
	job := rmanTestJob()
	job.RMAN.Catalog = "rco@catdb"

	assertHasError(
		t,
		runRMANValidation(t, job),
		"rman.catalog must use the Oracle Wallet form",
	)
}

func TestValidateOracleRMANJobRejectsSharedCommandAndLogPath(t *testing.T) {
	job := rmanTestJob()
	job.RMAN.LogFile = job.RMAN.CommandFile

	assertHasError(
		t,
		runRMANValidation(t, job),
		"must not reference the same path",
	)
}
