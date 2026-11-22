package preview

import (
	"bytes"
	"image"
	"strconv"
)

// A small sixel encoder (x/ansi's would add a dependency): a fixed
// 6x6x6 colour cube with 4x4 ordered dithering, run-length encoded. Good
// enough for previews, and fast because nothing is searched.

const cubeLevels = 6

// bayer4 is the 4x4 ordered dither matrix, in sixteenths.
var bayer4 = [4][4]int{
	{0, 8, 2, 10},
	{12, 4, 14, 6},
	{3, 11, 1, 9},
	{15, 7, 13, 5},
}

// cubeIndex is the palette entry for a pixel at (x, y), or -1 for a
// transparent one.
func cubeIndex(r, g, b, a uint8, x, y int) int {
	if a < 128 {
		return -1
	}
	// Dither by up to one cube step (51 = 255/5).
	d := (bayer4[y&3][x&3]*51)/16 - 25
	q := func(c uint8) int {
		v := int(c) + d
		return min(max((v*(cubeLevels-1)+127)/255, 0), cubeLevels-1)
	}
	return q(r)*cubeLevels*cubeLevels + q(g)*cubeLevels + q(b)
}

// encodeSixel returns the sixel data for img: raster attributes, the
// palette and the pixel bands (everything between DCS ... q and ST).
func encodeSixel(img *image.RGBA) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var out bytes.Buffer
	out.WriteString(`"1;1;` + strconv.Itoa(w) + ";" + strconv.Itoa(h))
	for i := range cubeLevels * cubeLevels * cubeLevels {
		r, g, bl := i/36, i/6%6, i%6
		pct := func(v int) string { return strconv.Itoa(v * 100 / (cubeLevels - 1)) }
		out.WriteString("#" + strconv.Itoa(i) + ";2;" + pct(r) + ";" + pct(g) + ";" + pct(bl))
	}

	idx := make([]int, w*6) // palette index per pixel of the band
	row := make([]byte, w)  // one colour's sixels across the band
	for top := 0; top < h; top += 6 {
		used := map[int]bool{}
		order := []int{}
		for dy := range 6 {
			y := top + dy
			for x := range w {
				i := -1
				if y < h {
					p := img.Pix[y*img.Stride+x*4:]
					i = cubeIndex(p[0], p[1], p[2], p[3], x, y)
				}
				idx[dy*w+x] = i
				if i >= 0 && !used[i] {
					used[i] = true
					order = append(order, i)
				}
			}
		}
		for n, c := range order {
			for x := range w {
				var bits byte
				for dy := range 6 {
					if idx[dy*w+x] == c {
						bits |= 1 << dy
					}
				}
				row[x] = 63 + bits
			}
			out.WriteString("#" + strconv.Itoa(c))
			writeRLE(&out, row)
			if n < len(order)-1 {
				out.WriteByte('$') // back to the band's start for the next colour
			}
		}
		out.WriteByte('-') // next band
	}
	return out.Bytes()
}

// writeRLE writes sixel characters, runs of more than three as !n<char>.
func writeRLE(out *bytes.Buffer, row []byte) {
	for i := 0; i < len(row); {
		j := i
		for j < len(row) && row[j] == row[i] {
			j++
		}
		if n := j - i; n > 3 {
			out.WriteString("!" + strconv.Itoa(n))
			out.WriteByte(row[i])
		} else {
			for range n {
				out.WriteByte(row[i])
			}
		}
		i = j
	}
}
