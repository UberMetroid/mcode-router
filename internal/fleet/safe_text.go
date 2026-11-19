package fleet

import (
	"strings"
	"unicode/utf8"
)

// skipEscape returns the index just past the escape sequence starting at i.
var skipEscape = func(s string, i int) int {
	i++
	if i < len(s) && s[i] == '[' {
		i++
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
			i++
		}
	}
	if i < len(s) {
		i++
	}
	return i
}

// SafeText strips escape sequences and control characters from text that came
// out of mutable state, so a forged value cannot repaint the terminal or forge
// extra lines of output.
var SafeText = func(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b:
			i = skipEscape(s, i)
		case c < 0x20 || c == 0x7f:
			i++
		case c >= 0x80:
			// Drop the C1 control range, including the UTF-8 encoded CSI and
			// any raw byte a terminal would read as an 8-bit control.
			r, size := utf8.DecodeRuneInString(s[i:])
			if (r == utf8.RuneError && size <= 1) || (r >= 0x80 && r <= 0x9f) {
				i += size
				continue
			}
			b.WriteString(s[i : i+size])
			i += size
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}
