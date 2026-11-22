package preview

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/daan3011/forge/internal/chafa"
	"github.com/daan3011/forge/internal/rsvg"
	"github.com/daan3011/forge/internal/ui"
)

func TestFit(t *testing.T) {
	b := Box{Cols: 40, Rows: 20, CellW: 10, CellH: 20} // 400 x 400 px
	tests := []struct {
		name               string
		w, h               int
		pw, ph, cols, rows int
	}{
		{"wide shrinks to width", 800, 400, 400, 200, 40, 10},
		{"tall shrinks to height", 400, 800, 200, 400, 20, 20},
		{"small never grows", 100, 50, 100, 50, 10, 3},
		{"exact", 400, 400, 400, 400, 40, 20},
		{"one pixel", 1, 1, 1, 1, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pw, ph, cols, rows := Fit(tt.w, tt.h, b)
			if pw != tt.pw || ph != tt.ph || cols != tt.cols || rows != tt.rows {
				t.Fatalf("Fit = %d %d %d %d, want %d %d %d %d", pw, ph, cols, rows, tt.pw, tt.ph, tt.cols, tt.rows)
			}
		})
	}
	if pw, _, _, _ := Fit(10, 10, Box{}); pw != 0 {
		t.Fatal("an empty box fits nothing")
	}
}

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

