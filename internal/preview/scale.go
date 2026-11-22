package preview

import (
	"image"
	"image/draw"
	"math"
)

// Box is the space a preview may fill, in cells, and one cell's size in
// pixels.
type Box struct {
	Cols, Rows   int
	CellW, CellH int
}

// px is the box's size in pixels.
func (b Box) px() (int, int) { return b.Cols * b.CellW, b.Rows * b.CellH }

func (b Box) valid() bool { return b.Cols > 0 && b.Rows > 0 && b.CellW > 0 && b.CellH > 0 }

// Fit scales a w x h image into b, keeping its aspect ratio and never
// enlarging it. It returns the pixel size to scale to and the cells that
// size covers.
func Fit(w, h int, b Box) (pw, ph, cols, rows int) {
	if w <= 0 || h <= 0 || !b.valid() {
		return 0, 0, 0, 0
	}
	maxW, maxH := b.px()
	s := math.Min(1, math.Min(float64(maxW)/float64(w), float64(maxH)/float64(h)))
	pw = min(max(int(math.Round(float64(w)*s)), 1), maxW)
	ph = min(max(int(math.Round(float64(h)*s)), 1), maxH)
	cols = min((pw+b.CellW-1)/b.CellW, b.Cols)
	rows = min((ph+b.CellH-1)/b.CellH, b.Rows)
	return pw, ph, cols, rows
}

// Scale resizes src to w x h with an area-average (box) filter, which
// keeps detail when shrinking a lot; enlarging repeats pixels. The result
// starts at (0, 0).
func Scale(src image.Image, w, h int) *image.RGBA {
	sb := src.Bounds()
	rgba, ok := src.(*image.RGBA)
	if !ok || sb.Min != (image.Point{}) {
		rgba = image.NewRGBA(image.Rect(0, 0, sb.Dx(), sb.Dy()))
		draw.Draw(rgba, rgba.Bounds(), src, sb.Min, draw.Src)
	}
	sw, sh := rgba.Bounds().Dx(), rgba.Bounds().Dy()
	if sw == w && sh == h {
		return rgba
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		y0 := y * sh / h
		y1 := max((y+1)*sh/h, y0+1)
		for x := range w {
			x0 := x * sw / w
			x1 := max((x+1)*sw/w, x0+1)
			var r, g, b, a, n uint32
			for sy := y0; sy < y1; sy++ {
				row := rgba.Pix[sy*rgba.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+4]
					r += uint32(p[0])
					g += uint32(p[1])
					b += uint32(p[2])
					a += uint32(p[3])
					n++
				}
			}
			o := dst.Pix[y*dst.Stride+x*4:]
			o[0], o[1], o[2], o[3] = uint8(r/n), uint8(g/n), uint8(b/n), uint8(a/n)
		}
	}
	return dst
}

// pad places img at the top left of a transparent w x h canvas, so a
// terminal stretching it to whole cells keeps its aspect ratio.
func pad(img *image.RGBA, w, h int) *image.RGBA {
	b := img.Bounds()
	if b.Dx() == w && b.Dy() == h {
		return img
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(out, b, img, b.Min, draw.Src)
	return out
}
