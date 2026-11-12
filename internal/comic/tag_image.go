package comic

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/png"
)

// TagImage uses the archive pool's shared decoder concurrency and memory budget.
// Original bytes stay private; only this bounded, metadata-free PNG is served.
func (p *Pool) TagImage(ctx context.Context, raw []byte, role string) (data []byte, width, height int, format string, err error) {
	if len(raw) > 10<<20 {
		return nil, 0, 0, "", limitError("upload", "page_bytes", int64(len(raw)), 10<<20)
	}
	if role != "foreground" && role != "background" {
		return nil, 0, 0, "", ErrImage
	}
	release, err := p.enterImage(ctx)
	if err != nil {
		return nil, 0, 0, "", err
	}
	defer release()
	cfg, format, e := image.DecodeConfig(bytes.NewReader(raw))
	if e != nil || (format != "png" && format != "jpeg" && format != "webp" && format != "bmp") {
		return nil, 0, 0, "", ErrImage
	}
	pixels := int64(cfg.Width) * int64(cfg.Height)
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxPixels || cfg.Height > MaxPixels || pixels > MaxPixels {
		return nil, 0, 0, "", limitError("decode", "pixels", pixels, MaxPixels)
	}
	free, e := p.reserve(ctx, pixels*12+int64(len(raw))*2+8<<20)
	if e != nil {
		return nil, 0, 0, "", e
	}
	defer free()
	src, _, e := image.Decode(contextReader{ctx, bytes.NewReader(raw)})
	if e != nil {
		return nil, 0, 0, "", ErrImage
	}
	ori := imageOrientation(raw, format)
	width, height = cfg.Width, cfg.Height
	if ori >= 5 {
		width, height = height, width
	}
	nw, nh := 1024, 512
	cw, ch := width, height
	ox, oy := 0, 0
	if role == "foreground" {
		scale := min(1.0, min(768.0/float64(width), 768.0/float64(height)))
		nw = max(1, int(float64(width)*scale))
		nh = max(1, int(float64(height)*scale))
	} else {
		if width > height*2 {
			cw = height * 2
			ox = (width - cw) / 2
		} else {
			ch = max(1, width/2)
			oy = (height - ch) / 2
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	bounds := src.Bounds()
	for y := 0; y < nh; y++ {
		if e = ctx.Err(); e != nil {
			return nil, 0, 0, "", e
		}
		for x := 0; x < nw; x++ {
			sx, sy := ox+x*cw/nw, oy+y*ch/nh
			switch ori {
			case 2:
				sx = cfg.Width - 1 - sx
			case 3:
				sx, sy = cfg.Width-1-sx, cfg.Height-1-sy
			case 4:
				sy = cfg.Height - 1 - sy
			case 5:
				sx, sy = sy, sx
			case 6:
				sx, sy = sy, cfg.Height-1-sx
			case 7:
				sx, sy = cfg.Width-1-sy, cfg.Height-1-sx
			case 8:
				sx, sy = cfg.Width-1-sy, sx
			}
			dst.Set(x, y, src.At(bounds.Min.X+sx, bounds.Min.Y+sy))
		}
	}
	var out limitedBuffer
	if e = png.Encode(&out, dst); e != nil {
		return nil, 0, 0, "", ErrLimit
	}
	return out.Bytes(), width, height, format, nil
}
func imageOrientation(raw []byte, format string) int {
	if format == "jpeg" {
		for off := 2; off < len(raw); {
			if raw[off] != 255 {
				return 1
			}
			for off < len(raw) && raw[off] == 255 {
				off++
			}
			if off >= len(raw) {
				return 1
			}
			marker := raw[off]
			off++
			if marker == 0xda || marker == 0xd9 {
				return 1
			}
			if marker == 0xd8 || marker == 1 || marker >= 0xd0 && marker <= 0xd7 {
				continue
			}
			if off+2 > len(raw) {
				return 1
			}
			n := int(binary.BigEndian.Uint16(raw[off : off+2]))
			if n < 2 || n > len(raw)-off {
				return 1
			}
			body := raw[off+2 : off+n]
			if marker == 0xe1 && bytes.HasPrefix(body, []byte("Exif\x00\x00")) {
				return tiffOrientation(body[6:])
			}
			off += n
		}
	}

	if format == "png" {
		for off := 8; off+12 <= len(raw); {
			n := uint64(binary.BigEndian.Uint32(raw[off : off+4]))
			if n > uint64(len(raw)-off-12) {
				break
			}
			if string(raw[off+4:off+8]) == "eXIf" {
				return tiffOrientation(raw[off+8 : off+8+int(n)])
			}
			off += 12 + int(n)
		}
	}
	if format == "webp" {
		for off := 12; off+8 <= len(raw); {
			n := uint64(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
			if n > uint64(len(raw)-off-8) {
				break
			}
			if string(raw[off:off+4]) == "EXIF" {
				body := raw[off+8 : off+8+int(n)]
				body = bytes.TrimPrefix(body, []byte("Exif\x00\x00"))
				return tiffOrientation(body)
			}
			off += 8 + int(n) + (int(n) & 1)
		}
	}
	return 1
}
func tiffOrientation(b []byte) int {
	if len(b) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(b[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(b[2:4]) != 42 {
		return 1
	}
	off := uint64(order.Uint32(b[4:8]))
	if off+2 > uint64(len(b)) {
		return 1
	}
	n := int(order.Uint16(b[off : off+2]))
	off += 2
	for i := 0; i < n && off+12 <= uint64(len(b)); i++ {
		v := b[off : off+12]
		if order.Uint16(v[:2]) == 0x112 && order.Uint16(v[2:4]) == 3 && order.Uint32(v[4:8]) == 1 {
			o := int(order.Uint16(v[8:10]))
			if o >= 1 && o <= 8 {
				return o
			}
			return 1
		}
		off += 12
	}
	return 1
}
