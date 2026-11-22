package usage

import (
	"math"
	"testing"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/store"
)

func TestPrices(t *testing.T) {
	p := Prices{
		"claude-opus":     {Input: 1, Output: 2},
		"claude-opus-5-5": {Input: 10, Output: 20, CacheRead: 1, CacheWrite: 12.5},
		"claude-haiku":    {}, // placeholder
	}
	tok := store.Tokens{Input: 1_000_000, Output: 500_000, CacheRead: 2_000_000, CacheWrite: 100_000}
	tests := []struct {
		model  string
		usd    float64
		priced bool
	}{
		{"claude-opus-5-5", 10 + 10 + 2 + 1.25, true},
		{"claude-opus-5-5-20261101", 10 + 10 + 2 + 1.25, true}, // longest prefix
		{"claude-opus-4", 1 + 1, true},
		{"claude-haiku-5-5", 0, false},
		{"gpt", 0, false},
	}
	for _, tt := range tests {
		usd, ok := p.Cost(tt.model, tok)
		if ok != tt.priced || math.Abs(usd-tt.usd) > 1e-9 {
			t.Errorf("%s: %v %v, want %v %v", tt.model, usd, ok, tt.usd, tt.priced)
		}
	}
}

func TestTotals(t *testing.T) {
	p := Prices{"a": config.Price{Input: 1e6}}
	var x Totals
	x.Add(p, "b", store.Tokens{Output: 5, Messages: 1})
	if x.Priced() || x.Partial() || x.Unpriced != 5 {
		t.Errorf("unpriced only: %+v", x)
	}
	x.Add(p, "a", store.Tokens{Input: 2, Messages: 1})
	if !x.Priced() || !x.Partial() || x.Cost != 2 || x.Total() != 7 || x.Messages != 2 {
		t.Errorf("mixed: %+v", x)
	}
}
