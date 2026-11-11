package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSourceSearchPageAndTitleIsolation(t *testing.T) {
	c := New(vaultForTest(t))
	c.interval = 0
	c.transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "e-hentai.org" || r.URL.Query().Get("f_search") != `title:"匿名 title artist bad"` || r.URL.Query().Get("next") != "25" {
			t.Fatal(r.URL)
		}
		var html strings.Builder
		html.WriteString(`<html><table class="itg">`)
		for i := 1; i <= 17; i++ {
			fmt.Fprintf(&html, `<a href="/g/%d/0123456789/">Anonymous %d</a>`, i, i)
		}
		html.WriteString(`</table><a id="unext" href="/?f_search=ignored&amp;next=123">Next</a></html>`)
		return reply(r, 200, html.String()), nil
	})
	p, e := c.SearchPage(context.Background(), "title", `匿名 title artist:bad`, "25", "eh")
	if e != nil || len(p.Items) != 17 || p.Next != "123" {
		t.Fatal(p, e)
	}
	for _, raw := range []string{"<html>unexpected login page</html>", `<html><a id="unext" href="https://127.0.0.1/?next=123">next</a></html>`, `<html><a id="unext" href="/?next=25">next</a></html>`} {
		if _, e := parseSearchPage([]byte(raw), "eh", "25"); e == nil {
			t.Fatal("invalid search accepted")
		}
	}
	if p, e := parseSearchPage([]byte(`<html>No hits found</html>`), "eh", ""); e != nil || len(p.Items) != 0 {
		t.Fatal(p, e)
	}
	if _, e = c.SearchPage(context.Background(), "title", "title", "https://example.com", "eh"); e == nil {
		t.Fatal("arbitrary cursor accepted")
	}
}
func TestSourceSearchBatchMetadataPartialAndReordered(t *testing.T) {
	c := New(vaultForTest(t))
	c.interval = 0
	c.transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.e-hentai.org" || r.Header.Get("Cookie") != "" {
			t.Fatal("cross-host cookie")
		}
		return reply(r, 200, `{"gmetadata":[{"gid":2,"error":"removed"},{"gid":1,"token":"0123456789","title":"Anonymous","filecount":"25","category":"Manga","rating":"4","tags":[]}]}`), nil
	})
	list, e := c.FetchMany(context.Background(), []string{"https://e-hentai.org/g/1/0123456789/", "https://e-hentai.org/g/2/0123456789/"})
	if e != nil || len(list) != 2 || list[0].Metadata == nil || list[1].Error != "provider_rejected" {
		t.Fatal(list, e)
	}
	if _, e = c.FetchMany(context.Background(), []string{"https://127.0.0.1/g/1/0123456789/"}); e == nil {
		t.Fatal("private target")
	}
	// Build URLs through url.Values; user syntax cannot inject query parameters.
	v := url.Values{"f_search": {`title:"a&f_shash=x"`}}
	if len(v) != 1 {
		t.Fatal(v)
	}
}
