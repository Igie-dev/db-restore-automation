package inspect

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func writeReport(writer io.Writer, report Report, format string) error {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		return writeJSONReport(writer, report)
	case "text", "":
		return writeTextReport(writer, report)
	default:
		return fmt.Errorf("unsupported output format %q; use text or json", format)
	}
}

func writeJSONReport(writer io.Writer, report Report) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
