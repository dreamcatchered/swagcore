package agent

import (
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// basicDrawString рисует строку ASCII-шрифтом basicfont.Face7x13.
// Кириллица транслитерируется translit-ом (basicfont её не содержит).
func basicDrawString(dst *image.RGBA, x, y int, s string, c color.RGBA) {
	s = translit(s)
	f := basicfont.Face7x13
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(c),
		Face: f,
	}
	for _, r := range s {
		d.Dot = fixed.P(x, y)
		d.DrawString(string(r))
		x += font.MeasureString(f, string(r)).Round()
	}
}

var _ = draw.Draw // keep draw import alive

// translit — минимальная транслитерация кириллицы для headless-карточки.
func translit(s string) string {
	m := map[rune]string{
		'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
		'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
		'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
		'ф': "f", 'х': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sch",
		'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
		'А': "A", 'Б': "B", 'В': "V", 'Г': "G", 'Д': "D", 'Е': "E", 'Ё': "E",
		'Ж': "Zh", 'З': "Z", 'И': "I", 'Й': "Y", 'К': "K", 'Л': "L", 'М': "M",
		'Н': "N", 'О': "O", 'П': "P", 'Р': "R", 'С': "S", 'Т': "T", 'У': "U",
		'Ф': "F", 'Х': "H", 'Ц': "Ts", 'Ч': "Ch", 'Ш': "Sh", 'Щ': "Sch",
		'Ъ': "", 'Ы': "Y", 'Ь': "", 'Э': "E", 'Ю': "Yu", 'Я': "Ya",
		'—': "-", '–': "-", '«': "\"", '»': "\"",
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if rep, ok := m[r]; ok {
			out = append(out, []rune(rep)...)
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
