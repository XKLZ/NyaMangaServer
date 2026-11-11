package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"time"
)

// ThumbnailURL is a distinct trust category. It cannot access gallery/API,
// arbitrary hosts, credentials, query parameters or private-network targets.
func ThumbnailURL(raw string) bool {
	if len(raw) == 0 || len(raw) > 2048 {
		return false
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.RawPath != "" || (u.Port() != "" && u.Port() != "443") || u.Path == "" || u.Path == "/" {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "ehgt.org":
		return true
	case "s.exhentai.org":
		return strings.HasPrefix(u.Path, "/t/")
	}
	return false
}
func CandidateThumbnail(c model.RemoteMetadata) string {
	if ThumbnailURL(c.Thumbnail) {
		return c.Thumbnail
	}
	if len(c.Raw) > 4<<20 {
		return ""
	}
	var saved struct {
		Thumb string `json:"thumb"`
	}
	if json.Unmarshal(c.Raw, &saved) == nil && ThumbnailURL(saved.Thumb) {
		return saved.Thumb
	}
	return ""
}
func (c *Client) thumbnailDestination(ctx context.Context, u *url.URL) error {
	if u == nil || !ThumbnailURL(u.String()) {
		return remoteError("blocked_destination")
	}
	ips, e := c.resolve(ctx, u.Hostname())
	if e != nil || len(ips) == 0 {
		return remoteError("thumbnail_unavailable")
	}
	for _, ip := range ips {
		if !publicIP(ip.IP) {
			return remoteError("blocked_destination")
		}
	}
	return nil
}
func (c *Client) FetchThumbnail(ctx context.Context, raw string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	u, e := url.Parse(raw)
	if e != nil {
		return nil, remoteError("blocked_destination")
	}
	if e = c.thumbnailDestination(ctx, u); e != nil {
		return nil, e
	}
	settings, e := c.Vault.snapshot()
	if e != nil {
		return nil, e
	}
	transport, closeTransport, e := c.transportFor(settings)
	if e != nil {
		return nil, e
	}
	defer closeTransport()
	request, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return nil, remoteError("blocked_destination")
	}
	request.Header.Set("User-Agent", "NyaMangaServer/0.3")
	request.Header.Set("Accept", "image/jpeg,image/png,image/webp")
	client := http.Client{Transport: transport, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 3 || next.Method != "GET" {
			return errBlockedRedirect
		}
		next.Header.Del("Cookie")
		next.Header.Del("Authorization")
		return c.thumbnailDestination(ctx, next.URL)
	}}
	// Site credentials are deliberately never attached to this image client.
	response, e := client.Do(request)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var remote *model.RemoteError
		if errors.As(e, &remote) {
			return nil, remote
		}
		return nil, remoteError("thumbnail_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, remoteError("thumbnail_unavailable")
	}
	if response.ContentLength > 4<<20 {
		return nil, remoteError("thumbnail_too_large")
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if e != nil {
		return nil, remoteError("thumbnail_unavailable")
	}
	if len(data) > 4<<20 {
		return nil, remoteError("thumbnail_too_large")
	}
	return data, nil
}
