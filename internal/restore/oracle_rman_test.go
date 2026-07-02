package restore

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/shell"
)

type fakeRunner struct {
	calls   int
	lastCmd shell.Command
	result  shell.Result
	err     error
	onRun   func()
}

func (f *fakeRunner) Run(
	ctx context.Context,
	cmdSpec shell.Command,
) (shell.Result, error) {
	f.calls++
	f.lastCmd = cmdSpec

	if f.onRun != nil {
		f.onRun()
	}

	return f.result, f.err
}

func rmanExecutableName() string {
	if runtime.GOOS == "windows" {
		return "rman.exe"
	}

	return "rman"
}

// newRMANFixture builds an on-disk layout that satisfies every provider
// precondition: a fake ORACLE_HOME with an rman binary, a non-empty command
// file, and a log path in a directory that does not exist yet.
func newRMANFixture(t *testing.T) (config.Config, config.JobConfig) {
	t.Helper()

	base := t.TempDir()

	oracleHome := filepath.Join(base, "dbhome_1")
	binDir := filepath.Join(oracleHome, "bin")

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create fake ORACLE_HOME: %v", err)
	}

	rmanBinary := filepath.Join(binDir, rmanExecutableName())
	if err := os.WriteFile(
		rmanBinary,
		[]byte("#!/bin/sh\nexit 0\n"),
		0o755,
	); err != nil {
		t.Fatalf("create fake rman binary: %v", err)
	}

	commandFile := filepath.Join(base, "restore_sales.rman")
	if err := os.WriteFile(
		commandFile,
		[]byte("RESTORE DATABASE;\nRECOVER DATABASE;\n"),
		0o644,
	); err != nil {
		t.Fatalf("create command file: %v", err)
	}

	logFile := filepath.Join(base, "logs", "sales_rman.log")

	enabled := true

	job := config.JobConfig{
		Name:    "sales_rman_restore",
		Enabled: &enabled,
		Type:    config.TypeOracleRMAN,
		RMAN: config.RMANConfig{
			Target:      "/",
			CommandFile: commandFile,
			LogFile:     logFile,
			OracleHome:  oracleHome,
			OracleSID:   "SALESTST",
		},
	}

	cfg := config.Config{
		Tools: config.ToolsConfig{
			OracleRMAN: config.OracleRMANToolsConfig{
				RMAN: "rman",
			},
		},
	}

	return cfg, job
}

func environmentValue(env []string, key string) (string, bool) {
	prefix := key + "="

	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return entry[len(prefix):], true
		}
	}

	return "", false
}

func TestRestoreBuildsExpectedRMANCommand(t *testing.T) {
	cfg, job := newRMANFixture(t)

	logFile := job.RMAN.LogFile

	runner := &fakeRunner{
		result: shell.Result{ExitCode: 0},
		onRun: func() {
			// Simulate RMAN writing its own log file.
			_ = os.WriteFile(
				logFile,
				[]byte("Recovery Manager complete.\n"),
				0o644,
			)
		},
	}

	provider := OracleRmanProvider{Runner: runner}

	if err := provider.Restore(
		context.Background(),
		cfg,
		job,
		Options{},
	); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}

	if runner.calls != 1 {
		t.Fatalf("expected exactly one command execution, got %d", runner.calls)
	}

	wantExecutable := filepath.Join(
		job.RMAN.OracleHome,
		"bin",
		rmanExecutableName(),
	)

	if runner.lastCmd.Executable != wantExecutable {
		t.Errorf(
			"executable = %q, want %q",
			runner.lastCmd.Executable,
			wantExecutable,
		)
	}

	wantArgs := []string{
		"target",
		"/",
		"cmdfile=" + job.RMAN.CommandFile,
		"log=" + logFile,
	}

	if len(runner.lastCmd.Args) != len(wantArgs) {
		t.Fatalf(
			"args = %v, want %v",
			runner.lastCmd.Args,
			wantArgs,
		)
	}

	for index, want := range wantArgs {
		if runner.lastCmd.Args[index] != want {
			t.Errorf(
				"args[%d] = %q, want %q",
				index,
				runner.lastCmd.Args[index],
				want,
			)
		}
	}

	if value, ok := environmentValue(
		runner.lastCmd.Env,
		"ORACLE_HOME",
	); !ok || value != job.RMAN.OracleHome {
		t.Errorf(
			"ORACLE_HOME = %q (present=%v), want %q",
			value,
			ok,
			job.RMAN.OracleHome,
		)
	}

	if value, ok := environmentValue(
		runner.lastCmd.Env,
		"ORACLE_SID",
	); !ok || value != "SALESTST" {
		t.Errorf(
			"ORACLE_SID = %q (present=%v), want SALESTST",
			value,
			ok,
		)
	}

	pathValue, ok := environmentValue(runner.lastCmd.Env, "PATH")
	oracleBin := filepath.Join(job.RMAN.OracleHome, "bin")

	if !ok || !strings.HasPrefix(pathValue, oracleBin) {
		t.Errorf(
			"PATH = %q, want prefix %q",
			pathValue,
			oracleBin,
		)
	}

	if runtime.GOOS != "windows" {
		libValue, ok := environmentValue(
			runner.lastCmd.Env,
			"LD_LIBRARY_PATH",
		)
		oracleLib := filepath.Join(job.RMAN.OracleHome, "lib")

		if !ok || !strings.HasPrefix(libValue, oracleLib) {
			t.Errorf(
				"LD_LIBRARY_PATH = %q, want prefix %q",
				libValue,
				oracleLib,
			)
		}
	}
}

