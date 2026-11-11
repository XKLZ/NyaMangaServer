package comic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"golang.org/x/image/bmp"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestTagImageFormatsAlphaCropAndOrientation(t *testing.T) {
	p := NewPool()
	defer p.Close()
	ctx := context.Background()
	src := image.NewNRGBA(image.Rect(0, 0, 4, 8))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 128})
	for _, format := range []string{"png", "jpeg", "bmp", "webp"} {
		var b bytes.Buffer
		switch format {
		case "png":
			png.Encode(&b, src)
		case "jpeg":
			jpeg.Encode(&b, src, nil)
		case "bmp":
			bmp.Encode(&b, src)
		case "webp":
			raw, _ := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
			b.Write(raw)
		}
		data, w, h, f, e := p.TagImage(ctx, b.Bytes(), "foreground")
		if e != nil || f != format || w < 1 || h < 1 {
			t.Fatal(format, w, h, f, e)
		}
		decoded, e := png.Decode(bytes.NewReader(data))
		if e != nil {
			t.Fatal(e)
		}
		if format == "png" {
			_, _, _, alpha := decoded.At(0, 0).RGBA()
			if alpha != 128*257 {
				t.Fatal("alpha lost", alpha)
			}
		}
		if format == "jpeg" {
			// APP1 EXIF orientation6 rotates 4x8 clockwise to8x4.
			exif := []byte{'E', 'x', 'i', 'f', 0, 0, 'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 1, 3, 0, 1, 0, 0, 0, 6, 0, 0, 0, 0, 0, 0, 0}
			oriented := append([]byte{}, b.Bytes()[:2]...)
			oriented = append(oriented, 255, 0xe1, 0, byte(len(exif)+2))
			oriented = append(oriented, exif...)
			oriented = append(oriented, b.Bytes()[2:]...)
			data, w, h, _, e = p.TagImage(ctx, oriented, "foreground")
			if e != nil || w != 8 || h != 4 {
				t.Fatal(w, h, e)
			}
			cfg, _, _ := image.DecodeConfig(bytes.NewReader(data))
			if cfg.Width != 8 || cfg.Height != 4 {
				t.Fatal(cfg)
			}
			rotated, _ := png.Decode(bytes.NewReader(data))
			original, _ := jpeg.Decode(bytes.NewReader(b.Bytes()))
			for y := 0; y < 8; y++ {
				for x := 0; x < 4; x++ {
					r, g, bb, a := original.At(x, y).RGBA()
					rr, gg, bbb, aa := rotated.At(7-y, x).RGBA()
					if r != rr || g != gg || bb != bbb || a != aa {
						t.Fatal("orientation pixels differ", x, y)
					}
				}
			}

		}
		bg, _, _, _, e := p.TagImage(ctx, b.Bytes(), "background")
		if e != nil {
			t.Fatal(e)
		}
		cfg, _, _ := image.DecodeConfig(bytes.NewReader(bg))
		if cfg.Width != 1024 || cfg.Height != 512 {
			t.Fatal(cfg)
		}
	}
	if _, _, _, _, e := p.TagImage(ctx, bytes.Repeat([]byte{1}, 10<<20+1), "foreground"); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	for _, raw := range [][]byte{[]byte("not an image"), {137, 80, 78, 71}, []byte("GIF89a")} {
		if _, _, _, _, e := p.TagImage(ctx, raw, "foreground"); !errors.Is(e, ErrImage) {
			t.Fatal(e)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, src)
	raw := b.Bytes()
	binary.BigEndian.PutUint32(raw[16:20], MaxPixels+1)
	binary.BigEndian.PutUint32(raw[29:33], crc32.ChecksumIEEE(raw[12:29]))
	if _, _, _, _, e := p.TagImage(ctx, raw, "foreground"); !errors.Is(e, ErrLimit) {
		t.Fatal("pixel limit not enforced", e)
	}
	// Valid header below MaxPixels but above the shared 512MiB decoder budget.
	binary.BigEndian.PutUint32(raw[16:20], 7000)
	binary.BigEndian.PutUint32(raw[20:24], 7000)
	binary.BigEndian.PutUint32(raw[29:33], crc32.ChecksumIEEE(raw[12:29]))
	if _, _, _, _, e := p.TagImage(ctx, raw, "foreground"); !errors.Is(e, ErrLimit) {
		t.Fatal("memory budget", e)
	}
	// Cancellation and the same pool reservation gate used by comic images.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, _, _, e := p.TagImage(canceled, b.Bytes(), "foreground"); e == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestTagImageEXIFContainers(t *testing.T) {
	ctx := context.Background()
	p := NewPool()
	defer p.Close()
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 1, 3, 0, 1, 0, 0, 0, 6, 0, 0, 0, 0, 0, 0, 0}
	// PNG eXIf is a TIFF payload with an ordinary PNG chunk CRC.
	src := image.NewNRGBA(image.Rect(0, 0, 3, 5))
	src.SetNRGBA(0, 0, color.NRGBA{G: 255, A: 128})
	var b bytes.Buffer
	png.Encode(&b, src)
	chunk := make([]byte, 8)
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(tiff)))
	copy(chunk[4:], "eXIf")
	chunk = append(chunk, tiff...)
	crc := make([]byte, 4)
	binary.BigEndian.PutUint32(crc, crc32.ChecksumIEEE(chunk[4:]))
	chunk = append(chunk, crc...)
	raw := append(append(append([]byte{}, b.Bytes()[:33]...), chunk...), b.Bytes()[33:]...)
	data, w, h, format, e := p.TagImage(ctx, raw, "foreground")
	if e != nil || w != 5 || h != 3 || format != "png" {
		t.Fatal(w, h, format, e)
	}
	decoded, e := png.Decode(bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	_, green, _, alpha := decoded.At(4, 0).RGBA()
	if alpha != 128*257 || green != 128*257 {
		t.Fatal("PNG EXIF alpha rotation", green, alpha)
	}
	// A valid VP8X container advertises EXIF and carries the unmodified VP8 image.
	webp, _ := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	extended := append([]byte{}, webp[:12]...)
	extended = append(extended, []byte{'V', 'P', '8', 'X', 10, 0, 0, 0, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0}...)
	extended = append(extended, webp[12:]...)
	extended = append(extended, []byte{'E', 'X', 'I', 'F', byte(len(tiff)), 0, 0, 0}...)
	extended = append(extended, tiff...)
	binary.LittleEndian.PutUint32(extended[4:8], uint32(len(extended)-8))
	if imageOrientation(extended, "webp") != 6 {
		t.Fatal("WebP EXIF not read")
	}
	if _, _, _, format, e = p.TagImage(ctx, extended, "foreground"); e != nil || format != "webp" {
		t.Fatal(format, e)
	}
	for ori := 1; ori <= 8; ori++ {
		tiff[18] = byte(ori)
		if tiffOrientation(tiff) != ori {
			t.Fatal("TIFF direction", ori)
		}
	}
}
