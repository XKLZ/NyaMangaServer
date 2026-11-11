package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"nyamangaserver.local/server/internal/model"
	"regexp"
	"strings"
	"unicode/utf8"
)

var releasePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func (c *Client) LatestDictionary(ctx context.Context) (model.DictionaryRelease, error) {
	data, e := c.send(ctx, "GET", "https://api.github.com/repos/EhTagTranslation/Database/releases/latest", nil, 4<<20)
	if e != nil {
		return model.DictionaryRelease{}, e
	}
	var release struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"assets"`
	}
	if json.Unmarshal(data, &release) != nil || !releasePattern.MatchString(release.Tag) {
		return model.DictionaryRelease{}, remoteError("invalid_dictionary")
	}
	for _, asset := range release.Assets {
		if asset.Name != "db.text.json" {
			continue
		}
		expected := "https://github.com/EhTagTranslation/Database/releases/download/" + url.PathEscape(release.Tag) + "/db.text.json"
		if asset.URL != expected || asset.Size > 32<<20 {
			return model.DictionaryRelease{}, remoteError("invalid_dictionary")
		}
		digest := strings.TrimPrefix(asset.Digest, "sha256:")
		if asset.Digest != "" && (!regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(digest) || asset.Digest == digest) {
			return model.DictionaryRelease{}, remoteError("invalid_dictionary")
		}
		return model.DictionaryRelease{Version: release.Tag, URL: expected, Digest: digest}, nil
	}
	return model.DictionaryRelease{}, remoteError("invalid_dictionary")
}
func (c *Client) DownloadDictionary(ctx context.Context, release model.DictionaryRelease) ([]byte, error) {
	if !releasePattern.MatchString(release.Version) || release.URL != "https://github.com/EhTagTranslation/Database/releases/download/"+url.PathEscape(release.Version)+"/db.text.json" {
		return nil, remoteError("invalid_dictionary")
	}
	return c.send(ctx, "GET", release.URL, nil, 32<<20)
}
func ParseDictionary(data []byte) (map[model.TagName]string, error) {
	if len(data) > 32<<20 {
		return nil, remoteError("response_too_large")
	}
	type entry struct {
		Name string `json:"name"`
	}
	type block struct {
		Namespace string          `json:"namespace"`
		Data      json.RawMessage `json:"data"`
	}
	var doc struct {
		Data []block `json:"data"`
	}
	if !utf8.Valid(data) || json.Unmarshal(data, &doc) != nil || doc.Data == nil {
		return nil, remoteError("invalid_dictionary")
	}
	out := map[model.TagName]string{}
	count := 0
	for _, b := range doc.Data {
		if b.Namespace == "rows" {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(b.Data))
		token, e := decoder.Token()
		if e != nil || token != json.Delim('{') {
			return nil, remoteError("invalid_dictionary")
		}
		for decoder.More() {
			key, e := decoder.Token()
			if e != nil {
				return nil, remoteError("invalid_dictionary")
			}
			name, ok := key.(string)
			if !ok {
				return nil, remoteError("invalid_dictionary")
			}
			var value entry
			if e = decoder.Decode(&value); e != nil {
				return nil, remoteError("invalid_dictionary")
			}

			count++
			if count > 500000 {
				return nil, remoteError("invalid_dictionary")
			}
			tag := model.CanonicalTag(model.TagName{Namespace: b.Namespace, Name: name})
			raw, _ := json.Marshal([]model.TagName{tag})
			if model.ValidateField("tags", raw) != nil || !utf8.ValidString(value.Name) || strings.ContainsRune(value.Name, 0) || utf8.RuneCountInString(value.Name) > 512 || len(value.Name) > 2048 {
				return nil, remoteError("invalid_dictionary")
			}
			text := value.Name
			if strings.TrimSpace(text) == "" {
				text = ""
			}
			if old, exists := out[tag]; exists && old != text {
				return nil, fmt.Errorf("%w: conflicting dictionary identity", remoteError("invalid_dictionary"))
			}
			out[tag] = text
		}
	}
	if len(out) == 0 {
		return nil, remoteError("invalid_dictionary")
	}
	return out, nil
}
