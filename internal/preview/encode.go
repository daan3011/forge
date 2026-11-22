package preview

import (
	"bytes"
	"image"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

// KittyTransmit is the kitty graphics sequence that uploads img as image
// id with a virtual placement cols x rows cells big; Placeholders then
// shows it. The image is padded to whole cells so the terminal doesn't
// stretch it. wrap is applied to every chunk (tmux passthrough); nil
// leaves them as they are.
func KittyTransmit(img *image.RGBA, id, cols, rows, cellW, cellH int, wrap func(string) string) (string, error) {
	var buf bytes.Buffer
	err := kitty.EncodeGraphics(&buf, pad(img, cols*cellW, rows*cellH), &kitty.Options{
		Action:           kitty.TransmitAndPut,
		Quiet:            2,
		ID:               id,
		Format:           kitty.PNG,
		Transmission:     kitty.Direct, // must be set: EncodeGraphics encodes nothing for the zero value
		Chunk:            true,
		ChunkFormatter:   wrap,
		VirtualPlacement: true,
		Columns:          cols,
		Rows:             rows,
	})
	if err != nil {
		return "", err //nolint:wrapcheck
	}
	return buf.String(), nil
}

// KittyDelete frees image id (its placements and its data).
func KittyDelete(id int) string {
	return ansi.KittyGraphics(nil, "a=d", "d=I", "i="+strconv.Itoa(id), "q=2")
}

// Placeholders is the text that shows kitty image id in cols x rows
// cells: Unicode placeholders coloured with the id (256-colour index, so
// ids are 1-255). Each row's first cell names its row and column; the
// terminal infers the rest of the row.
func Placeholders(id, cols, rows int) string {
	var b strings.Builder
	fg := "\x1b[38;5;" + strconv.Itoa(id) + "m"
	for r := range rows {
		if r > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(fg)
		b.WriteRune(kitty.Placeholder)
		b.WriteRune(kitty.Diacritic(r))
		b.WriteRune(kitty.Diacritic(0))
		for range cols - 1 {
			b.WriteRune(kitty.Placeholder)
		}
		b.WriteString("\x1b[39m")
	}
	return b.String()
}

// Sixel encodes img as a sixel sequence (transparent pixels keep the
// background). See sixel.go.
func Sixel(img *image.RGBA) string {
	return ansi.SixelGraphics(0, 1, 0, encodeSixel(img))
}

// Place draws seq with its top left at the zero-based cell (col, row) and
// puts the cursor back where it was.
func Place(seq string, col, row int) string {
	return ansi.SaveCursor + ansi.CursorPosition(col+1, row+1) + seq + ansi.RestoreCursor
}