func TestRestoreIncludesCatalogArguments(t *testing.T) {
	cfg, job := newRMANFixture(t)
	job.RMAN.Catalog = "/@RCOCAT"

	runner := &fakeRunner{result: shell.Result{ExitCode: 0}}
	provider := OracleRmanProvider{Runner: runner}

	if err := provider.Restore(
		context.Background(),
		cfg,
		job,
		Options{},
	); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}

	wantPrefix := []string{"target", "/", "catalog", "/@RCOCAT"}

	if len(runner.lastCmd.Args) < len(wantPrefix) {
		t.Fatalf("args = %v, want prefix %v", runner.lastCmd.Args, wantPrefix)
	}

	for index, want := range wantPrefix {
		if runner.lastCmd.Args[index] != want {
			t.Errorf(
				"args[%d] = %q, want %q",
				index,
				runner.lastCmd.Args[index],
				want,
			)
		}
	}
}

func TestRestoreDryRunDoesNotExecute(t *testing.T) {
	cfg, job := newRMANFixture(t)

	runner := &fakeRunner{result: shell.Result{ExitCode: 0}}
	provider := OracleRmanProvider{Runner: runner}

	if err := provider.Restore(
		context.Background(),
		cfg,
		job,
		Options{DryRun: true},
	); err != nil {
		t.Fatalf("dry-run Restore returned error: %v", err)
	}

	if runner.calls != 0 {
		t.Fatalf(
			"dry run must not execute commands, got %d executions",
			runner.calls,
		)
	}
}

func TestRestoreFailsOnNonZeroExit(t *testing.T) {
	cfg, job := newRMANFixture(t)

	runner := &fakeRunner{result: shell.Result{ExitCode: 1}}
	provider := OracleRmanProvider{Runner: runner}

	err := provider.Restore(context.Background(), cfg, job, Options{})
	if err == nil {
		t.Fatal("expected error for exit code 1, got nil")
	}

	if !strings.Contains(err.Error(), "exit code 1") {
		t.Errorf("error %q does not mention exit code", err.Error())
	}
}

func TestRestoreRejectsPromptingConnectStrings(t *testing.T) {
	tests := []struct {
		name             string
		credentialMethod string
		target           string
		catalog          string
		wantSubstring    string
	}{
		{
			name:          "os_auth target with username",
			target:        "sys@ORCL",
			wantSubstring: "rman.target",
		},
		{
			name:             "wallet target without wallet form",
			credentialMethod: "oracle_wallet",
			target:           "/",
			wantSubstring:    "rman.target",
		},
		{
			name:          "catalog without wallet form",
			target:        "/",
			catalog:       "rco@catdb",
			wantSubstring: "rman.catalog",
		},
		{
			name:          "inline password in target",
			target:        "sys/secret@ORCL",
			wantSubstring: "inline password",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, job := newRMANFixture(t)

			job.RMAN.Target = test.target
			job.RMAN.Catalog = test.catalog

			if test.credentialMethod != "" {
				job.RMAN.CredentialMethod = test.credentialMethod
			}

			runner := &fakeRunner{result: shell.Result{ExitCode: 0}}
			provider := OracleRmanProvider{Runner: runner}

			err := provider.Restore(
				context.Background(),
				cfg,
				job,
				Options{},
			)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}

			if !strings.Contains(err.Error(), test.wantSubstring) {
				t.Errorf(
					"error %q does not contain %q",
					err.Error(),
					test.wantSubstring,
				)
			}

			if runner.calls != 0 {
				t.Errorf(
					"validation failure must not execute commands, got %d",
					runner.calls,
				)
			}
		})
	}
}

func TestRMANContainsInlinePassword(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"/", false},
		{"/@ORCL", false},
		{"/@//db01:1521/SALES", false},
		{"sys/secret@ORCL", true},
		{"user/pass", true},
		{"sys@ORCL", false},
	}

	for _, test := range tests {
		if got := rmanContainsInlinePassword(test.value); got != test.want {
			t.Errorf(
				"rmanContainsInlinePassword(%q) = %v, want %v",
				test.value,
				got,
				test.want,
			)
		}
	}
}

func TestRMANWalletConnectSpec(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"/@ORCL", true},
		{"/@//db01:1521/SALES", true},
		{"/@", false},
		{"/", false},
		{"rco@catdb", false},
		{"", false},
	}

	for _, test := range tests {
		if got := rmanWalletConnectSpec(test.value); got != test.want {
			t.Errorf(
				"rmanWalletConnectSpec(%q) = %v, want %v",
				test.value,
				got,
				test.want,
			)
		}
	}
}

