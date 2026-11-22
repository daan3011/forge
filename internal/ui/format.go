package ui

import (
	"fmt"
	"strings"
)

// CompactCount formats a count for narrow columns: 950, 1.2k, 12k, 3.4M.
func CompactCount(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 10_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1000), ".0") + "k"
	case n < 1_000_000:
		return fmt.Sprintf("%dk", n/1000)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000_000), ".0") + "M"
}
