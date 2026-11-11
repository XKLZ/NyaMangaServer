package testfixture

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"time"
)

// Remote is an entirely offline provider for isolated test executables. The
// production server never imports this package or exposes a mock switch.
type Remote struct{ Vault *provider.Vault }

func (r *Remote) Settings() (provider.Settings, error) { return r.Vault.Settings() }
func (r *Remote) Configure(p provider.SettingPatch) (provider.Settings, error) {
	return r.Vault.Update(p)
}
func (r *Remote) Credentials(p provider.CredentialPatch) (provider.Settings, error) {
	return r.Vault.Credentials(p)
}
func (r *Remote) Test(ctx context.Context) error { return ctx.Err() }
func (r *Remote) Search(ctx context.Context, _ string) ([]model.Candidate, error) {
	return []model.Candidate{{URL: "https://e-hentai.org/g/1/0123456789/", Title: "匿名候选一"}, {URL: "https://e-hentai.org/g/2/0123456789/", Title: "匿名候选二"}}, ctx.Err()
}
func (r *Remote) Fetch(ctx context.Context, url string) (model.RemoteMetadata, error) {
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return model.RemoteMetadata{}, ctx.Err()
	case <-timer.C:
	}
	normalized, id, _, e := provider.Gallery(url)
	if e != nil {
		return model.RemoteMetadata{}, e
	}
	if id == "999999" {
		return model.RemoteMetadata{}, &model.RemoteError{Code: "network_error", Retryable: true}
	}
	title := "匿名来源标题"
	original := "Anonymous original"
	category := "Manga"
	language := "japanese"
	rating := 4.5
	return model.RemoteMetadata{Thumbnail: "https://ehgt.org/anonymous-preview.png", ExternalID: id, URL: normalized, Fields: model.Metadata{Title: &title, TitleOriginal: &original, Category: &category, Language: &language, Rating: &rating, Tags: []model.TagName{{Namespace: "artist", Name: "anonymous"}, {Namespace: "language", Name: "japanese"}}}, Raw: json.RawMessage(`{"gid":1,"title":"Anonymous fixture"}`)}, nil
}
func (r *Remote) LatestDictionary(ctx context.Context) (model.DictionaryRelease, error) {
	return model.DictionaryRelease{Version: "anonymous-v1", URL: "https://github.com/EhTagTranslation/Database/releases/download/anonymous-v1/db.text.json"}, ctx.Err()
}
func (r *Remote) DownloadDictionary(ctx context.Context, _ model.DictionaryRelease) ([]byte, error) {
	return []byte(`{"data":[{"namespace":"artist","data":{"anonymous":{"name":"匿名作者"},"unused":{"name":"未使用"}}},{"namespace":"language","data":{"japanese":{"name":"日语"}}}]}`), ctx.Err()
}

func (r *Remote) FetchThumbnail(ctx context.Context, url string) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if !provider.ThumbnailURL(url) {
		return nil, model.ErrInput
	}
	img := image.NewRGBA(image.Rect(0, 0, 120, 180))
	for y := 0; y < 180; y++ {
		for x := 0; x < 120; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x + 40), G: uint8(y + 30), B: 110, A: 255})
		}
	}
	var out bytes.Buffer
	if e := png.Encode(&out, img); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}

func (r *Remote) SearchPage(ctx context.Context, mode, q, cursor, site string) (model.SearchPage, error) {
	if cursor != "" {
		return model.SearchPage{Items: []model.Candidate{}}, ctx.Err()
	}
	items, e := r.Search(ctx, q)
	return model.SearchPage{Items: items}, e
}
func (r *Remote) FetchMany(ctx context.Context, urls []string) ([]model.FetchResult, error) {
	out := []model.FetchResult{}
	for _, url := range urls {
		v, e := r.Fetch(ctx, url)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if e != nil {
			out = append(out, model.FetchResult{URL: url, Error: "provider_rejected"})
		} else {
			out = append(out, model.FetchResult{URL: url, Metadata: &v})
		}
	}
	return out, nil
}
