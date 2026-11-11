package provider

import (
	"context"
	"fmt"
	"net/http"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"testing"
)

func TestDictionaryFormatReleaseAndBounds(t *testing.T) {
	data := []byte(`{"data":[{"namespace":"rows","data":{}},{"namespace":"artist","data":{"anonymous":{"name":"匿名作者"}}}]}`)
	d, e := ParseDictionary(data)
	if e != nil || d[model.TagName{Namespace: "artist", Name: "anonymous"}] != "匿名作者" {
		t.Fatal(d, e)
	}
	for _, bad := range [][]byte{[]byte(`{}`), []byte(`{"data":[]}`), []byte(`{"data":[{"namespace":"../x","data":{"a":{"name":"x"}}}]}`), append([]byte(`{"data":`), 255)} {
		if _, e = ParseDictionary(bad); e == nil {
			t.Fatal("invalid dictionary accepted")
		}
	}
	c := New(vaultForTest(t))
	c.interval = 0
	digest := strings.Repeat("a", 64)
	c.transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Cookie") != "" {
			t.Fatal("cookie sent to upstream")
		}
		if r.URL.Host == "api.github.com" {
			return reply(r, 200, fmt.Sprintf(`{"tag_name":"test-v1","assets":[{"name":"db.text.json","size":100,"digest":"sha256:%s","browser_download_url":"https://github.com/EhTagTranslation/Database/releases/download/test-v1/db.text.json"}]}`, digest)), nil
		}
		return reply(r, 200, string(data)), nil
	})
	release, e := c.LatestDictionary(context.Background())
	if e != nil || release.Digest != digest || release.Version != "test-v1" {
		t.Fatal(release, e)
	}
	if _, e = c.DownloadDictionary(context.Background(), release); e != nil {
		t.Fatal(e)
	}
	release.URL = "https://github.com/other/db.text.json"
	if _, e = c.DownloadDictionary(context.Background(), release); e == nil {
		t.Fatal("untrusted asset")
	}
}

func TestDictionaryPreservesUntranslatedAndRejectsIdentityCollisions(t *testing.T) {
	data := []byte(`{"data":[{"namespace":"artist","data":{"blank":{"name":""},"known":{"name":"已知"}}}]}`)
	d, e := ParseDictionary(data)
	if e != nil {
		t.Fatal(e)
	}
	if text, ok := d[model.TagName{Namespace: "artist", Name: "blank"}]; !ok || text != "" {
		t.Fatal(d)
	}
	for _, data := range []string{
		`{"data":[{"namespace":"artist","data":{"Known":{"name":"甲"},"known":{"name":"乙"}}}]}`,
		`{"data":[{"namespace":"artist","data":{"known":{"name":"甲"},"known":{"name":"乙"}}}]}`,
	} {
		if _, e = ParseDictionary([]byte(data)); e == nil {
			t.Fatal("conflicting identity accepted")
		}
	}
}
