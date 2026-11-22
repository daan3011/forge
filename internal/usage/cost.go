package usage

import (
	"strings"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/store"
)

// Prices estimates costs from the user's price table (config usage.prices).
type Prices map[string]config.Price

// Lookup finds the price of model: an exact key, else the longest key the
// model starts with (so "claude-opus-5-5" covers dated ids). ok is false
// when there is none or it was left at 0.
func (p Prices) Lookup(model string) (config.Price, bool) {
	if pr, ok := p[model]; ok {
		return pr, !pr.IsZero()
	}
	best := ""
	for k := range p {
		if k != "" && strings.HasPrefix(model, k) && len(k) > len(best) {
			best = k
		}
	}
	if best == "" {
		return config.Price{}, false
	}
	pr := p[best]
	return pr, !pr.IsZero()
}

// Cost is the estimated USD cost of t on model; priced is false when the
// model has no price.
func (p Prices) Cost(model string, t store.Tokens) (usd float64, priced bool) {
	pr, ok := p.Lookup(model)
	if !ok {
		return 0, false
	}
	usd = (float64(t.Input)*pr.Input + float64(t.Output)*pr.Output +
		float64(t.CacheRead)*pr.CacheRead + float64(t.CacheWrite)*pr.CacheWrite) / 1e6
	return usd, true
}

// Totals is tokens plus their estimated cost.
type Totals struct {
	store.Tokens
	// Cost is the estimated USD cost of the priced tokens.
	Cost float64
	// Unpriced counts tokens of models without a price.
	Unpriced int64
}

// Add adds t's tokens on model, pricing them with p.
func (x *Totals) Add(p Prices, model string, t store.Tokens) {
	x.Input += t.Input
	x.Output += t.Output
	x.CacheRead += t.CacheRead
	x.CacheWrite += t.CacheWrite
	x.Messages += t.Messages
	if usd, ok := p.Cost(model, t); ok {
		x.Cost += usd
	} else {
		x.Unpriced += t.Total()
	}
}

// Priced reports whether any of the tokens had a price.
func (x Totals) Priced() bool { return x.Total() > x.Unpriced }

// Partial reports whether some, but not all, tokens had a price.
func (x Totals) Partial() bool { return x.Priced() && x.Unpriced > 0 }
