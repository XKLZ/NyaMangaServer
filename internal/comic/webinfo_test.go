package comic

import (
	"context"
	"strings"
	"testing"
)

func TestWebInfoSelectionAndLimits(t *testing.T) {
	for _, test := range []struct {
		name    string
		entries []entry
		want    string
		bad     bool
	}{
		{"root first", []entry{{"2/webinfo.txt", []byte("nested"), false}, {"webinfo.txt", []byte("https://e-hentai.org/g/1/0123456789/\n"), false}}, "https://e-hentai.org/g/1/0123456789/", false},
		{"natural nested", []entry{{"10/webinfo.txt", []byte("ten"), false}, {"2/webinfo.txt", []byte("two"), false}}, "two", false},
		{"no hint", []entry{{"notes.txt", []byte("notes"), false}}, "", false},
		{"too big", []entry{{"webinfo.txt", []byte(strings.Repeat("x", 4097)), false}}, "", true},
		{"multiline", []entry{{"webinfo.txt", []byte("one\ntwo"), false}}, "", true},
		{"bad utf8", []entry{{"webinfo.txt", []byte{255}, false}}, "", true},
		{"unsafe entry", []entry{{"../webinfo.txt", []byte("x"), false}}, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, size, mtime := archive(t, test.entries)
			p := NewPool()
			defer p.Close()
			got, e := p.WebInfo(context.Background(), root, "sample.cbz", "utf8", size, mtime)
			if (e != nil) != test.bad || got != test.want {
				t.Fatal(got, e)
			}
		})
	}
}
