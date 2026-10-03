package agent

import (
	"bytes"
	"image"
	"image/draw"
	_ "image/png"
	"strings"
)

// splitLines — деление строки на строки (без strings, чтобы не плодить импорты вShot-файлах).
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}

func trimSpace(s string) string {
	return strings.TrimSpace(s)
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// stitchHorizontal склеивает несколько PNG-изображений в одно по горизонтали
// (имитация виртуального рабочего стола из нескольких мониторов).
func stitchHorizontal(pngs [][]byte) ([]byte, error) {
	var imgs []image.Image
	totalW, maxH := 0, 0
	for _, p := range pngs {
		img, _, err := image.Decode(bytes.NewReader(p))
		if err != nil {
			return nil, err
		}
		b := img.Bounds()
		totalW += b.Dx()
		if b.Dy() > maxH {
			maxH = b.Dy()
		}
		imgs = append(imgs, img)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, totalW, maxH))
	x := 0
	for _, img := range imgs {
		b := img.Bounds()
		draw.Draw(canvas, image.Rect(x, 0, x+b.Dx(), b.Dy()), img, b.Min, draw.Src)
		x += b.Dx()
	}
	var out bytes.Buffer
	if err := pngEncode(&out, canvas); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
