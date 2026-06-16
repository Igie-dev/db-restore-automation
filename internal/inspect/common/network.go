package common

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

func InspectTCP(ctx context.Context, report *JobReport, name, host string, port int) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		report.Fail(name, "host is not configured", "")
		return false
	}
	if port < 1 || port > 65535 {
		report.Fail(name, "port must be between 1 and 65535", fmt.Sprint(port))
		return false
	}

	dialer := net.Dialer{Timeout: 10 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		report.Fail(name, fmt.Sprintf("TCP connection failed: %v", err), net.JoinHostPort(host, fmt.Sprint(port)))
		return false
	}
	_ = connection.Close()
	report.Pass(name, "TCP connection succeeded", net.JoinHostPort(host, fmt.Sprint(port)))
	return true
}
