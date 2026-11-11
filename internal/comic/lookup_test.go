package comic

import (
	"context"
	"crypto/sha1"
	"fmt"
	"testing"
)

func TestContentPageLookupPolicy(t *testing.T) {
	for _, n := range []int{7, 8, 9} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			entries := []entry{}
			for i := 1; i <= n; i++ {
				raw := append(picture(t, "png"), byte(i))
				entries = append(entries, entry{fmt.Sprintf("%02d.png", i), raw, false})
			}
			root, size, mtime := archive(t, entries)
			p := NewPool()
			defer p.Close()
			hash, count, e := p.LookupSHA1(context.Background(), root, "sample.cbz", "utf8", size, mtime)
			selected := 0
			if n >= 8 {
				selected = 7
			}
			if e != nil || count != n || hash != fmt.Sprintf("%x", sha1.Sum(entries[selected].data)) {
				t.Fatal(hash, count, e)
			}
		})
	}
	entries := []entry{}
	for i := 1; i <= 8; i++ {
		data := picture(t, "png")
		if i == 8 {
			data = []byte("damaged")
		}
		entries = append(entries, entry{fmt.Sprintf("%02d.png", i), data, false})
	}
	root, size, mtime := archive(t, entries)
	p := NewPool()
	defer p.Close()
	if hash, _, e := p.LookupSHA1(context.Background(), root, "sample.cbz", "utf8", size, mtime); e != nil || hash != fmt.Sprintf("%x", sha1.Sum([]byte("damaged"))) {
		t.Fatal("raw representative bytes must hash without decoding", hash, e)
	}
}
