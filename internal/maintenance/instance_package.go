package maintenance

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const InstanceFormat = 1
const instanceMaxManifest = 128 << 20

type InstanceBuild struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

func (b InstanceBuild) valid() bool {
	return b.Version != "" && b.Commit != "" && b.Commit != "unknown" && !strings.HasSuffix(b.Commit, "-dirty")
}

type InstanceManifest struct {
	Format      int                     `json:"format_version"`
	Application string                  `json:"application"`
	Baseline    string                  `json:"baseline"`
	Build       InstanceBuild           `json:"build"`
	Created     int64                   `json:"created_at"`
	Inventory   model.InstanceInventory `json:"inventory"`
	Files       []model.InstanceFile    `json:"files"`
	Secrets     bool                    `json:"contains_credentials_and_password_hashes"`
}
type InstanceBudget struct {
	ArchiveBytes, ExpandedBytes, FileBytes int64
	Files                                  int
}

func DefaultInstanceBudget() InstanceBudget {
	return InstanceBudget{256 << 30, 1 << 40, 128 << 30, 1000000}
}

func instanceKey(key string) bool {
	if !fs.ValidPath(key) || strings.ContainsAny(key, "\\:\x00") || len(key) > 1024 {
		return false
	}
	if key == "app.sqlite" || key == "vault/key" || key == "vault/credentials" {
		return true
	}
	for _, prefix := range []string{"assets/", "cache/", "indexes/", "health/"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
func instancePrivateDir(dir string) error {
	if e := appdata.NoSymlinks(dir); e != nil {
		return e
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	return os.Chmod(dir, 0700)
}
func instanceSyncDir(dir string) error {
	f, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func instanceJSON(path string, v any) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if e = appdata.NoSymlinks(path); e != nil {
		return e
	}
	if e = instancePrivateDir(filepath.Dir(path)); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), "pending-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(raw); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	return instanceSyncDir(filepath.Dir(path))
}

type instanceReader struct {
	ctx context.Context
	r   io.Reader
}

func (r instanceReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}

func instanceCopy(ctx context.Context, from, to string, limit int64) (model.InstanceFile, error) {
	v := model.InstanceFile{}
	if e := appdata.NoSymlinks(from); e != nil {
		return v, e
	}
	source, e := os.Open(from)
	if e != nil {
		return v, e
	}
	defer source.Close()
	info, e := source.Stat()
	if e != nil {
		return v, e
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return v, model.ErrInstancePackage
	}
	// Existing explicitly configured secret parents retain their permissions.
	// New staging descendants are private; copied files are always 0600.
	if e = appdata.NoSymlinks(filepath.Dir(to)); e != nil {
		return v, e
	}
	if e = os.MkdirAll(filepath.Dir(to), 0700); e != nil {
		return v, e
	}
	if e = appdata.NoSymlinks(to); e != nil {
		return v, e
	}
	dest, e := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return v, e
	}
	good := false
	defer func() {
		dest.Close()
		if !good {
			os.Remove(to)
		}
	}()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(dest, h), instanceReader{ctx, io.LimitReader(source, limit+1)})
	if e != nil {
		return v, e
	}
	if n != info.Size() {
		return v, model.ErrInstancePackage
	}
	if e = dest.Sync(); e != nil {
		return v, e
	}
	if e = dest.Close(); e != nil {
		return v, e
	}
	good = true
	v.Size = n
	v.SHA256 = hex.EncodeToString(h.Sum(nil))
	return v, instanceSyncDir(filepath.Dir(to))
}
func instanceHash(ctx context.Context, path string) (string, int64, error) {
	return instanceHashWithReader(ctx, path, nil)
}
func instanceHashWithReader(ctx context.Context, path string, wrap func(io.Reader) io.Reader) (string, int64, error) {
	if e := appdata.NoSymlinks(path); e != nil {
		return "", 0, e
	}
	f, e := os.Open(path)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil {
		return "", 0, e
	}
	if !i.Mode().IsRegular() {
		return "", 0, model.ErrInstancePackage
	}
	h := sha256.New()
	var reader io.Reader = f
	if wrap != nil {
		reader = wrap(reader)
	}
	n, e := io.Copy(h, instanceReader{ctx, reader})
	return hex.EncodeToString(h.Sum(nil)), n, e
}
func validateInstanceMembers(m InstanceManifest, b InstanceBudget) error {
	if m.Format != InstanceFormat || m.Application != "nyamangaserver" || m.Baseline != "foundation-v1" || !m.Secrets || !m.Build.valid() || len(m.Files) == 0 || len(m.Files) > b.Files {
		return model.ErrInstancePackage
	}
	keys := map[string]bool{}
	total := int64(0)
	for _, f := range m.Files {
		raw, e := hex.DecodeString(f.SHA256)
		if !instanceKey(f.Key) || keys[f.Key] || f.Size < 0 || f.Size > b.FileBytes || e != nil || len(raw) != 32 {
			return model.ErrInstancePackage
		}
		keys[f.Key] = true
		total += f.Size
		if total > b.ExpandedBytes {
			return model.ErrInstancePackage
		}
	}
	if !keys["app.sqlite"] || keys["vault/key"] != keys["vault/credentials"] {
		return model.ErrInstancePackage
	}
	for _, f := range m.Files {
		for parent := filepath.ToSlash(filepath.Dir(f.Key)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			if keys[parent] {
				return model.ErrInstancePackage
			}
		}
	}
	return nil
}

