// Package testfixture generates anonymous archives for isolated acceptance.
package testfixture

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
)

const Manifest = ".nyamanga-anonymous-fixtures.json"

func Write(root string) error {
	img := image.NewRGBA(image.Rect(0, 0, 12, 18))
	for y := 0; y < 18; y++ {
		for x := 0; x < 12; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 16), uint8(y * 12), 100, 255})
		}
	}
	var pngBytes bytes.Buffer
	if e := png.Encode(&pngBytes, img); e != nil {
		return e
	}
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	for _, page := range []string{"10.PNG", "2.png", "1.png"} {
		w, e := z.Create(page)
		if e != nil {
			return e
		}
		if _, e = w.Write(pngBytes.Bytes()); e != nil {
			return e
		}
	}
	if e := z.Close(); e != nil {
		return e
	}
	files := map[string][]byte{"合成1.cbz": archive.Bytes(), "子目录/合成2.zip": archive.Bytes(), "损坏.zip": []byte("anonymous invalid ZIP fixture")}
	hashes := map[string]string{}
	for name, data := range files {
		hashes[name] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	manifest, e := json.Marshal(struct {
		Kind  string            `json:"kind"`
		Files map[string]string `json:"files"`
	}{"nyamangaserver-anonymous-v1", hashes})
	if e != nil {
		return e
	}
	files[Manifest] = manifest
	if info, e := os.Lstat(root); e == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("fixture output must be a real isolated directory")
		}
		// Repeated generation is allowed only when all existing bytes are already
		// exactly this anonymous fixture set. Never overwrite foreign material.
		if e = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			name, e := filepath.Rel(root, path)
			if e != nil {
				return e
			}
			data, ok := files[filepath.ToSlash(name)]
			if !ok || !d.Type().IsRegular() {
				return errors.New("fixture directory contains foreign material")
			}
			existing, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			if !bytes.Equal(data, existing) {
				return errors.New("fixture content differs from anonymous generator")
			}
			return nil
		}); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = os.MkdirAll(filepath.Join(root, "子目录"), 0700); e != nil {
		return e
	}
	for name, data := range files {
		path := filepath.Join(root, name)
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if os.IsExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		_, e = f.Write(data)
		closeError := f.Close()
		if e != nil {
			return e
		}
		if closeError != nil {
			return closeError
		}
	}
	return nil
}
