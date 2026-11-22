// Package usage aggregates Claude Code token usage (P5.4).
//
// It reads every transcript under ~/.claude/projects (subagent transcripts
// included) into a ledger keyed by message id, so a message copied into a
// resumed or forked session's file is counted once. The daily rollups in the
// usage table are rebuilt from that ledger after each scan, with the project
// worked out from the forge session or the working directory. Costs are
// estimated from the user's price table (config usage.prices); forge ships
// no prices.
package usage