func TestScale(t *testing.T) {
	// Left half black, right half white: shrunk to 2x1 it stays that way.
	src := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := range 4 {
		for x := range 8 {
			v := uint8(0)
			if x >= 4 {
				v = 255
			}
			src.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	got := Scale(src, 2, 1)
	if got.Bounds() != image.Rect(0, 0, 2, 1) {
		t.Fatalf("bounds = %v", got.Bounds())
	}
	if c := got.RGBAAt(0, 0); c.R != 0 {
		t.Errorf("left = %v", c)
	}
	if c := got.RGBAAt(1, 0); c.R != 255 {
		t.Errorf("right = %v", c)
	}
	// Averaging: 4x1 of black/white alternating becomes mid grey.
	stripes := image.NewRGBA(image.Rect(0, 0, 4, 1))
	for x := range 4 {
		v := uint8(255 * (x % 2))
		stripes.Set(x, 0, color.RGBA{v, v, v, 255})
	}
	if c := Scale(stripes, 1, 1).RGBAAt(0, 0); c.R < 120 || c.R > 135 {
		t.Errorf("average = %v", c)
	}
	// A non-RGBA source with an offset origin comes out at (0, 0).
	gray := image.NewGray(image.Rect(5, 5, 9, 9))
	if b := Scale(gray, 4, 4).Bounds(); b.Min != (image.Point{}) {
		t.Errorf("origin = %v", b.Min)
	}
}

func TestPlaceholders(t *testing.T) {
	got := Placeholders(42, 3, 2)
	ph, d := string(kitty.Placeholder), func(i int) string { return string(kitty.Diacritic(i)) }
	want := "\x1b[38;5;42m" + ph + d(0) + d(0) + ph + ph + "\x1b[39m\n" +
		"\x1b[38;5;42m" + ph + d(1) + d(0) + ph + ph + "\x1b[39m"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	for _, l := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(l); w != 3 {
			t.Fatalf("row width = %d, want 3", w)
		}
	}
}

// TestPlaceholdersSurviveLayout is the P4.7 spike kept as a test: the
// grid goes through ui.Box and the lipgloss compositor (the overlay path)
// with its diacritics, colour and width intact.
func TestPlaceholdersSurviveLayout(t *testing.T) {
	grid := Placeholders(7, 4, 2)
	boxed := ui.Box(grid, 6, 3)
	canvas := lipgloss.NewCanvas(10, 3)
	canvas.Compose(lipgloss.NewCompositor(lipgloss.NewLayer(boxed), lipgloss.NewLayer("X").X(8).Y(0).Z(1)))
	out := canvas.Render()
	lines := strings.Split(out, "\n")
	for r := range 2 {
		head := string(kitty.Placeholder) + string(kitty.Diacritic(r)) + string(kitty.Diacritic(0))
		if !strings.Contains(lines[r], head) {
			t.Fatalf("row %d lost its diacritics: %q", r, lines[r])
		}
		if !regexp.MustCompile(`\x1b\[38;5;0*7m`).MatchString(lines[r]) {
			t.Fatalf("row %d lost the image id colour: %q", r, lines[r])
		}
	}
	if !strings.HasSuffix(ansi.Strip(lines[0]), "X") || ansi.StringWidth(lines[0]) != 9 {
		t.Fatalf("overlay misplaced, width %d: %q", ansi.StringWidth(lines[0]), lines[0])
	}
}

func TestKittyTransmit(t *testing.T) {
	img := solid(30, 10, color.RGBA{255, 0, 0, 255})
	got, err := KittyTransmit(img, 9, 4, 2, 10, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "\x1b_G") || !strings.HasSuffix(got, "\x1b\\") {
		t.Fatalf("not an APC sequence: %q", got[:min(len(got), 40)])
	}
	first, _, ok := strings.Cut(got, ";")
	if !ok {
		t.Fatalf("no payload: %q", got)
	}
	for _, opt := range []string{"f=100", "q=2", "i=9", "U=1", "c=4", "r=2", "a=T"} {
		if !strings.Contains(first, opt) {
			t.Errorf("first chunk options %q lack %s", first, opt)
		}
	}

	// A big image is chunked, and wrap is applied to every chunk.
	noisy := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for i := range noisy.Pix {
		noisy.Pix[i] = uint8(i * 7919 % 251)
	}
	wrapped, err := KittyTransmit(noisy, 1, 20, 10, 10, 20, func(s string) string { return "<" + s + ">" })
	if err != nil {
		t.Fatal(err)
	}
	chunks := strings.Count(wrapped, "<\x1b_G")
	if chunks < 2 || chunks != strings.Count(wrapped, "\x1b\\>") {
		t.Fatalf("chunks = %d", chunks)
	}
	if !strings.Contains(wrapped, "m=1") || !strings.Contains(wrapped, "m=0") {
		t.Fatal("missing chunk markers")
	}
}

func TestKittyDelete(t *testing.T) {
	if got, want := KittyDelete(12), "\x1b_Ga=d,d=I,i=12,q=2\x1b\\"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPad(t *testing.T) {
	img := solid(3, 2, color.RGBA{1, 2, 3, 255})
	p := pad(img, 5, 4)
	if p.Bounds().Dx() != 5 || p.Bounds().Dy() != 4 || p.RGBAAt(0, 0).R != 1 || p.RGBAAt(4, 3).A != 0 {
		t.Fatalf("pad = %v %v %v", p.Bounds(), p.RGBAAt(0, 0), p.RGBAAt(4, 3))
	}
	if pad(img, 3, 2) != img {
		t.Fatal("same size should not copy")
	}
}

func TestSixel(t *testing.T) {
	img := solid(10, 7, color.RGBA{255, 255, 255, 255})
	img.SetRGBA(0, 0, color.RGBA{}) // a transparent pixel
	got := Sixel(img)
	if !strings.HasPrefix(got, "\x1bP0;1q\"1;1;10;7") || !strings.HasSuffix(got, "\x1b\\") {
		t.Fatalf("header/trailer: %q", got[:min(len(got), 30)])
	}
	// White is the cube's last entry; 7 rows make two bands.
	if !strings.Contains(got, "#215;2;100;100;100") || strings.Count(got, "-") < 2 {
		t.Fatalf("palette or bands missing: %q", got[len(got)-80:])
	}
	// Run-length encoding: the second band (row 6 only, bit 0 = '@') is
	// one run of 10.
	if !strings.Contains(got, "#215!10@") {
		t.Fatalf("no RLE run in %q", got[strings.LastIndex(got, "#215;"):])
	}
}

func TestCubeIndex(t *testing.T) {
	if cubeIndex(0, 0, 0, 0, 0, 0) != -1 {
		t.Fatal("transparent should have no colour")
	}
	if got := cubeIndex(0, 0, 0, 255, 0, 0); got != 0 {
		t.Fatalf("black = %d", got)
	}
	if got := cubeIndex(255, 255, 255, 255, 3, 3); got != 215 {
		t.Fatalf("white = %d", got)
	}
	if got := cubeIndex(255, 0, 0, 255, 1, 2); got != 5*36 {
		t.Fatalf("red = %d", got)
	}
}

func TestPlace(t *testing.T) {
	if got, want := Place("S", 4, 9), "\x1b7\x1b[10;5HS\x1b8"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractHTML(t *testing.T) {
	tests := []struct {
		name  string
		doc   string
		title string
		lines []string
	}{
		{
			name:  "basic page",
			doc:   `<!doctype html><html><head><title>Checkout &amp; pay</title><style>body{color:red}</style></head><body><h1>Pay now</h1><p>Total:   <b>€12</b></p><script>alert(1)</script></body></html>`,
			title: "Checkout & pay",
			lines: []string{"Pay now", "Total: €12"},
		},
		{
			name:  "no title, comments and noscript dropped",
			doc:   "<div>one</div><!-- hidden --><noscript>enable js</noscript><div>two&nbsp;three</div>",
			lines: []string{"one", "two three"},
		},
		{
			name:  "uppercase tags, br, list",
			doc:   "<BODY>a<BR>b<UL><LI>c</LI><LI>d</LI></UL></BODY>",
			lines: []string{"a", "b", "c", "d"},
		},
		{
			name:  "inline svg dropped, unterminated script",
			doc:   `<p>logo</p><svg><text>SVG TEXT</text></svg><p>after</p><script>never ends`,
			lines: []string{"logo", "after"},
		},
		{
			name:  "title with tags and newlines",
			doc:   "<title>\n  My <i>Mock</i>\n</title><p>x</p>",
			title: "My Mock",
			lines: []string{"x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, lines, err := ExtractHTML(strings.NewReader(tt.doc), HTMLReadLimit)
			if err != nil {
				t.Fatal(err)
			}
			if title != tt.title || !slices.Equal(lines, tt.lines) {
				t.Fatalf("got %q %q, want %q %q", title, lines, tt.title, tt.lines)
			}
		})
	}
}

func TestExtractHTMLLimit(t *testing.T) {
	doc := "<p>first</p>" + strings.Repeat("x", 100) + "<p>late</p>"
	_, lines, err := ExtractHTML(strings.NewReader(doc), 20)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(lines, "late") {
		t.Fatal("read past the limit")
	}
}

func writePNG(t *testing.T, dir, name string, img image.Image) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

var box = Box{Cols: 20, Rows: 10, CellW: 10, CellH: 20}

func TestLoadPNGGraphics(t *testing.T) {
	p := writePNG(t, t.TempDir(), "a.png", solid(400, 100, color.RGBA{0, 0, 255, 255}))
	res := Load(context.Background(), p, "png", Options{Box: box, Graphics: true}, Deps{})
	if res.Kind != Image || res.Width != 400 || res.Height != 100 {
		t.Fatalf("res = %+v", res)
	}
	if b := res.Image.Bounds(); b.Dx() != 200 || b.Dy() != 50 || res.Cols != 20 || res.Rows != 3 {
		t.Fatalf("scaled %v into %dx%d cells", b, res.Cols, res.Rows)
	}
}

func TestLoadPNGChafa(t *testing.T) {
	p := writePNG(t, t.TempDir(), "a.png", solid(40, 20, color.RGBA{0, 255, 0, 255}))
	f := &chafa.Fake{Func: func(string, int, int) (string, error) { return "##\n##", nil }}
	res := Load(context.Background(), p, "png", Options{Box: box, Chafa: true}, Deps{Chafa: f})
	if res.Kind != Art || res.Art != "##\n##" || res.Rows != 2 || res.Width != 40 {
		t.Fatalf("res = %+v", res)
	}
	if !slices.Equal(f.Calls(), []string{p}) {
		t.Fatalf("chafa calls = %v", f.Calls())
	}
}

func TestLoadCardReasons(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "a.png", solid(4, 4, color.RGBA{A: 255}))
	tests := []struct {
		name string
		o    Options
		d    Deps
		want string
	}{
		{
			"no chafa installed",
			Options{Box: box, Chafa: true, Reason: "no graphics support in Apple_Terminal"},
			Deps{Chafa: &chafa.Fake{Missing: true}},
			"no graphics support in Apple_Terminal; install chafa for previews",
		},
		{"text mode", Options{Box: box, Reason: "artifacts.preview is text"}, Deps{Chafa: &chafa.Fake{}}, "artifacts.preview is text"},
		{"chafa fails", Options{Box: box, Chafa: true}, Deps{Chafa: &chafa.Fake{Func: func(string, int, int) (string, error) { return "", errors.New("chafa: boom") }}}, "chafa: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Load(context.Background(), p, "png", tt.o, tt.d)
			if res.Kind != Card || res.Note != tt.want || res.Width != 4 {
				t.Fatalf("res = %+v", res)
			}
		})
	}
}

func TestLoadBadFiles(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.png")
	if err := os.WriteFile(bad, []byte("not a png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := Load(context.Background(), bad, "png", Options{Box: box, Graphics: true}, Deps{}); res.Kind != Card || !strings.HasPrefix(res.Note, "can't decode") {
		t.Fatalf("bad = %+v", res)
	}
	if res := Load(context.Background(), filepath.Join(dir, "gone.png"), "png", Options{Box: box, Graphics: true}, Deps{}); !strings.HasPrefix(res.Note, "can't read") {
		t.Fatalf("missing = %+v", res)
	}
	if res := Load(context.Background(), bad, "webp", Options{}, Deps{}); res.Note != "no preview for webp" {
		t.Fatalf("webp = %+v", res)
	}
	if res := Load(context.Background(), bad, "md", Options{}, Deps{}); res.Kind != Card || res.Note != "" {
		t.Fatalf("md = %+v", res)
	}
}

func TestTooBig(t *testing.T) {
	if got := tooBig(MaxFileSize+1, image.Config{Width: 1, Height: 1}); !strings.Contains(got, "MB") {
		t.Fatalf("size: %q", got)
	}
	if got := tooBig(10, image.Config{Width: 10000, Height: 10000}); got != "too large to preview (10000x10000)" {
		t.Fatalf("pixels: %q", got)
	}
	if tooBig(10, image.Config{Width: 100, Height: 100}) != "" {
		t.Fatal("small image is not too big")
	}
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestLoadSVG(t *testing.T) {
	svg := filepath.Join(t.TempDir(), "a.svg")
	if err := os.WriteFile(svg, []byte("<svg/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	var asked [2]int
	r := &rsvg.Fake{Func: func(_ string, w, h int) ([]byte, error) {
		asked = [2]int{w, h}
		return pngBytes(t, solid(100, 50, color.RGBA{255, 0, 0, 255})), nil
	}}
	res := Load(context.Background(), svg, "svg", Options{Box: box, Graphics: true}, Deps{Rsvg: r})
	if res.Kind != Image || res.Width != 100 || asked != [2]int{200, 200} {
		t.Fatalf("graphics: res=%+v asked=%v", res, asked)
	}

	// Through chafa: rasterised at 8x16 per cell into a temp file that is
	// removed afterwards.
	tmp := t.TempDir()
	var seen string
	f := &chafa.Fake{Func: func(path string, _, _ int) (string, error) {
		seen = path
		if _, err := os.Stat(path); err != nil {
			t.Errorf("temp file missing while chafa runs: %v", err)
		}
		return "art", nil
	}}
	res = Load(context.Background(), svg, "svg", Options{Box: box, Chafa: true}, Deps{Rsvg: r, Chafa: f, TempDir: tmp})
	if res.Kind != Art || asked != [2]int{160, 160} || filepath.Dir(seen) != tmp {
		t.Fatalf("chafa: res=%+v asked=%v seen=%q", res, asked, seen)
	}
	if _, err := os.Stat(seen); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}

	// No rasterizer.
	res = Load(context.Background(), svg, "svg", Options{Box: box, Graphics: true}, Deps{Rsvg: &rsvg.Fake{Missing: true}})
	if res.Kind != Card || !strings.Contains(res.Note, "rsvg-convert") {
		t.Fatalf("no rsvg: %+v", res)
	}
	// Neither graphics nor chafa: the terminal reason wins.
	res = Load(context.Background(), svg, "svg", Options{Box: box, Reason: "nope"}, Deps{Rsvg: r})
	if res.Note != "nope" {
		t.Fatalf("no output: %+v", res)
	}
}

func TestLoadHTML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.html")
	if err := os.WriteFile(p, []byte("<title>Mock</title><h1>Hello</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := Load(context.Background(), p, "html", Options{}, Deps{})
	if res.Kind != HTML || res.Title != "Mock" || !slices.Equal(res.Lines, []string{"Hello"}) {
		t.Fatalf("res = %+v", res)
	}
}