func TestRMANValidateTargetSpec(t *testing.T) {
	tests := []struct {
		name             string
		value            string
		credentialMethod string
		wantErr          bool
	}{
		{"os_auth bequeath", "/", "os_auth", false},
		{"os_auth rejects username", "sys@ORCL", "os_auth", true},
		{"os_auth rejects wallet form", "/@ORCL", "os_auth", true},
		{"wallet alias", "/@ORCL", "oracle_wallet", false},
		{"wallet rejects bequeath", "/", "oracle_wallet", true},
		{"wallet rejects empty alias", "/@", "oracle_wallet", true},
		{"rejects whitespace", "/ as sysdba", "os_auth", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := rmanValidateTargetSpec(
				"rman.target",
				test.value,
				test.credentialMethod,
			)

			if (err != nil) != test.wantErr {
				t.Errorf(
					"rmanValidateTargetSpec(%q, %q) error = %v, wantErr %v",
					test.value,
					test.credentialMethod,
					err,
					test.wantErr,
				)
			}
		})
	}
}

func TestRMANValidOracleSID(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"ORCL", true},
		{"salestst", true},
		{"db_1$#", true},
		{"1abc", false},
		{"or cl", false},
		{"", false},
		{"orcl;rm", false},
	}

	for _, test := range tests {
		if got := rmanValidOracleSID(test.value); got != test.want {
			t.Errorf(
				"rmanValidOracleSID(%q) = %v, want %v",
				test.value,
				got,
				test.want,
			)
		}
	}
}

func TestRMANLogWasUpdated(t *testing.T) {
	base := time.Date(2026, 7, 2, 2, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		before rmanLogSnapshot
		after  rmanLogSnapshot
		want   bool
	}{
		{
			name:   "log never appeared",
			before: rmanLogSnapshot{},
			after:  rmanLogSnapshot{},
			want:   false,
		},
		{
			name:   "log created during run",
			before: rmanLogSnapshot{},
			after: rmanLogSnapshot{
				Exists:       true,
				Size:         10,
				ModifiedTime: base,
			},
			want: true,
		},
		{
			name: "size changed",
			before: rmanLogSnapshot{
				Exists:       true,
				Size:         10,
				ModifiedTime: base,
			},
			after: rmanLogSnapshot{
				Exists:       true,
				Size:         20,
				ModifiedTime: base,
			},
			want: true,
		},
		{
			name: "modified time changed",
			before: rmanLogSnapshot{
				Exists:       true,
				Size:         10,
				ModifiedTime: base,
			},
			after: rmanLogSnapshot{
				Exists:       true,
				Size:         10,
				ModifiedTime: base.Add(time.Minute),
			},
			want: true,
		},
		{
			name: "stale log untouched by run",
			before: rmanLogSnapshot{
				Exists:       true,
				Size:         10,
				ModifiedTime: base,
			},
			after: rmanLogSnapshot{
				Exists:       true,
				Size:         10,
				ModifiedTime: base,
			},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := rmanLogWasUpdated(
				test.before,
				test.after,
			); got != test.want {
				t.Errorf(
					"rmanLogWasUpdated() = %v, want %v",
					got,
					test.want,
				)
			}
		})
	}
}

func TestRMANResolveExecutable(t *testing.T) {
	base := t.TempDir()

	oracleHome := filepath.Join(base, "dbhome_1")
	binDir := filepath.Join(oracleHome, "bin")

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin directory: %v", err)
	}

	candidate := filepath.Join(binDir, rmanExecutableName())
	if err := os.WriteFile(
		candidate,
		[]byte("stub"),
		0o755,
	); err != nil {
		t.Fatalf("create stub rman: %v", err)
	}

	if got := rmanResolveExecutable("rman", oracleHome); got != candidate {
		t.Errorf(
			"bare name with existing candidate = %q, want %q",
			got,
			candidate,
		)
	}

	missingHome := filepath.Join(base, "missing_home")

	if got := rmanResolveExecutable("rman", missingHome); got != "rman" {
		t.Errorf(
			"bare name without candidate = %q, want bare name",
			got,
		)
	}

	explicit := filepath.Join(base, "custom", "rman")

	if got := rmanResolveExecutable(explicit, oracleHome); got != explicit {
		t.Errorf(
			"explicit path = %q, want unchanged %q",
			got,
			explicit,
		)
	}
}

func TestRMANSetEnvironmentValue(t *testing.T) {
	environment := []string{
		"ORACLE_HOME=/old/home",
		"KEEP=1",
	}

	result := rmanSetEnvironmentValue(
		environment,
		"ORACLE_HOME",
		"/new/home",
	)

	replacements := 0

	for _, entry := range result {
		if strings.HasPrefix(entry, "ORACLE_HOME=") {
			replacements++

			if entry != "ORACLE_HOME=/new/home" {
				t.Errorf("unexpected ORACLE_HOME entry %q", entry)
			}
		}
	}

	if replacements != 1 {
		t.Errorf("ORACLE_HOME appears %d times, want exactly 1", replacements)
	}

	if _, ok := environmentValue(result, "KEEP"); !ok {
		t.Error("unrelated entry KEEP was dropped")
	}
}
