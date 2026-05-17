package api

import (
	"strings"
)

func cleanStringList(values []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(values))
	for i := range values {
		v := strings.TrimSpace(values[i])
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
