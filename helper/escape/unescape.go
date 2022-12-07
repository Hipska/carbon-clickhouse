package escape

import "strings"

func ishex(c byte) bool {
	switch {
	case '0' <= c && c <= '9':
		return true
	case 'a' <= c && c <= 'f':
		return true
	case 'A' <= c && c <= 'F':
		return true
	}
	return false
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

func isPercentEscape(s string, i int) bool {
	return i+2 < len(s) && ishex(s[i+1]) && ishex(s[i+2])
}

// unescape unescapes a string; the mode specifies
// which section of the URL string is being unescaped.
func Unescape(s string) string {
	var sb strings.Builder
	return UnescapeTo(s, true, &sb)
}

// unescape unescapes a string; the mode specifies
// which section of the URL string is being unescaped.
func UnescapeTo(s string, noCopy bool, sb *strings.Builder) string {
	var (
		first int
		pos   int
	)
	if noCopy {
		first = strings.IndexAny(s, "%+")
		if first == -1 {
			return s
		}
		pos = sb.Len()
		sb.Grow(pos + len(s))
		sb.WriteString(s[:first])
	} else {
		pos = sb.Len()
	}

LOOP:
	for i := first; i < len(s); i++ {
		switch s[i] {
		case '%':
			if len(s) < i+3 {
				sb.WriteString(s[i:])
				break LOOP
			}
			if !isPercentEscape(s, i) {
				sb.WriteString(s[i : i+3])
			} else {
				sb.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			}
			i += 2
		case '+':
			sb.WriteByte(' ')
		default:
			sb.WriteByte(s[i])
		}
	}

	return sb.String()[pos:]
}
