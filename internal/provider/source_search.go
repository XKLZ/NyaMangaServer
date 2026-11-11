package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"nyamangaserver.local/server/internal/model"
)

// SearchPage uses only server-issued numeric gallery cursors. It never follows
// a page-supplied URL; every request is rebuilt against the configured host.
func (c *Client) SearchPage(ctx context.Context, mode, q, cursor, site string) (model.SearchPage, error) {
	out := model.SearchPage{Items: []model.Candidate{}}
	p, e := c.Vault.snapshot()
	if e != nil {
		return out, e
	}
	if p.Site != site {
		return out, remoteError("provider_settings_changed")
	}
	if site == "ex" && (p.Secrets["ipb_member_id"] == "" || p.Secrets["ipb_pass_hash"] == "" || p.Secrets["igneous"] == "") {
		return out, &model.RemoteError{Code: "provider_auth_required", ActionRequired: true}
	}
	values := url.Values{}
	if mode == "matches" {
		if !sha1Pattern.MatchString(q) {
			return out, model.ErrInput
		}
		values.Set("f_shash", q)
		values.Set("fs_exp", "on")
	} else if mode == "title" {
		if strings.TrimSpace(q) == "" || len(q) > 1024 || !utf8.ValidString(q) || utf8.RuneCountInString(q) > 256 {
			return out, model.ErrInput
		}
		// Neutralize query syntax rather than exposing an arbitrary site query.
		words := strings.Fields(strings.Map(func(r rune) rune {
			if strings.ContainsRune("\"_:()|+-\n\r\t", r) {
				return ' '
			}
			return r
		}, q))
		if len(words) == 0 {
			return out, model.ErrInput
		}
		values.Set("f_search", `title:"`+strings.Join(words, " ")+`"`)
	} else {
		return out, model.ErrInput
	}
	if cursor != "" {
		if _, e = strconv.ParseUint(cursor, 10, 64); e != nil || len(cursor) > 20 {
			return out, model.ErrInput
		}
		values.Set("next", cursor)
	}
	data, e := c.send(ctx, "GET", "https://"+SiteHost(site)+"/?"+values.Encode(), nil, 4<<20)
	if e != nil {
		return out, e
	}
	if e = challenge(data); e != nil {
		return out, e
	}
	return parseSearchPage(data, site, cursor)
}

func parseSearchPage(data []byte, site, cursor string) (model.SearchPage, error) {
	out := model.SearchPage{Items: []model.Candidate{}}
	if !utf8.Valid(data) || !strings.Contains(strings.ToLower(string(data)), "<html") {
		return out, remoteError("invalid_response")
	}
	base, _ := url.Parse("https://" + SiteHost(site) + "/")
	z := html.NewTokenizer(bytes.NewReader(data))
	seen := map[string]int{}
	href, title := "", ""
	recognized := false
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			if z.Err() != io.EOF {
				return out, remoteError("invalid_response")
			}
			break
		}
		t := z.Token()
		if kind == html.StartTagToken {
			for _, a := range t.Attr {
				if a.Key == "class" && strings.Contains(a.Val, "itg") {
					recognized = true
				}
			}
			if t.Data == "a" {
				href, title = "", ""
				raw, id, rel := "", "", ""
				for _, a := range t.Attr {
					switch a.Key {
					case "href":
						raw = a.Val
					case "title":
						title = a.Val
					case "id":
						id = a.Val
					case "rel":
						rel = a.Val
					}
				}
				u, e := url.Parse(raw)
				if e == nil {
					u = base.ResolveReference(u)
					href, _, _, _ = Gallery(u.String())
					if id == "unext" || rel == "next" {
						if u.Scheme != "https" || u.Host != base.Host || u.Path != "/" || u.User != nil {
							return out, remoteError("invalid_response")
						}
						next := u.Query().Get("next")
						if _, e := strconv.ParseUint(next, 10, 64); e != nil || next == cursor || len(next) > 20 {
							return out, remoteError("invalid_response")
						}
						out.Next = next
					}
				}
			}
		}
		if kind == html.TextToken && href != "" && len(title) < 2048 {
			title += t.Data
		}
		if kind == html.TextToken {
			v := strings.ToLower(t.Data)
			if strings.Contains(v, "no hits found") || strings.Contains(v, "no unfiltered results found") {
				recognized = true
			}
		}
		if kind == html.EndTagToken && t.Data == "a" && href != "" {
			recognized = true
			title = strings.TrimSpace(title)
			if len(title) > 2048 {
				title = ""
			}
			if i, ok := seen[href]; ok {
				if out.Items[i].Title == "" {
					out.Items[i].Title = title
				}
			} else {
				if len(out.Items) >= 200 {
					return out, remoteError("search_limit_reached")
				}
				seen[href] = len(out.Items)
				out.Items = append(out.Items, model.Candidate{URL: href, Title: title})
			}
			href = ""
		}
	}
	if !recognized {
		return out, remoteError("invalid_response")
	}
	return out, nil
}

// FetchMany preserves each gallery's identity even when gdata returns errors
// or reorders responses. One malformed entry cannot replace a sibling.
func (c *Client) FetchMany(ctx context.Context, urls []string) ([]model.FetchResult, error) {
	if len(urls) < 1 || len(urls) > 10 {
		return nil, model.ErrInput
	}
	ids := [][]any{}
	canonical := make([]string, len(urls))
	gids := make([]string, len(urls))
	tokens := make([]string, len(urls))
	seen := map[string]bool{}
	for i, raw := range urls {
		u, g, t, e := Gallery(raw)
		if e != nil || seen[g] {
			return nil, model.ErrInput
		}
		seen[g] = true
		n, _ := strconv.ParseInt(g, 10, 64)
		ids = append(ids, []any{n, t})
		canonical[i], gids[i], tokens[i] = u, g, t
	}
	body, _ := json.Marshal(map[string]any{"method": "gdata", "gidlist": ids, "namespace": 1})
	data, e := c.send(ctx, "POST", "https://api.e-hentai.org/api.php", body, 4<<20, true)
	if e != nil {
		return nil, e
	}
	if e = challenge(data); e != nil {
		return nil, e
	}
	var payload struct {
		Items []json.RawMessage `json:"gmetadata"`
	}
	if json.Unmarshal(data, &payload) != nil || len(payload.Items) != len(urls) {
		return nil, remoteError("invalid_response")
	}
	byID := map[string]json.RawMessage{}
	for _, raw := range payload.Items {
		var v struct {
			GID json.RawMessage `json:"gid"`
		}
		if json.Unmarshal(raw, &v) != nil {
			return nil, remoteError("invalid_response")
		}
		g := textNumber(v.GID)
		if !seen[g] || byID[g] != nil {
			return nil, remoteError("source_mismatch")
		}
		byID[g] = raw
	}
	out := make([]model.FetchResult, len(urls))
	for i, u := range canonical {
		out[i].URL = u
		m, e := c.parseGallery(byID[gids[i]], u, gids[i], tokens[i])
		if e != nil {
			out[i].Error = "invalid_response"
			if r, ok := e.(*model.RemoteError); ok {
				out[i].Error = r.Code
			}
		} else {
			out[i].Metadata = &m
		}
	}
	return out, nil
}
