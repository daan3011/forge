package ui

import "testing"

func TestCompactCount(t *testing.T) {
	tests := map[int]string{0: "0", 950: "950", 1000: "1k", 1234: "1.2k", 9999: "10k", 12_345: "12k", 3_400_000: "3.4M"}
	for n, want := range tests {
		if got := CompactCount(n); got != want {
			t.Errorf("CompactCount(%d) = %q, want %q", n, got, want)
		}
	}
}
