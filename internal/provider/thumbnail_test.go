package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"testing"
)

func TestThumbnailTrustDNSCookieRedirectAndBounds(t *testing.T) {
	for _, raw := range []string{"https://evil.example/a.jpg", "http://ehgt.org/a.jpg", "https://user@ehgt.org/a.jpg", "https://ehgt.org:8080/a.jpg", "https://ehgt.org/a.jpg?cookie=x", "https://ehgt.org/a.jpg#x", "https://s.exhentai.org/g/1/a", "https://127.0.0.1/a.jpg"} {
		if ThumbnailURL(raw) {
			t.Fatal(raw)
		}
	}
	if !ThumbnailURL("https://ehgt.org/a.jpg") || !ThumbnailURL("https://s.exhentai.org/t/a.jpg") {
		t.Fatal("official thumb rejected")
	}
	v := vaultForTest(t)
	if _, e := v.Credentials(CredentialPatch{Expected: 1, Fields: map[string]SecretChange{"ipb_pass_hash": {Op: "set", Value: str("anonymous-cookie")}}}); e != nil {
		t.Fatal(e)
	}
	c := New(v)
	calls := 0
	c.resolve = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	c.transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Fatal("site credentials sent to CDN")
		}
		if r.URL.Hostname() == "ehgt.org" {
			reply := reply(r, 302, "")
			reply.Header.Set("Location", "https://s.exhentai.org/t/a.jpg")
			return reply, nil
		}
		return reply(r, 200, "anonymous image"), nil
	})
	data, e := c.FetchThumbnail(context.Background(), "https://ehgt.org/a.jpg")
	if e != nil || string(data) != "anonymous image" || calls != 2 {
		t.Fatal(e, calls)
	}
	c.resolve = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	if _, e = c.FetchThumbnail(context.Background(), "https://ehgt.org/a.jpg"); e == nil || calls != 2 {
		t.Fatal("private DNS reached transport", e, calls)
	}
	c.resolve = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	c.transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		response := reply(r, 302, "")
		response.Header.Set("Location", "https://evil.example/x")
		return response, nil
	})
	before := calls
	if _, e = c.FetchThumbnail(context.Background(), "https://ehgt.org/a.jpg"); e == nil || calls != before+1 {
		t.Fatal("unsafe redirect", e, calls)
	}
	c.transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		return reply(r, 200, strings.Repeat("a", (4<<20)+1)), nil
	})
	_, e = c.FetchThumbnail(context.Background(), "https://ehgt.org/a.jpg")
	var remote *model.RemoteError
	if !errors.As(e, &remote) || remote.Code != "thumbnail_too_large" {
		t.Fatal(e)
	}
	old := model.RemoteMetadata{Raw: json.RawMessage(`{"thumb":"https://ehgt.org/old.jpg"}`)}
	if CandidateThumbnail(old) != "https://ehgt.org/old.jpg" {
		t.Fatal("old saved response lost")
	}
	old.Raw = json.RawMessage(`{"thumb":"https://evil.example/old.jpg"}`)
	if CandidateThumbnail(old) != "" {
		t.Fatal("unsafe old response")
	}
	// The proxy configuration is reused, while gallery cookies remain absent.
	settings, _ := v.Settings()
	if _, e = v.Update(SettingPatch{Expected: settings.Revision, Site: str("eh"), ProxyURL: str("http://127.0.0.1:7892")}); e != nil {
		t.Fatal(e)
	}
	c.transport = nil
	p, _ := v.snapshot()
	rt, close, e := c.transportFor(p)
	if e != nil {
		t.Fatal(e)
	}
	defer close()
	proxy, e := rt.(*http.Transport).Proxy(&http.Request{URL: mustURL("https://ehgt.org/a.jpg")})
	if e != nil || proxy.Host != "127.0.0.1:7892" {
		t.Fatal(proxy, e)
	}
}
