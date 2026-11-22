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

// HumanBytes formats a size, 1024-based: "512 B", "3.4 KB", "1.2 MB".
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 3; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// USD formats an estimated cost: "$0.42", "$12.30", "$1234"; amounts under
// a cent show as "<$0.01".
func USD(v float64) string {
	switch {
	case v <= 0:
		return "$0"
	case v < 0.01:
		return "<$0.01"
	case v < 1000:
		return fmt.Sprintf("$%.2f", v)
	}
	return fmt.Sprintf("$%.0f", v)
}
