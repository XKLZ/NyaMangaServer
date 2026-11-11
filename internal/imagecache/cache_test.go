package imagecache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCacheLRUBoundsAndRecovery(t *testing.T) {
	c := New(t.TempDir(), 0, 0)
	c.bytes = 6
	c.count = 2
	a, b, d := strings.Repeat("a", 64)+".png", strings.Repeat("b", 64)+".png", strings.Repeat("c", 64)+".png"
	if e := c.Put(a, []byte("aa")); e != nil {
		t.Fatal(e)
	}
	if e := c.Put(b, []byte("bb")); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Get(a); e != nil {
		t.Fatal(e)
	}
	if e := c.Put(d, []byte("cc")); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Get(b); !os.IsNotExist(e) {
		t.Fatal("least recently used file not evicted", e)
	}
	if e := c.Put(a, []byte("aaa")); e != nil {
		t.Fatal(e)
	}
	if c.total != 5 || len(c.entries) != 2 {
		t.Fatal("replacement accounting")
	}
	if e := os.Remove(c.path(a)); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Get(a); !os.IsNotExist(e) || c.total != 2 {
		t.Fatal("external cache removal", e)
	}
	again := New(c.dir, 0, 0)
	if _, e := again.Get(d); e != nil {
		t.Fatal("cache restart", e)
	}
}

func TestShardingRestartQuotaAndInvalidKeys(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, 6, 2)
	a, b := strings.Repeat("a", 64)+".png", strings.Repeat("b", 64)+".png"
	for _, key := range []string{"../escape.png", "", strings.Repeat("g", 64) + ".png"} {
		if e := c.Put(key, []byte("x")); e == nil {
			t.Fatal("unsafe key accepted")
		}
		if _, e := c.Get(key); e == nil {
			t.Fatal("unsafe key read")
		}
	}
	if e := c.Put(a, []byte("aaaa")); e != nil {
		t.Fatal(e)
	}
	if e := c.Put(b, []byte("bb")); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(c.path(a)); e != nil {
		t.Fatal("sharded file absent", e)
	}
	pending := filepath.Join(filepath.Dir(c.path(a)), "pending-interrupted")
	if e := os.WriteFile(pending, []byte("partial"), 0600); e != nil {
		t.Fatal(e)
	}
	again := New(dir, 2, 1)
	if _, e := again.Get(b); e != nil {
		t.Fatal("new quota recovery", e)
	}
	if _, e := again.Get(a); !os.IsNotExist(e) {
		t.Fatal("restart did not enforce quota", e)
	}
	if _, e := os.Stat(pending); !os.IsNotExist(e) {
		t.Fatal("partial write retained", e)
	}
	if e := again.Put(a, []byte("big")); e == nil {
		t.Fatal("oversized item accepted")
	}
}
func TestCleanupConditionalRemovalPreservesReplacement(t *testing.T) {
	dir := t.TempDir()
	cache := New(dir, 1024, 10)
	name := strings.Repeat("a", 64) + ".png"
	if e := cache.Put(name, []byte("old")); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(filepath.Join(dir, "aa", name))
	if e != nil {
		t.Fatal(e)
	}
	if e = cache.Put(name, []byte("new replacement")); e != nil {
		t.Fatal(e)
	}
	if e = cache.RemoveIfUnchanged(name, info.Size(), info.ModTime().UnixNano()); e != nil {
		t.Fatal(e)
	}
	raw, e := cache.Get(name)
	if e != nil || string(raw) != "new replacement" {
		t.Fatal(string(raw), e)
	}
	now, e := os.Stat(filepath.Join(dir, "aa", name))
	if e != nil {
		t.Fatal(e)
	}
	if e = cache.RemoveIfUnchanged(name, now.Size(), now.ModTime().UnixNano()); e != nil {
		t.Fatal(e)
	}
	if _, e = cache.Get(name); !os.IsNotExist(e) {
		t.Fatal("unchanged cleanup did not remove file", e)
	}
}

func TestHealthPeekDoesNotInitializeOrEvict(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, 1, 1)
	name := strings.Repeat("a", 64) + ".png"
	path := filepath.Join(dir, "aa", name)
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, []byte("read-only"), 0600)
	data, e := c.Peek(name)
	if e != nil || string(data) != "read-only" || c.ready {
		t.Fatal(string(data), e, c.ready)
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal("peek evicted file", e)
	}
}
