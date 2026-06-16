package inspect

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"db-restore-automation/internal/inspect/common"
)

const Usage = `Usage:
  db-restore-automation inspect --config <file> [options]
  db-restore-automation inspect --discover [options]

Options:
  --config <file>           Restore jobs YAML file.
  --job <name>              Inspect only one job, case-insensitively.
  --type <provider>         Inspect only one provider type.
  --format <text|json>      Output format. Default: text.
  --test-connection         Run explicit read-only connectivity checks where safe.
  --include-disabled        Include disabled jobs.
  --discover                Inspect installed tools without requiring a config file.
  --timeout <duration>      Maximum duration per job. Default: 30s.
  --max-scan-file-size <n>  Maximum PowerProtect text file size in bytes. Default: 20971520.
  --max-scan-matches <n>    Maximum PowerProtect candidates. Default: 500.

Examples:
  db-restore-automation inspect --config ./config/restore-jobs.windows.yml
  db-restore-automation inspect --config ./config/restore-jobs.windows.yml --job AdventureWorksRestore
  db-restore-automation inspect --config ./config/restore-jobs.windows.yml --type postgres --test-connection
  db-restore-automation inspect --discover --format json
`

func RunCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	options := common.DefaultOptions()
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		_, _ = fmt.Fprint(stderr, Usage)
	}

	flags.StringVar(&options.ConfigPath, "config", "", "restore jobs YAML file")
	flags.StringVar(&options.JobName, "job", "", "inspect one job")
	flags.StringVar(&options.ProviderType, "type", "", "inspect one provider type")
	flags.StringVar(&options.Format, "format", options.Format, "text or json")
	flags.BoolVar(&options.TestConnection, "test-connection", false, "run read-only connectivity checks")
	flags.BoolVar(&options.IncludeDisabled, "include-disabled", false, "include disabled jobs")
	flags.BoolVar(&options.Discover, "discover", false, "inspect installed tools without config")
	flags.DurationVar(&options.Timeout, "timeout", options.Timeout, "maximum duration per job")
	flags.Int64Var(&options.MaxScanFileSize, "max-scan-file-size", options.MaxScanFileSize, "maximum PowerProtect scan file size")
	flags.IntVar(&options.MaxScanMatches, "max-scan-matches", options.MaxScanMatches, "maximum PowerProtect candidate matches")

	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n\n%s", strings.Join(flags.Args(), " "), Usage)
		return ExitUsage
	}
	if options.Timeout <= 0 {
		fmt.Fprintln(stderr, "--timeout must be greater than zero")
		return ExitUsage
	}
	if options.MaxScanFileSize <= 0 || options.MaxScanMatches <= 0 {
		fmt.Fprintln(stderr, "--max-scan-file-size and --max-scan-matches must be greater than zero")
		return ExitUsage
	}
	if options.Discover && options.JobName != "" {
		fmt.Fprintln(stderr, "--job cannot be combined with --discover")
		return ExitUsage
	}
	if !options.Discover && strings.TrimSpace(options.ConfigPath) == "" {
		fmt.Fprintf(stderr, "--config is required unless --discover is used\n\n%s", Usage)
		return ExitUsage
	}
	if options.ProviderType != "" {
		options.ProviderType = common.NormalizeProviderType(options.ProviderType)
		if !common.IsSupportedProvider(options.ProviderType) {
			fmt.Fprintln(stderr, common.UnsupportedProviderMessage(options.ProviderType))
			return ExitUsage
		}
	}
	format := strings.ToLower(strings.TrimSpace(options.Format))
	if format != "text" && format != "json" {
		fmt.Fprintln(stderr, "--format must be text or json")
		return ExitUsage
	}
	options.Format = format

	report, err := Run(ctx, options)
	if err != nil {
		fmt.Fprintf(stderr, "inspection failed: %v\n", err)
		return ExitFailure
	}
	if err := writeReport(stdout, report, options.Format); err != nil {
		fmt.Fprintf(stderr, "write inspection report: %v\n", err)
		return ExitFailure
	}
	return report.ExitCode()
}

func Run(ctx context.Context, options Options) (Report, error) {
	defaults := common.DefaultOptions()
	if options.Format == "" {
		options.Format = defaults.Format
	}
	if options.Timeout <= 0 {
		options.Timeout = defaults.Timeout
	}
	if options.MaxScanFileSize <= 0 {
		options.MaxScanFileSize = defaults.MaxScanFileSize
	}
	if options.MaxScanMatches <= 0 {
		options.MaxScanMatches = defaults.MaxScanMatches
	}

	var config *common.Config
	var err error
	if options.Discover {
		config = common.EmptyConfig()
	} else {
		config, err = common.LoadConfig(options.ConfigPath)
		if err != nil {
			return Report{}, err
		}
	}
	return runInspection(ctx, config, options)
}
