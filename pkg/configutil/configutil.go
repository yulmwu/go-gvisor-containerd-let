package configutil

import (
	"os"
	"strings"
)

func SplitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.ToUpper(strings.TrimSpace(p)))
	}

	return out
}

func CSVEnv(key string, def []string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return append([]string(nil), def...)
	}

	out := make([]string, 0)
	for _, p := range SplitCSV(raw) {
		if p != "" {
			out = append(out, p)
		}
	}

	if len(out) == 0 {
		return append([]string(nil), def...)
	}

	return out
}
