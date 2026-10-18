package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// trayIcon draws a small QR-style glyph as a PNG.
func trayIcon() []byte {
	const size = 32
	bg := color.RGBA{0x1b, 0x28, 0x38, 0xff}
	fg := color.RGBA{0x66, 0xc0, 0xf4, 0xff}
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// 8x8 module grid, 4px per module: three finder squares plus a few dots.
	pattern := []string{
		"###..###",
		"#.#..#.#",
		"###..###",
		"...#.#..",
		"..#.#..#",
		"###.#.#.",
		"#.#..##.",
		"###.#..#",
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			c := bg
			if pattern[y/4][x/4] == '#' {
				c = fg
			}
			img.Set(x, y, c)
		}
	}

	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}
