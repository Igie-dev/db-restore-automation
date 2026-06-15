package restore

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
	"db-restore-automation/internal/shell"
)

type Options struct {
	DryRun     bool
	BackupFile string
}

type Provider interface {
	Restore(
		ctx context.Context,
		cfg config.Config,
		job config.JobConfig,
		opts Options,
	) error
}

func ProviderFor(
	jobType string,
	logger *logging.Logger,
) (Provider, error) {
	normalizedJobType := strings.ToLower(
		strings.TrimSpace(jobType),
	)

	if normalizedJobType == "" {
		return nil, fmt.Errorf(
			"restore provider type is required",
		)
	}

	if strings.ContainsRune(normalizedJobType, '\x00') ||
		strings.ContainsAny(normalizedJobType, "\r\n") {
		return nil, fmt.Errorf(
			"restore provider type must be a single-line value without null characters",
		)
	}

	runner := shell.Runner{
		Logger: logger,
	}

	switch normalizedJobType {
	case config.TypePostgres:
		return PostgresProvider{
			Logger: logger,
			Runner: runner,
		}, nil

	case config.TypeMySQL:
		return MySQLProvider{
			Logger: logger,
			Runner: runner,
		}, nil

	case config.TypeOracle:
		return OracleDataPumpProvider{
			Logger: logger,
			Runner: runner,
		}, nil

	case config.TypeOracleRMAN:
		return OracleRmanProvider{
			Logger: logger,
			Runner: runner,
		}, nil

	case config.TypeMSSQLPowerProtect:
		return MssqlPowerProtectProvider{
			Logger: logger,
			Runner: runner,
		}, nil

	default:
		return nil, fmt.Errorf(
			"unsupported database type %q; supported types are: %s",
			normalizedJobType,
			strings.Join(
				[]string{
					config.TypePostgres,
					config.TypeMySQL,
					config.TypeOracle,
					config.TypeOracleRMAN,
					config.TypeMSSQLPowerProtect,
				},
				", ",
			),
		)
	}
}

func ProviderLog(job config.JobConfig) string {
	switch job.TypeName() {
	case config.TypeOracleRMAN:
		logFile := strings.TrimSpace(job.RMAN.LogFile)
		if logFile == "" {
			return ""
		}

		if strings.ContainsRune(logFile, '\x00') ||
			strings.ContainsAny(logFile, "\r\n") {
			return ""
		}

		absolutePath, err := filepath.Abs(logFile)
		if err != nil {
			return filepath.Clean(logFile)
		}

		return filepath.Clean(absolutePath)

	default:
		return ""
	}
}

