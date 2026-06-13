// Command genicon renders the application icon (a green oscilloscope sine on a
// dark rounded square) and writes build/appicon.png plus a multi-size
// build/windows/icon.ico. Run from the repo root: go run ./tools/genicon
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func main() {
	// Render at 4x for smooth anti-aliasing, then downscale.
	const out = 1024
	master := downscale(drawIcon(out*4), out)

	if err := savePNG("build/appicon.png", master); err != nil {
		panic(err)
	}

	sizes := []int{256, 128, 64, 48, 32, 16}
	blobs := make([][]byte, len(sizes))
	for i, s := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, downscale(master, s)); err != nil {
			panic(err)
		}
		blobs[i] = buf.Bytes()
	}
	if err := writeICO("build/windows/icon.ico", sizes, blobs); err != nil {
		panic(err)
	}
}

func drawIcon(d int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, d, d))
	fd := float64(d)

	radius := 0.20 * fd
	cy := fd / 2
	amp := 0.23 * fd
	k := 2 * math.Pi * 2.4 / fd // 2.4 cycles across the icon
	coreHalf := 0.028 * fd
	glowHalf := 0.075 * fd

	top := color.RGBA{17, 32, 43, 255}
	bot := color.RGBA{7, 10, 14, 255}
	core := [3]float64{93, 240, 168}
	glow := [3]float64{57, 217, 138}

	for y := 0; y < d; y++ {
		py := float64(y) + 0.5
		// vertical gradient for the background
		t := py / fd
		bg := color.RGBA{
			R: lerp(top.R, bot.R, t),
			G: lerp(top.G, bot.G, t),
			B: lerp(top.B, bot.B, t),
			A: 255,
		}
		for x := 0; x < d; x++ {
			px := float64(x) + 0.5
			if !insideRounded(px, py, fd, radius) {
				continue
			}
			r, g, b := float64(bg.R), float64(bg.G), float64(bg.B)

			// faint center axis line, oscilloscope style
			if math.Abs(py-cy) < 0.006*fd {
				r = blend(r, 200, 0.10)
				g = blend(g, 230, 0.10)
				b = blend(b, 210, 0.10)
			}

			// sine wave with soft glow
			yc := cy - amp*math.Sin(k*px)
			slope := -amp * k * math.Cos(k*px)
			dist := math.Abs(py-yc) / math.Sqrt(1+slope*slope)
			if dist <= coreHalf {
				r, g, b = core[0], core[1], core[2]
			} else if dist <= glowHalf {
				a := 0.6 * (1 - (dist-coreHalf)/(glowHalf-coreHalf))
				r = blend(r, glow[0], a)
				g = blend(g, glow[1], a)
				b = blend(b, glow[2], a)
			}
			img.SetRGBA(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), 255})
		}
	}
	return img
}

// insideRounded reports whether (x,y) lies within a rounded square of side s
// with the given corner radius.
func insideRounded(x, y, s, r float64) bool {
	dx := math.Max(math.Max(r-x, x-(s-r)), 0)
	dy := math.Max(math.Max(r-y, y-(s-r)), 0)
	return dx*dx+dy*dy <= r*r
}

func lerp(a, b uint8, t float64) uint8 {
	return uint8(float64(a) + (float64(b)-float64(a))*t)
}

func blend(dst, src, a float64) float64 { return src*a + dst*(1-a) }

// downscale area-averages a square RGBA image to dst x dst pixels.
func downscale(src *image.RGBA, dst int) *image.RGBA {
	srcN := src.Bounds().Dx()
	out := image.NewRGBA(image.Rect(0, 0, dst, dst))
	scale := float64(srcN) / float64(dst)
	for y := 0; y < dst; y++ {
		y0, y1 := int(float64(y)*scale), int(float64(y+1)*scale)
		for x := 0; x < dst; x++ {
			x0, x1 := int(float64(x)*scale), int(float64(x+1)*scale)
			var r, g, b, a, n float64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					c := src.RGBAAt(sx, sy)
					r += float64(c.R)
					g += float64(c.G)
					b += float64(c.B)
					a += float64(c.A)
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			out.SetRGBA(x, y, color.RGBA{uint8(r / n), uint8(g / n), uint8(b / n), uint8(a / n)})
		}
	}
	return out
}

func savePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// writeICO writes a PNG-encoded ICO containing the given sizes.
func writeICO(path string, sizes []int, blobs [][]byte) error {
	var buf bytes.Buffer
	// ICONDIR
	binary.Write(&buf, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // type: icon
	binary.Write(&buf, binary.LittleEndian, uint16(len(sizes)))

	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := byte(s) // 256 wraps to 0, which ICO interprets as 256
		buf.WriteByte(dim)
		buf.WriteByte(dim)
		buf.WriteByte(0) // palette
		buf.WriteByte(0) // reserved
		binary.Write(&buf, binary.LittleEndian, uint16(1))  // planes
		binary.Write(&buf, binary.LittleEndian, uint16(32)) // bpp
		binary.Write(&buf, binary.LittleEndian, uint32(len(blobs[i])))
		binary.Write(&buf, binary.LittleEndian, uint32(offset))
		offset += len(blobs[i])
	}
	for _, b := range blobs {
		buf.Write(b)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