// archive/zip writes ZIP64 automatically when offsets/sizes/counts require it.
// Staging is immutable and outside the live barrier before compression starts.
func writeInstancePackage(ctx context.Context, stage, path string, m InstanceManifest, b InstanceBudget) error {
	if e := validateInstanceMembers(m, b); e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	good := false
	defer func() {
		f.Close()
		if !good {
			os.Remove(path)
		}
	}()
	z := zip.NewWriter(f)
	raw, e := json.Marshal(m)
	if e != nil {
		return e
	}
	if len(raw) > instanceMaxManifest {
		return model.ErrInstancePackage
	}
	w, e := z.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Deflate})
	if e != nil {
		return e
	}
	if _, e = w.Write(raw); e != nil {
		return e
	}
	for _, member := range m.Files {
		header := &zip.FileHeader{Name: member.Key, Method: zip.Deflate}
		header.SetMode(0600)
		w, e = z.CreateHeader(header)
		if e != nil {
			return e
		}
		source, e := os.Open(filepath.Join(stage, filepath.FromSlash(member.Key)))
		if e != nil {
			return e
		}
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(w, h), instanceReader{ctx, io.LimitReader(source, member.Size+1)})
		closeErr := source.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		if n != member.Size || hex.EncodeToString(h.Sum(nil)) != member.SHA256 {
			return model.ErrInstancePackage
		}
		if pos, e := f.Seek(0, io.SeekCurrent); e != nil {
			return e
		} else if pos > b.ArchiveBytes {
			return model.ErrInstancePackage
		}
	}
	if e = z.Close(); e != nil {
		return e
	}
	i, e := f.Stat()
	if e != nil {
		return e
	}
	if i.Size() > b.ArchiveBytes {
		return model.ErrInstancePackage
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	good = true
	return instanceSyncDir(filepath.Dir(path))
}

// Validate central directory and manifest before creating any extracted object.
// Expanded and per-file limits apply to bytes actually read, including CRC check.
func readInstancePackage(ctx context.Context, path, stage string, build InstanceBuild, b InstanceBudget, probes ...func(string) (uint64, error)) (InstanceManifest, error) {
	var m InstanceManifest
	if !build.valid() {
		return m, model.ErrInstanceBuild
	}
	if e := appdata.NoSymlinks(path); e != nil {
		return m, e
	}
	i, e := os.Lstat(path)
	if e != nil {
		return m, e
	}
	if !i.Mode().IsRegular() || i.Size() > b.ArchiveBytes {
		return m, model.ErrInstancePackage
	}
	z, e := zip.OpenReader(path)
	if e != nil {
		return m, model.ErrInstancePackage
	}
	defer z.Close()
	if len(z.File) < 2 || len(z.File) > b.Files+1 {
		return m, model.ErrInstancePackage
	}
	members := map[string]*zip.File{}
	for _, f := range z.File {
		if f.Name != "manifest.json" && !instanceKey(f.Name) || members[f.Name] != nil || !f.Mode().IsRegular() || f.Flags&1 != 0 || f.UncompressedSize64 > uint64(b.FileBytes) || f.Method != zip.Store && f.Method != zip.Deflate {
			return m, model.ErrInstancePackage
		}
		members[f.Name] = f
	}
	manifest := members["manifest.json"]
	if manifest == nil || manifest.UncompressedSize64 > instanceMaxManifest {
		return m, model.ErrInstancePackage
	}
	r, e := manifest.Open()
	if e != nil {
		return m, model.ErrInstancePackage
	}
	raw, e := io.ReadAll(io.LimitReader(instanceReader{ctx, r}, instanceMaxManifest+1))
	ce := r.Close()
	if e != nil || ce != nil || len(raw) > instanceMaxManifest {
		return m, model.ErrInstancePackage
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF {
		return m, model.ErrInstancePackage
	}
	if e = validateInstanceMembers(m, b); e != nil {
		return m, e
	}
	if m.Build != build {
		return m, model.ErrInstanceBuild
	}
	if len(m.Files)+1 != len(z.File) {
		return m, model.ErrInstancePackage
	}
	for _, f := range m.Files {
		a := members[f.Key]
		if a == nil || a.UncompressedSize64 != uint64(f.Size) {
			return m, model.ErrInstancePackage
		}
	}
	probe := instanceFreeSpace
	if len(probes) > 0 && probes[0] != nil {
		probe = probes[0]
	}
	available, e := probe(filepath.Dir(stage))
	if e != nil {
		return m, e
	}
	var expanded int64
	for _, f := range m.Files {
		expanded += f.Size
	}
	if uint64(expanded+32<<20) > available {
		return m, syscall.ENOSPC
	}
	if e = instancePrivateDir(stage); e != nil {
		return m, e
	}
	for _, f := range m.Files {
		if e = ctx.Err(); e != nil {
			return m, e
		}
		filename := filepath.Join(stage, filepath.FromSlash(f.Key))
		if e = instancePrivateDir(filepath.Dir(filename)); e != nil {
			return m, e
		}
		if e = appdata.NoSymlinks(filename); e != nil {
			return m, e
		}
		out, e := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return m, e
		}
		in, e := members[f.Key].Open()
		if e != nil {
			out.Close()
			return m, model.ErrInstancePackage
		}
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(out, h), instanceReader{ctx, io.LimitReader(in, f.Size+1)})
		ce := in.Close()
		syncErr := out.Sync()
		closeErr := out.Close()
		if e != nil || ce != nil || n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return m, model.ErrInstancePackage
		}
		if syncErr != nil || closeErr != nil {
			return m, errors.Join(syncErr, closeErr)
		}
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Key < m.Files[j].Key })
	return m, instanceSyncDir(stage)
}
