package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/net/html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"nyamangaserver.local/server/internal/model"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var errBlockedDestination = errors.New("blocked_destination")
var errBlockedRedirect = errors.New("blocked_redirect")

var galleryPath = regexp.MustCompile(`^/g/([1-9][0-9]*)/([a-fA-F0-9]{10})/?$`)
var sha1Pattern = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

func Gallery(raw string) (string, string, string, error) {
	if len(raw) > 2048 {
		return "", "", "", model.ErrInput
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || u.RawPath != "" {
		return "", "", "", model.ErrInput
	}
	host := strings.ToLower(u.Hostname())
	if host != "e-hentai.org" && host != "exhentai.org" {
		return "", "", "", model.ErrInput
	}
	parts := galleryPath.FindStringSubmatch(u.Path)
	if parts == nil {
		return "", "", "", model.ErrInput
	}
	if _, e = strconv.ParseInt(parts[1], 10, 64); e != nil {
		return "", "", "", model.ErrInput
	}
	token := strings.ToLower(parts[2])
	return "https://" + host + "/g/" + parts[1] + "/" + token + "/", parts[1], token, nil
}
func SiteHost(site string) string {
	if site == "ex" {
		return "exhentai.org"
	}
	return "e-hentai.org"
}

type Client struct {
	Vault     *Vault
	rate      chan struct{}
	last      time.Time
	interval  time.Duration
	transport http.RoundTripper // injectable only from provider's own tests
	resolve   func(context.Context, string) ([]net.IPAddr, error)
}

func New(v *Vault) *Client {
	return &Client{Vault: v, rate: make(chan struct{}, 1), interval: 2 * time.Second, resolve: net.DefaultResolver.LookupIPAddr}
}
func (c *Client) Settings() (Settings, error)                      { return c.Vault.Settings() }
func (c *Client) Configure(in SettingPatch) (Settings, error)      { return c.Vault.Update(in) }
func (c *Client) Credentials(in CredentialPatch) (Settings, error) { return c.Vault.Credentials(in) }

func publicIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	for _, raw := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		if netip.MustParsePrefix(raw).Contains(addr) {
			return false
		}
	}
	return true
}
func (c *Client) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, errBlockedDestination
	}
	ips, e := c.resolve(ctx, host)
	if e != nil {
		return nil, errors.New("dns_failed")
	}
	if len(ips) == 0 {
		return nil, errors.New("dns_failed")
	}
	for _, ip := range ips {
		if !publicIP(ip.IP) {
			return nil, errBlockedDestination
		}
	}
	var last error
	for _, ip := range ips {
		d := net.Dialer{Timeout: 10 * time.Second}
		conn, e := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if e == nil {
			return conn, nil
		}
		last = e
	}
	return nil, last
}
func allowed(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "e-hentai.org", "exhentai.org":
		if u.Path == "/" {
			return true
		}
		_, _, _, e := Gallery(u.String())
		return e == nil
	case "api.e-hentai.org":
		return u.Path == "/api.php" && u.RawQuery == ""
	case "api.github.com":
		return u.Path == "/repos/EhTagTranslation/Database/releases/latest" && u.RawQuery == ""
	case "github.com":
		return strings.HasPrefix(u.Path, "/EhTagTranslation/Database/releases/download/") && strings.HasSuffix(u.Path, "/db.text.json") && u.RawQuery == ""
	case "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}
func (c *Client) transportFor(p privateSettings) (http.RoundTripper, func(), error) {
	if c.transport != nil {
		return c.transport, func() {}, nil
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.MaxConnsPerHost = 1
	tr.TLSHandshakeTimeout = 10 * time.Second
	tr.ResponseHeaderTimeout = 20 * time.Second
	if p.ProxyURL == "" {
		tr.DialContext = c.dial
	} else {
		u, e := url.Parse(p.ProxyURL)
		if e != nil {
			return nil, nil, model.ErrInput
		}
		if p.Secrets["proxy_username"] != "" || p.Secrets["proxy_password"] != "" {
			u.User = url.UserPassword(p.Secrets["proxy_username"], p.Secrets["proxy_password"])
		}
		tr.Proxy = http.ProxyURL(u)
		tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	return tr, tr.CloseIdleConnections, nil
}
func remoteError(code string) *model.RemoteError { return &model.RemoteError{Code: code} }
func (c *Client) send(ctx context.Context, method, raw string, body []byte, limit int64, redactJSON ...bool) ([]byte, error) {
	u, e := url.Parse(raw)
	if e != nil || !allowed(u) {
		return nil, remoteError("blocked_destination")
	}
	select {
	case c.rate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.rate }()
	if wait := time.Until(c.last.Add(c.interval)); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	p, e := c.Vault.snapshot()
	if e != nil {
		return nil, e
	}
	rt, closeTransport, e := c.transportFor(p)
	if e != nil {
		return nil, e
	}
	defer closeTransport()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, method, raw, bytes.NewReader(body))
	if e != nil {
		return nil, remoteError("invalid_response")
	}
	req.Header.Set("User-Agent", "NyaMangaServer/0.3")
	req.Header.Set("Accept", "application/json,text/html;q=0.8")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.URL.Hostname() == SiteHost(p.Site) {
		for _, key := range []string{"ipb_member_id", "ipb_pass_hash", "igneous"} {
			if key == "igneous" && p.Site != "ex" {
				continue
			}
			if value := p.Secrets[key]; value != "" {
				req.AddCookie(&http.Cookie{Name: key, Value: value})
			}
		}
	}
	client := http.Client{Transport: rt, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !allowed(next.URL) {
			return errBlockedRedirect
		}
		// Redirects may not change the trust category, method, or carry cookies.
		from := via[0].URL.Hostname()
		to := next.URL.Hostname()
		github := from == "github.com"
		if from != to && !(github && (to == "release-assets.githubusercontent.com" || to == "objects.githubusercontent.com")) {
			return errBlockedRedirect
		}
		if next.Method != via[0].Method {
			return errBlockedRedirect
		}
		if from != to {
			next.Header.Del("Cookie")
		}
		return nil
	}}
	c.last = time.Now()
	resp, e := client.Do(req)
	if e != nil {
		if errors.Is(e, errBlockedDestination) || errors.Is(e, errBlockedRedirect) {
			return nil, remoteError("blocked_destination")
		}
		if ctx.Err() != nil {
			return nil, &model.RemoteError{Code: "network_timeout", Retryable: true}
		}
		return nil, &model.RemoteError{Code: "network_error", Retryable: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		wait := 2 * time.Second
		if n, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && n >= 0 {
			if n > 86400 {
				n = 86400
			}
			wait = time.Duration(n) * time.Second
		} else if date, e := http.ParseTime(resp.Header.Get("Retry-After")); e == nil && time.Until(date) > 0 {
			wait = time.Until(date)
		}
		return nil, &model.RemoteError{Code: "rate_limited", Retryable: wait <= time.Minute, ActionRequired: wait > time.Minute, Wait: wait}
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, &model.RemoteError{Code: "provider_auth_required", ActionRequired: true}
	}
	if resp.StatusCode >= 500 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if int64(len(data)) <= limit {
			if e := challenge(data); e != nil {
				return nil, e
			}
		}
		return nil, &model.RemoteError{Code: "provider_unavailable", Retryable: true}
	}
	if resp.StatusCode == 404 {
		return nil, remoteError("no_match")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, remoteError("invalid_response")
	}
	if resp.ContentLength > limit {
		return nil, remoteError("response_too_large")
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return nil, &model.RemoteError{Code: "network_error", Retryable: true}
	}
	if int64(len(data)) > limit {
		return nil, remoteError("response_too_large")
	}
	// Sanitize against the exact credentials used by this request, even when
	// an administrator replaces them while the request is in flight.
	if len(redactJSON) > 0 && redactJSON[0] {
		if e := challenge(data); e != nil {
			return nil, e
		}
		var value any
		if !utf8.Valid(data) || json.Unmarshal(data, &value) != nil {
			return nil, remoteError("invalid_response")
		}
		data, e = json.Marshal(sanitize(value, p.Secrets))
		if int64(len(data)) > limit {
			return nil, remoteError("response_too_large")
		}
		if e != nil {
			return nil, remoteError("invalid_response")
		}
	}
	return data, nil
}
func challenge(data []byte) error {
	body := strings.ToLower(string(data))
	for _, v := range []string{"cf-chl-", "g-recaptcha", "h-captcha", "just a moment...", "your ip address has been temporarily banned", "sad panda", "exhentai.org is not available"} {
		if strings.Contains(body, v) {
			return &model.RemoteError{Code: "provider_action_required", ActionRequired: true}
		}
	}
	return nil
}
func (c *Client) Search(ctx context.Context, hash string) ([]model.Candidate, error) {
	if !sha1Pattern.MatchString(hash) {
		return nil, remoteError("no_sha1")
	}
	p, e := c.Vault.snapshot()
	if e != nil {
		return nil, e
	}
	if p.Site == "ex" && (p.Secrets["ipb_member_id"] == "" || p.Secrets["ipb_pass_hash"] == "" || p.Secrets["igneous"] == "") {
		return nil, &model.RemoteError{Code: "provider_auth_required", ActionRequired: true}
	}
	root := "https://" + SiteHost(p.Site) + "/"
	query := url.Values{"f_shash": {strings.ToLower(hash)}, "fs_exp": {"on"}}
	data, e := c.send(ctx, "GET", root+"?"+query.Encode(), nil, 4<<20)
	if e != nil {
		return nil, e
	}
	if e = challenge(data); e != nil {
		return nil, e
	}
	if !utf8.Valid(data) || !strings.Contains(strings.ToLower(string(data)), "<html") {
		return nil, remoteError("invalid_response")
	}
	tokenizer := html.NewTokenizer(bytes.NewReader(data))
	out := []model.Candidate{}
	seen := map[string]int{}
	var href, title string
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			if tokenizer.Err() != io.EOF {
				return nil, remoteError("invalid_response")
			}
			break
		}
		token := tokenizer.Token()
		if kind == html.StartTagToken && token.Data == "a" {
			href = ""
			title = ""
			for _, a := range token.Attr {
				if a.Key == "href" {
					base, _ := url.Parse(root)
					u, e := url.Parse(a.Val)
					if e == nil {
						href, _, _, _ = Gallery(base.ResolveReference(u).String())
					}
				}
				if a.Key == "title" {
					title = a.Val
				}
			}
		}
		if kind == html.TextToken && href != "" && len(title) < 512 {
			title += token.Data
		}
		if kind == html.EndTagToken && token.Data == "a" && href != "" {
			title = strings.TrimSpace(title)
			if len(title) > 2048 {
				title = ""
			}
			if index, ok := seen[href]; ok {
				// Thumbnail links often precede a second link containing the title.
				if out[index].Title == "" && title != "" {
					out[index].Title = title
				}
			} else if len(out) < 10 {
				seen[href] = len(out)
				out = append(out, model.Candidate{URL: href, Title: title})
			}
			href = ""
		}
	}
	return out, nil
}
func textNumber(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}
func sanitize(v any, secrets map[string]string) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, v := range x {
			lower := strings.ToLower(k)
			if strings.Contains(lower, "cookie") || strings.Contains(lower, "password") || strings.Contains(lower, "authorization") || strings.Contains(lower, "credential") || strings.Contains(lower, "secret") || strings.HasPrefix(lower, "ipb_") || lower == "igneous" {
				continue
			}
			out[k] = sanitize(v, secrets)
		}
		return out
	case []any:
		for i := range x {
			x[i] = sanitize(x[i], secrets)
		}
		return x
	case string:
		for _, secret := range secrets {
			if secret != "" {
				x = strings.ReplaceAll(x, secret, "[redacted]")
			}
		}
		return x
	}
	return v
}
func (c *Client) Fetch(ctx context.Context, raw string) (model.RemoteMetadata, error) {
	normalized, gid, token, e := Gallery(raw)
	if e != nil {
		return model.RemoteMetadata{}, model.ErrInput
	}
	number, _ := strconv.ParseInt(gid, 10, 64)
	body, _ := json.Marshal(map[string]any{"method": "gdata", "gidlist": [][]any{{number, token}}, "namespace": 1})
	data, e := c.send(ctx, "POST", "https://api.e-hentai.org/api.php", body, 4<<20, true)
	if e != nil {
		return model.RemoteMetadata{}, e
	}
	if e = challenge(data); e != nil {
		return model.RemoteMetadata{}, e
	}
	var payload struct {
		Items []json.RawMessage `json:"gmetadata"`
	}
	if json.Unmarshal(data, &payload) != nil || len(payload.Items) != 1 {
		return model.RemoteMetadata{}, remoteError("invalid_response")
	}
	return c.parseGallery(payload.Items[0], normalized, gid, token)
}
func (c *Client) parseGallery(raw json.RawMessage, normalized, gid, token string) (model.RemoteMetadata, error) {
	var item struct {
		GID       json.RawMessage `json:"gid"`
		Token     string          `json:"token"`
		Title     *string         `json:"title"`
		Original  *string         `json:"title_jpn"`
		Thumbnail string          `json:"thumb"`
		Category  *string         `json:"category"`
		Rating    json.RawMessage `json:"rating"`
		Posted    json.RawMessage `json:"posted"`
		Tags      []string        `json:"tags"`
		Error     string          `json:"error"`
	}
	if json.Unmarshal(raw, &item) != nil {
		return model.RemoteMetadata{}, remoteError("invalid_response")
	}
	if item.Error != "" {
		return model.RemoteMetadata{}, remoteError("provider_rejected")
	}
	if textNumber(item.GID) != gid || item.Token != token {
		return model.RemoteMetadata{}, remoteError("source_mismatch")
	}
	if item.Original != nil && strings.TrimSpace(*item.Original) == "" {
		item.Original = nil
	}
	fields := model.Metadata{Title: item.Title, TitleOriginal: item.Original, Category: item.Category, Tags: []model.TagName{}}
	pages, size, infoErr := ParseRemoteFileInfo(raw, gid)
	if infoErr != nil {
		return model.RemoteMetadata{}, remoteError("invalid_response")
	}
	fields.RemotePageCount = pages
	fields.RemoteSizeBytes = size
	if len(item.Rating) > 0 && string(item.Rating) != "null" {
		rating, e := strconv.ParseFloat(textNumber(item.Rating), 64)
		if e != nil {
			return model.RemoteMetadata{}, remoteError("invalid_response")
		}
		fields.Rating = &rating
	}
	if len(item.Posted) > 0 && string(item.Posted) != "null" {
		posted, e := strconv.ParseInt(textNumber(item.Posted), 10, 64)
		if e != nil || posted < 0 || posted > 253402300799 {
			return model.RemoteMetadata{}, remoteError("invalid_response")
		}
		v := time.Unix(posted, 0).UTC().Format(time.RFC3339)
		fields.PublishedAt = &v
	}
	seen := map[model.TagName]bool{}
	for _, tag := range item.Tags {
		namespace, name, found := strings.Cut(tag, ":")
		if !found {
			name = namespace
			namespace = "other"
		}
		v := model.CanonicalTag(model.TagName{Namespace: namespace, Name: name})
		if !seen[v] {
			fields.Tags = append(fields.Tags, v)
			seen[v] = true
		}
		if namespace == "language" && fields.Language == nil {
			v := name
			fields.Language = &v
		}
	}
	if model.ValidateMetadata(fields) != nil {
		return model.RemoteMetadata{}, remoteError("invalid_response")
	}
	p, e := c.Vault.snapshot()
	if e != nil {
		return model.RemoteMetadata{}, e
	}
	var rawValue any
	rawDecoder := json.NewDecoder(bytes.NewReader(raw))
	rawDecoder.UseNumber()
	if rawDecoder.Decode(&rawValue) != nil {
		return model.RemoteMetadata{}, remoteError("invalid_response")
	}
	safe, _ := json.Marshal(sanitize(rawValue, p.Secrets))
	canonical, _ := json.Marshal(fields)
	var canonicalValue any
	canonicalDecoder := json.NewDecoder(bytes.NewReader(canonical))
	canonicalDecoder.UseNumber()
	_ = canonicalDecoder.Decode(&canonicalValue)
	canonical, _ = json.Marshal(sanitize(canonicalValue, p.Secrets))
	if json.Unmarshal(canonical, &fields) != nil || model.ValidateMetadata(fields) != nil {
		return model.RemoteMetadata{}, remoteError("invalid_response")
	}
	thumbnail := ""
	var safeThumbnail struct {
		URL string `json:"thumb"`
	}
	if json.Unmarshal(safe, &safeThumbnail) == nil && ThumbnailURL(safeThumbnail.URL) {
		thumbnail = safeThumbnail.URL
	}
	return model.RemoteMetadata{ExternalID: gid, URL: normalized, Fields: fields, Raw: safe, Thumbnail: thumbnail}, nil
}
func (c *Client) Test(ctx context.Context) error {
	p, e := c.Vault.snapshot()
	if e != nil {
		return e
	}
	if p.Site == "ex" && (p.Secrets["ipb_member_id"] == "" || p.Secrets["ipb_pass_hash"] == "" || p.Secrets["igneous"] == "") {
		return &model.RemoteError{Code: "provider_auth_required", ActionRequired: true}
	}
	data, e := c.send(ctx, "GET", "https://"+SiteHost(p.Site)+"/", nil, 4<<20)
	if e != nil {
		return e
	}
	if e := challenge(data); e != nil {
		return e
	}
	if !utf8.Valid(data) || !strings.Contains(strings.ToLower(string(data)), "<html") {
		return &model.RemoteError{Code: "provider_action_required", ActionRequired: true}
	}
	return nil
}
