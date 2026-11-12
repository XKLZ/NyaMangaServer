package comic

import (
	"bytes"
	"context"
	"image"
	"image/png"
)

// Thumbnail validates uploads under the archive decoder's shared concurrency and memory budgets.
func (p *Pool) NormalizeUpload(ctx context.Context, raw []byte) ([]byte, error) {
	if len(raw) > 10<<20 {
		return nil, limitError("upload", "page_bytes", int64(len(raw)), 10<<20)
	}
	return p.normalizeImage(ctx, raw, false)
}
func (p *Pool) Thumbnail(ctx context.Context, raw []byte) ([]byte, error) {
	return p.normalizeImage(ctx, raw, true)
}
func (p *Pool) normalizeImage(ctx context.Context, raw []byte, thumbnail bool) ([]byte, error) {
	if len(raw) > int(MaxPage) {
		return nil, limitError("read", "page_bytes", int64(len(raw)), MaxPage)
	}
	release, err := p.enterImage(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	cfg, format, e := image.DecodeConfig(bytes.NewReader(raw))
	if e != nil {
		return nil, ErrImage
	}
	if format != "png" && format != "jpeg" && format != "webp" && format != "bmp" {
		return nil, ErrImage
	}
	pixels := int64(cfg.Width) * int64(cfg.Height)
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxPixels || cfg.Height > MaxPixels || pixels < 0 || pixels > MaxPixels {
		return nil, limitError("decode", "pixels", pixels, MaxPixels)
	}
	free, e := p.reserve(ctx, pixels*12+int64(len(raw))*2+1<<20)
	if e != nil {
		return nil, e
	}
	defer free()
	src, _, e := image.Decode(contextReader{ctx, bytes.NewReader(raw)})
	if e != nil {
		return nil, ErrImage
	}
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	scale := 1.0
	if thumbnail {
		scale = min(1.0, min(320.0/float64(w), 480.0/float64(h)))
	}
	nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		for x := 0; x < nw; x++ {
			dst.Set(x, y, src.At(bounds.Min.X+x*w/nw, bounds.Min.Y+y*h/nh))
		}
	}
	var out limitedBuffer
	if e = png.Encode(&out, dst); e != nil {
		return nil, ErrLimit
	}
	return out.Bytes(), nil
}
