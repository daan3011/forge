package app

import (
	"slices"
	"strings"

	"github.com/sahilm/fuzzy"
)

const (
	// recentBoosted is how many of the most recent items get a score bonus.
	recentBoosted = 10
	// recentBonus is the bonus per place for the most recent item; it falls
	// off linearly over the boosted places.
	recentBonus = 3
	// maxRecent is how many recent ids are kept.
	maxRecent = 20
)

type itemSource []item

func (s itemSource) String(i int) string { return s[i].searchText() }
func (s itemSource) Len() int            { return len(s) }

// rank orders items for the palette. With an empty query the recently used
// items come first under "Recent", then everything else in registry order.
// Otherwise items are fuzzy-matched, recently used ones get a bonus, and the
// results are grouped: groups ordered by their best match, items within a
// group by score. Ties keep registry order.
func rank(items []item, query string, recent []string) []item {
	if strings.TrimSpace(query) == "" {
		return rankEmpty(items, recent)
	}

	bonus := make(map[string]int, recentBoosted)
	for i, id := range recent[:min(len(recent), recentBoosted)] {
		bonus[id] = (recentBoosted - i) * recentBonus
	}

	type scored struct {
		idx, score int
	}
	matches := fuzzy.FindFrom(query, itemSource(items))
	results := make([]scored, len(matches))
	best := map[string]int{}     // group -> best score
	firstIdx := map[string]int{} // group -> first registry index
	for i, mt := range matches {
		it := items[mt.Index]
		s := scored{idx: mt.Index, score: mt.Score + bonus[it.id]}
		results[i] = s
		if b, ok := best[it.group]; !ok || s.score > b {
			best[it.group] = s.score
		}
	}
	for i, it := range items {
		if _, ok := firstIdx[it.group]; !ok {
			firstIdx[it.group] = i
		}
	}

	slices.SortStableFunc(results, func(a, b scored) int {
		ga, gb := items[a.idx].group, items[b.idx].group
		if ga != gb {
			if best[ga] != best[gb] {
				return best[gb] - best[ga]
			}
			return firstIdx[ga] - firstIdx[gb]
		}
		if a.score != b.score {
			return b.score - a.score
		}
		return a.idx - b.idx
	})
	out := make([]item, len(results))
	for i, r := range results {
		out[i] = items[r.idx]
	}
	return out
}

func rankEmpty(items []item, recent []string) []item {
	byID := make(map[string]int, len(items))
	for i, it := range items {
		if _, dup := byID[it.id]; !dup {
			byID[it.id] = i
		}
	}
	out := make([]item, 0, len(items))
	used := map[int]bool{}
	for _, id := range recent {
		i, ok := byID[id]
		if !ok || used[i] {
			continue
		}
		used[i] = true
		it := items[i]
		it.group = groupRecent
		out = append(out, it)
	}
	for i, it := range items {
		if !used[i] {
			out = append(out, it)
		}
	}
	return out
}

// pushRecent moves id to the front of recent, capped at maxRecent.
func pushRecent(recent []string, id string) []string {
	out := make([]string, 0, min(len(recent)+1, maxRecent))
	out = append(out, id)
	for _, r := range recent {
		if r != id && len(out) < maxRecent {
			out = append(out, r)
		}
	}
	return out
}

// mergeRecent puts the in-session ids first, then the loaded ones.
func mergeRecent(session, loaded []string) []string {
	out := slices.Clone(session)
	for _, id := range loaded {
		if len(out) >= maxRecent {
			break
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
