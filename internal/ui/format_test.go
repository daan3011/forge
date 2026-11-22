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

func TestHumanBytes(t *testing.T) {
	tests := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 1536: "1.5 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB"}
	for n, want := range tests {
		if got := HumanBytes(n); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestUSD(t *testing.T) {
	tests := map[float64]string{0: "$0", 0.004: "<$0.01", 0.42: "$0.42", 12.3: "$12.30", 1234.4: "$1234"}
	for v, want := range tests {
		if got := USD(v); got != want {
			t.Errorf("USD(%v) = %q, want %q", v, got, want)
		}
	}
}
