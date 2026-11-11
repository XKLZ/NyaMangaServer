package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"testing"
	"time"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reply(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}
func TestGalleryAndPublicDestinationValidation(t *testing.T) {
	for _, u := range []string{"http://e-hentai.org/g/1/0123456789/", "https://127.0.0.1/g/1/0123456789/", "https://e-hentai.org.evil/g/1/0123456789/", "https://user@e-hentai.org/g/1/0123456789/", "https://e-hentai.org:7892/g/1/0123456789/", "https://e-hentai.org/g/1/0123456789/?a=1", "https://e-hentai.org/g/1/0123456789/#x", "https://e-hentai.org/g/1/0123456789/../"} {
		if _, _, _, e := Gallery(u); e == nil {
			t.Fatal("unsafe URL", u)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "169.254.169.254", "100.64.0.1", "::1", "fc00::1", "::ffff:127.0.0.1", "198.18.0.1"} {
		if publicIP(net.ParseIP(ip)) {
			t.Fatal("nonpublic destination", ip)
		}
	}
	c := New(vaultForTest(t))
	c.resolve = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	if _, e := c.dial(context.Background(), "tcp", "e-hentai.org:443"); e == nil {
		t.Fatal("private DNS accepted")
	}
}
func TestClientCookieIsolationSourceAndCandidates(t *testing.T) {
	v := vaultForTest(t)
	settings, e := v.Credentials(CredentialPatch{Expected: 1, Fields: map[string]SecretChange{"ipb_member_id": {Op: "set", Value: str("999999999")}, "ipb_pass_hash": {Op: "set", Value: str("private-cookie-hash-0123456789")}}})
	if e != nil {
		t.Fatal(e)
	}
	c := New(v)
	c.interval = 0
	requests := 0
	c.transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Host == "e-hentai.org" {
			if !strings.Contains(r.Header.Get("Cookie"), "private-cookie-hash") {
				t.Fatal("cookie missing")
			}
			return reply(r, 200, `<html><a href="/g/123/0123456789/"><img src="/anonymous.png"></a><a href="/g/123/0123456789/"><span>Anonymous A</span></a><a href="https://e-hentai.org/g/124/0123456789/">Anonymous B</a><a href="http://127.0.0.1/">bad</a></html>`), nil
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Fatal("credential crossed host")
		}
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil || body["method"] != "gdata" {
			t.Fatal(body, e)
		}
		return reply(r, 200, `{"gmetadata":[{"gid":123,"token":"0123456789","title":"Anonymous source","title_jpn":"匿名","thumb":"https://ehgt.org/anonymous.jpg","rating":"4.5","posted":"1234567890","category":"Manga","tags":["artist:anonymous","language:japanese"],"cookie":"private-cookie-hash-0123456789"}]}`), nil
	})
	matches, e := c.Search(context.Background(), strings.Repeat("a", 40))
	if e != nil || len(matches) != 2 || matches[0].Title != "Anonymous A" {
		t.Fatal(matches, e)
	}
	result, e := c.Fetch(context.Background(), matches[0].URL)
	if e != nil || result.Fields.Title == nil || *result.Fields.Title != "Anonymous source" || result.Fields.Rating == nil || *result.Fields.Rating != 4.5 || strings.Contains(string(result.Raw), "private-cookie-hash") {
		t.Fatal(result, e)
	}
	if result.Thumbnail != "https://ehgt.org/anonymous.jpg" {
		t.Fatal("gdata thumbnail missing", result.Thumbnail)
	}
	if requests != 2 {
		t.Fatal(requests)
	}
	p, e := v.Update(SettingPatch{Expected: settings.Revision, ProxyURL: str("http://127.0.0.1:7892")})
	if e != nil {
		t.Fatal(e)
	}
	_ = p
	private, e := v.snapshot()
	if e != nil {
		t.Fatal(e)
	}
	c.transport = nil
	transport, close, e := c.transportFor(private)
	if e != nil {
		t.Fatal(e)
	}
	defer close()
	proxy, e := transport.(*http.Transport).Proxy(&http.Request{URL: mustURL("https://e-hentai.org/")})
	if e != nil || proxy.Host != "127.0.0.1:7892" {
		t.Fatal(proxy, e)
	}
}
func TestClientLimitsChallengesAndRedirects(t *testing.T) {
	c := New(vaultForTest(t))
	c.interval = 0
	for _, scenario := range []struct {
		status int
		body   string
		code   string
		pause  bool
	}{{403, "blocked", "provider_auth_required", true}, {200, "<html>cf-chl-challenge</html>", "provider_action_required", true}, {503, "unavailable", "provider_unavailable", false}, {503, "<html>cf-chl-challenge</html>", "provider_action_required", true}, {200, "invalid text", "invalid_response", false}, {200, strings.Repeat("x", (4<<20)+1), "response_too_large", false}} {
		c.transport = roundTripper(func(r *http.Request) (*http.Response, error) { return reply(r, scenario.status, scenario.body), nil })
		_, e := c.Search(context.Background(), strings.Repeat("a", 40))
		var remote *model.RemoteError
		if !errors.As(e, &remote) || remote.Code != scenario.code || remote.ActionRequired != scenario.pause {
			t.Fatal(scenario.code, e)
		}
	}
	c.transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		resp := reply(r, 429, "wait")
		resp.Header.Set("Retry-After", "120")
		return resp, nil
	})
	_, e := c.Fetch(context.Background(), "https://e-hentai.org/g/1/0123456789/")
	var remote *model.RemoteError
	if !errors.As(e, &remote) || !remote.ActionRequired || remote.Wait != 120*time.Second {
		t.Fatal(e)
	}
	calls := 0
	c.transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		resp := reply(r, 302, "")
		resp.Header.Set("Location", "http://127.0.0.1/private")
		return resp, nil
	})
	_, e = c.Search(context.Background(), strings.Repeat("a", 40))
	if e == nil || calls != 1 {
		t.Fatal("unsafe redirect followed", calls, e)
	}
}

