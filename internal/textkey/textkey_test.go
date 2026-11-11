package textkey

import (
	"bytes"
	"testing"
)

func TestUnicodeAndNatural(t *testing.T) {
	if Normalize(" ＡＢＣ　Straße ") != "abc strasse" {
		t.Fatal(Normalize(" ＡＢＣ　Straße "))
	}
	if !bytes.Equal(Natural("第01卷"), Natural("第1卷")) {
		t.Fatal("leading zero normalization")
	}
	for _, p := range [][2]string{{"第2卷", "第10卷"}, {"A9", "a100000000000000000000000000000000000000"}, {"x", "x1"}} {
		if bytes.Compare(Natural(p[0]), Natural(p[1])) >= 0 {
			t.Fatal(p)
		}
	}
}
