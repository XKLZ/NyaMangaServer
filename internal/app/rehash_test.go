package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/library"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestOfflineRehashUpgradeLockRestartAndOfflineCover(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	data := t.TempDir()
	path := filepath.Join(root, "sample.cbz")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	var first, last []byte
	for i := 1; i <= 8; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		img.Set(0, 0, color.RGBA{R: uint8(i), A: 255})
		var b bytes.Buffer
		png.Encode(&b, img)
		if i == 1 {
			first = append([]byte{}, b.Bytes()...)
		}
		if i == 8 {
			last = append([]byte{}, b.Bytes()...)
		}
		w, e := z.Create(fmt.Sprintf("%02d.png", i))
		if e != nil {
			t.Fatal(e)
		}
		w.Write(b.Bytes())
	}
	z.Close()
	f.Close()
	info, _ := os.Stat(path)
	c := config.Config{Listen: "127.0.0.1:18080", PublicURL: "http://localhost:5173", DataDir: data, SetupTokenFile: filepath.Join(data, "setup"), Roots: []config.Root{{Key: "anonymous", Name: "Anonymous", Path: root}}}
	files := fstest.MapFS{}
	for _, name := range []string{"001_foundation.sql", "002_metadata.sql", "003_organization.sql", "004_administration.sql", "005_properties_dictionary.sql"} {
		raw, _ := migrations.Files.ReadFile(name)
		files[name] = &fstest.MapFile{Data: raw}
	}
	db, e := store.Open(ctx, data, files)
	if e != nil {
		t.Fatal(e)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := library.New(db, c, logger)
	l, e := service.SaveLibrary(ctx, model.Library{RootKey: "anonymous", Name: "Anonymous", Encoding: "utf8"}, 0)
	if e != nil {
		t.Fatal(e)
	}
	hash := fmt.Sprintf("%x", sha1.Sum(first))
	_, e = db.Write.Exec(`INSERT INTO books(id,library_id,relative_path,filename_title,size,mtime_ns,content_revision,representative_sha1,page_count,file_state,indexed_at,revision) VALUES('book',?,'sample.cbz','Anonymous',?,?,1,?,8,'available',1,7),('missing',?,'missing.cbz','Offline',1,1,1,'old',1,'missing',1,3);INSERT INTO field_overrides VALUES('book','title','value','"Keep manual title"')`, l.ID, info.Size(), info.ModTime().UnixNano(), hash, l.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Initialize(ctx, model.Account{ID: "a", PasswordHash: "anonymous-hash", Revision: 1}, model.Session{Hash: make([]byte, 32), AccountID: "a", Created: 1, Expires: 99}); e != nil {
		t.Fatal(e)
	}
	oldKey := sha256.Sum256([]byte(fmt.Sprintf("book:1:page=0:sha1=%s:320x480:nearest:png:v1", hash)))
	coverPath := filepath.Join(data, "cache", "covers", "v1", fmt.Sprintf("%x", oldKey[:1]), fmt.Sprintf("%x.png", oldKey))
	os.MkdirAll(filepath.Dir(coverPath), 0700)
	os.WriteFile(coverPath, first, 0600)
	service.Close()
	db.Close()
	configPath := filepath.Join(t.TempDir(), "config.json")
	raw, _ := json.Marshal(c)
	os.WriteFile(configPath, raw, 0600)
	report := filepath.Join(data, "tmp", "report.json")
	oldArgs := os.Args
	os.Args = []string{"dev-rehash", "--config", configPath, "--report", report}
	defer func() { os.Args = oldArgs }()
	lock, e := appdata.Acquire(data)
	if e != nil {
		t.Fatal(e)
	}
	if e = RunRehash(); e == nil {
		t.Fatal("running instance lock ignored")
	}
	lock.Close()
	if e = RunRehash(); e != nil {
		t.Fatal(e)
	}
	inspect := func() map[string]any {
		raw, e := os.ReadFile(report)
		if e != nil {
			t.Fatal(e)
		}
		var out map[string]any
		json.Unmarshal(raw, &out)
		return out
	}
	if out := inspect(); out["updated"] != float64(1) || len(out["failed"].([]any)) != 1 {
		t.Fatal(out)
	}
	if e = RunRehash(); e != nil {
		t.Fatal(e)
	}
	if out := inspect(); out["updated"] != float64(0) {
		t.Fatal("already rehashed book repeated", out)
	}
	db, e = store.Open(ctx, data, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	b, e := db.Edition(ctx, "book")
	if e != nil || b.SHA1 == nil || *b.SHA1 != fmt.Sprintf("%x", sha1.Sum(last)) || b.Revision != 7 || b.ContentRevision != 1 {
		t.Fatal(b, e)
	}
	var value string
	if e = db.Read.QueryRow("SELECT value_json FROM field_overrides WHERE edition_id='book' AND field='title'").Scan(&value); e != nil || value != `"Keep manual title"` {
		t.Fatal(value, e)
	}
	var n int
	db.Read.QueryRow("SELECT count(*) FROM accounts").Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	db.Read.QueryRow("SELECT count(*) FROM sessions").Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	service = library.New(db, c, logger)
	defer service.Close()
	os.Rename(root, root+"-offline")
	defer os.Rename(root+"-offline", root)
	cover, kind, e := service.Image(ctx, "book", 1, 1, true)
	if e != nil || kind != "image/png" || !bytes.Equal(cover, first) {
		t.Fatal("old cover lost on rehash", kind, e)
	}
	reportInfo, _ := os.Stat(report)
	if reportInfo.Mode().Perm() != 0600 {
		t.Fatal("report not private")
	}
}