func mustURL(raw string) *url.URL { u, _ := url.Parse(raw); return u }

func TestCredentialReplacementDuringRequestCannotLeakOldValue(t *testing.T) {
	v := vaultForTest(t)
	old := "anonymous-old-proxy-password"
	next := "anonymous-new-proxy-password"
	if _, e := v.Credentials(CredentialPatch{Expected: 1, Fields: map[string]SecretChange{"proxy_password": {Op: "set", Value: &old}}}); e != nil {
		t.Fatal(e)
	}
	c := New(v)
	c.interval = 0
	c.transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		if _, e := v.Credentials(CredentialPatch{Expected: 2, Fields: map[string]SecretChange{"proxy_password": {Op: "set", Value: &next}}}); e != nil {
			t.Fatal(e)
		}
		return reply(r, 200, `{"gmetadata":[{"gid":1,"token":"0123456789","title":"anonymous-old-proxy-password","title_jpn":"","tags":[],"note":"anonymous-old-proxy-password"}]}`), nil
	})
	result, e := c.Fetch(context.Background(), "https://e-hentai.org/g/1/0123456789/")
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), old) || strings.Contains(string(encoded), next) {
		t.Fatal("in-flight secret persisted")
	}
}

func TestGdataChallengeStillPausesBeforeJSONParsing(t *testing.T) {
	c := New(vaultForTest(t))
	c.interval = 0
	c.transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		return reply(r, 200, "<html>cf-chl-challenge</html>"), nil
	})
	_, e := c.Fetch(context.Background(), "https://e-hentai.org/g/1/0123456789/")
	var remote *model.RemoteError
	if !errors.As(e, &remote) || !remote.ActionRequired {
		t.Fatal(e)
	}
}
