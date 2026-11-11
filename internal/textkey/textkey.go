// Package textkey defines the version-one portable search and natural-order keys.
package textkey

import (
	"encoding/binary"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"strings"
	"unicode/utf8"
)

func Normalize(s string) string {
	return strings.Join(strings.Fields(cases.Fold().String(norm.NFKC.String(s))), " ")
}
func Natural(s string) []byte {
	s = Normalize(s)
	out := []byte{}
	for len(s) > 0 {
		if s[0] >= '0' && s[0] <= '9' {
			n := 0
			for n < len(s) && s[n] >= '0' && s[n] <= '9' {
				n++
			}
			digits := strings.TrimLeft(s[:n], "0")
			if digits == "" {
				digits = "0"
			}
			out = append(out, 1)
			out = binary.BigEndian.AppendUint32(out, uint32(len(digits)))
			out = append(out, digits...)
			out = append(out, 0)
			s = s[n:]
		} else {
			_, n := utf8.DecodeRuneInString(s)
			out = append(out, 2)
			out = append(out, s[:n]...)
			out = append(out, 0)
			s = s[n:]
		}
	}
	return out
}
