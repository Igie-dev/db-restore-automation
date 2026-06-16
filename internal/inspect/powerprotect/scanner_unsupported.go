//go:build !windows

package powerprotect

import (
	"context"
	"os"

	"db-restore-automation/internal/inspect/common"
)

func inspectPlatform(_ context.Context, _ common.Job, _ common.Options) (string, []string, []common.Candidate, []string) {
	hostname, _ := os.Hostname()
	return hostname, nil, nil, []string{"PowerProtect discovery is supported only on Windows"}
}
