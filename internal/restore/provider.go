package restore

import (
	"context"
	"fmt"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
	"db-restore-automation/internal/shell"
)

type Options struct {
	DryRun     bool
	BackupFile string
}

type Provider interface {
	Restore(ctx context.Context, cfg config.Config, job config.JobConfig, opts Options) error
}

func ProviderFor(jobType string, logger *logging.Logger) (Provider, error) {
	runner := shell.Runner{Logger: logger}
	switch jobType {
	case config.TypePostgres:
		return PostgresProvider{Logger: logger, Runner: runner}, nil
	case config.TypeMySQL:
		return MySQLProvider{Logger: logger, Runner: runner}, nil
	case config.TypeOracle:
		return OracleDataPumpProvider{Logger: logger, Runner: runner}, nil
	case config.TypeOracleRMAN:
		return OracleRmanProvider{Logger: logger, Runner: runner}, nil
	case config.TypeMSSQLPowerProtect:
		return MssqlPowerProtectProvider{Logger: logger, Runner: runner}, nil
	default:
		return nil, fmt.Errorf("unsupported database type: %s", jobType)
	}
}

func ProviderLog(job config.JobConfig) string {
	if job.TypeName() == config.TypeOracleRMAN {
		return job.RMAN.LogFile
	}
	return ""
}
