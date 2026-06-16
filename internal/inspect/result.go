package inspect

import "db-restore-automation/internal/inspect/common"

type Status = common.Status

type Check = common.Check
type Candidate = common.Candidate
type JobReport = common.JobReport
type Summary = common.Summary
type Report = common.Report
type Options = common.Options

const (
	StatusPass = common.StatusPass
	StatusWarn = common.StatusWarn
	StatusFail = common.StatusFail
	StatusInfo = common.StatusInfo

	ExitOK       = common.ExitOK
	ExitWarnings = common.ExitWarnings
	ExitFailure  = common.ExitFailure
	ExitUsage    = common.ExitUsage
)
