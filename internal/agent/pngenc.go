package agent

import (
	"image"
	"image/png"
	"io"
)

// pngEncode — кодирует image в PNG (единая точка для стич-склеек).
func pngEncode(w io.Writer, img image.Image) error {
	return png.Encode(w, img)
}
