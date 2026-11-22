// Package preview builds the Artifacts tab's inline previews (P4.7):
// images decoded and scaled to the detail pane, SVGs rasterised with
// rsvg-convert, chafa text art for terminals without graphics, HTML
// summaries, and the kitty / sixel escape sequences that draw them. It has
// no TUI code; the tab decides when and where to show the result.
package preview
