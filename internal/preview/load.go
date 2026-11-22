package preview

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"  // GIF decoder (first frame)
	_ "image/jpeg" // JPEG decoder
	_ "image/png"  // PNG decoder
	"io"
	"os"
	"strings"

	"github.com/daan3011/forge/internal/chafa"
	"github.com/daan3011/forge/internal/rsvg"
)

// Limits on what gets decoded: bigger files show the card instead.
const (
	MaxFileSize = 50 << 20   // bytes
	MaxPixels   = 24_000_000 // width x height
)

// Kind is what a preview shows.
type Kind int

// Kinds of preview.
const (
	Card  Kind = iota // metadata only (Note says why there's no picture)
	Image             // pixels for kitty or sixel (Result.Image)
	Art               // chafa text art (Result.Art)
	HTML              // title and visible text (Result.Title, Result.Lines)
)

// Result is a loaded preview.
type Result struct {
	Kind Kind
	// Image is the picture scaled to fit, starting at (0, 0).
	Image *image.RGBA
	// Cols and Rows are the cells the image covers (Rows only for art).
	Cols, Rows int
	// Width and Height are the image's own size in pixels (0 if unknown).
	Width, Height int
	// Art is chafa's output.
	Art string
	// Title and Lines summarise an HTML page.
	Title string
	Lines []string
	// Note says why there's no picture, for the card ("" for types that
	// never have one).
	Note string
}

// Options say what the terminal can show.
type Options struct {
	Box
	// Graphics means pixels can be drawn (kitty or sixel).
	Graphics bool
	// Chafa allows the chafa fallback when there are no graphics.
	Chafa bool
	// Reason says why there are no graphics, for the card.
	Reason string
}

// Deps are the external tools.
type Deps struct {
	Chafa chafa.Renderer  // nil: no chafa
	Rsvg  rsvg.Rasterizer // nil: no SVG pictures
	// TempDir holds rasterised SVGs on their way to chafa; "" is the
	// system default.
	TempDir string
}

func (d Deps) hasChafa() bool { return d.Chafa != nil && d.Chafa.Available() }
func (d Deps) hasRsvg() bool  { return d.Rsvg != nil && d.Rsvg.Available() }

// Load builds the preview of the file at path, whose artifact type is typ
// (artifacts.Type). It never fails: problems become a card with a note.
func Load(ctx context.Context, path, typ string, o Options, d Deps) Result {
	switch typ {
	case "html":
		return loadHTML(path)
	case "png", "jpg", "gif":
		return loadRaster(ctx, path, o, d)
	case "svg":
		return loadSVG(ctx, path, o, d)
	case "webp":
		return Result{Note: "no preview for webp"}
	}
	return Result{}
}

func loadHTML(path string) Result {
	f, err := os.Open(path) //nolint:gosec // an artifact the user is looking at
	if err != nil {
		return Result{Note: "can't read: " + err.Error()}
	}
	defer f.Close()
	title, lines, err := ExtractHTML(f, HTMLReadLimit)
	if err != nil {
		return Result{Note: "can't read: " + err.Error()}
	}
	return Result{Kind: HTML, Title: title, Lines: lines}
}

func loadRaster(ctx context.Context, path string, o Options, d Deps) Result {
	f, err := os.Open(path) //nolint:gosec // an artifact the user is looking at
	if err != nil {
		return Result{Note: "can't read: " + err.Error()}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Result{Note: "can't read: " + err.Error()}
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return Result{Note: "can't decode: " + err.Error()}
	}
	res := Result{Width: cfg.Width, Height: cfg.Height}
	switch {
	case o.Graphics:
		if note := tooBig(st.Size(), cfg); note != "" {
			res.Note = note
			return res
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			res.Note = "can't read: " + err.Error()
			return res
		}
		img, _, err := image.Decode(f)
		if err != nil {
			res.Note = "can't decode: " + err.Error()
			return res
		}
		return fitImage(res, img, o.Box)
	case o.Chafa && d.hasChafa():
		return art(ctx, res, path, o, d)
	}
	res.Note = noPicture(o, d)
	return res
}

func loadSVG(ctx context.Context, path string, o Options, d Deps) Result {
	if !o.Graphics && (!o.Chafa || !d.hasChafa()) {
		return Result{Note: noPicture(o, d)}
	}
	if !d.hasRsvg() {
		return Result{Note: "install librsvg (rsvg-convert) for SVG previews"}
	}
	// Rasterise at the size it will be shown: chafa's cells are drawn
	// from a picture about 8x16 pixels per cell.
	b := o.Box
	if !o.Graphics {
		b.CellW, b.CellH = 8, 16
	}
	w, h := b.px()
	data, err := d.Rsvg.PNG(ctx, path, w, h)
	if err != nil {
		return Result{Note: err.Error()}
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{Note: "can't decode rasterised SVG: " + err.Error()}
	}
	res := Result{Width: img.Bounds().Dx(), Height: img.Bounds().Dy()}
	if o.Graphics {
		return fitImage(res, img, o.Box)
	}
	tmp, err := os.CreateTemp(d.TempDir, "forge-preview-*.png")
	if err != nil {
		res.Note = "can't write temp file: " + err.Error()
		return res
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		res.Note = "can't write temp file: " + werr.Error()
		return res
	}
	return art(ctx, res, tmp.Name(), o, d)
}

func fitImage(res Result, img image.Image, b Box) Result {
	b0 := img.Bounds()
	pw, ph, cols, rows := Fit(b0.Dx(), b0.Dy(), b)
	if pw == 0 {
		res.Note = "no room to preview"
		return res
	}
	res.Kind, res.Image, res.Cols, res.Rows = Image, Scale(img, pw, ph), cols, rows
	return res
}

func art(ctx context.Context, res Result, path string, o Options, d Deps) Result {
	out, err := d.Chafa.Render(ctx, path, o.Cols, o.Rows)
	if err != nil {
		res.Note = err.Error()
		return res
	}
	res.Kind, res.Art = Art, out
	res.Rows = strings.Count(out, "\n") + 1
	return res
}

func tooBig(size int64, cfg image.Config) string {
	switch {
	case size > MaxFileSize:
		return fmt.Sprintf("too large to preview (%d MB)", size>>20)
	case int64(cfg.Width)*int64(cfg.Height) > MaxPixels:
		return fmt.Sprintf("too large to preview (%dx%d)", cfg.Width, cfg.Height)
	}
	return ""
}

// noPicture explains why an image shows only its card.
func noPicture(o Options, d Deps) string {
	reason := o.Reason
	if reason == "" {
		reason = "no graphics support"
	}
	if o.Chafa && !d.hasChafa() {
		return reason + "; install chafa for previews"
	}
	return reason
}
