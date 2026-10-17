package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
)

// trayIcon draws a small QR-style glyph and wraps the PNG in an ICO container,
// which is what the Windows tray expects.
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

	var pngBuf bytes.Buffer
	png.Encode(&pngBuf, img)

	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, struct {
		Reserved, Type, Count uint16
		Width, Height         uint8
		Colors, Reserved2     uint8
		Planes, BitCount      uint16
		Size, Offset          uint32
	}{0, 1, 1, size, size, 0, 0, 1, 32, uint32(pngBuf.Len()), 22})
	ico.Write(pngBuf.Bytes())
	return ico.Bytes()
}
