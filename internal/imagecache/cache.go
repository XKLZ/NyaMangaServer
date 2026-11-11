package imagecache

import (
	"container/heap"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"nyamangaserver.local/server/internal/appdata"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type cacheEntry struct {
	name  string
	size  int64
	used  time.Time
	index int
}
type cacheHeap []*cacheEntry

func (h cacheHeap) Len() int           { return len(h) }
func (h cacheHeap) Less(i, j int) bool { return h[i].used.Before(h[j].used) }
func (h cacheHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *cacheHeap) Push(v any)        { e := v.(*cacheEntry); e.index = len(*h); *h = append(*h, e) }
func (h *cacheHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	e.index = -1
	return e
}

type Cache struct {
	mu      sync.Mutex
	dir     string
	ready   bool
	entries map[string]*cacheEntry
	order   cacheHeap
	total   int64
	bytes   int64
	count   int
}

func New(dir string, maxBytes int64, maxFiles int) *Cache {
	if maxBytes == 0 {
		maxBytes = 2 << 30
	}
	if maxFiles == 0 {
		maxFiles = 10000
	}
	return &Cache{dir: dir, entries: map[string]*cacheEntry{}, bytes: maxBytes, count: maxFiles}
}
func (c *Cache) init() error {
	if c.ready {
		return nil
	}
	c.entries = map[string]*cacheEntry{}
	c.order = nil
	c.total = 0
	if e := appdata.NoSymlinks(c.dir); e != nil {
		return e
	}
	if e := os.MkdirAll(c.dir, 0700); e != nil {
		return e
	}
	e := filepath.WalkDir(c.dir, func(path string, v fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if v.IsDir() {
			return nil
		}
		if !v.Type().IsRegular() {
			return nil
		}
		if strings.HasPrefix(v.Name(), "pending-") {
			return os.Remove(path)
		}
		rel, e := filepath.Rel(c.dir, path)
		if e != nil {
			return e
		}
		if !valid(v.Name()) || rel != filepath.Join(v.Name()[:2], v.Name()) {
			return nil
		}
		info, e := v.Info()
		if e != nil {
			return e
		}
		entry := &cacheEntry{name: v.Name(), size: info.Size(), used: info.ModTime()}
		c.entries[entry.name] = entry
		c.total += entry.size
		heap.Push(&c.order, entry)
		return nil
	})
	if e != nil {
		return e
	}
	if e := c.trim(0, 0); e != nil {
		return e
	}
	c.ready = true
	return nil
}
func (c *Cache) drop(e *cacheEntry) {
	delete(c.entries, e.name)
	c.total -= e.size
	heap.Remove(&c.order, e.index)
}
func (c *Cache) trim(extra int64, additional int) error {
	for c.total+extra > c.bytes || len(c.entries)+additional > c.count {
		if len(c.order) == 0 {
			return errors.New("cache item exceeds capacity")
		}
		entry := c.order[0]
		if e := os.Remove(c.path(entry.name)); e != nil && !os.IsNotExist(e) {
			return e
		}
		c.drop(entry)
	}
	return nil
}
func (c *Cache) Get(name string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !valid(name) {
		return nil, fmt.Errorf("invalid cache key")
	}
	if e := c.init(); e != nil {
		return nil, e
	}
	entry := c.entries[name]
	if entry == nil {
		return nil, os.ErrNotExist
	}
	if e := appdata.NoSymlinks(c.path(name)); e != nil {
		return nil, e
	}
	b, e := os.ReadFile(c.path(name))
	if e != nil {
		if os.IsNotExist(e) {
			c.drop(entry)
		}
		return nil, e
	}
	entry.used = time.Now()
	heap.Fix(&c.order, entry.index)
	_ = os.Chtimes(c.path(name), entry.used, entry.used)
	return b, nil
}
func (c *Cache) Put(name string, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !valid(name) {
		return errors.New("invalid cache key")
	}
	if e := c.init(); e != nil {
		return e
	}
	if int64(len(data)) > c.bytes {
		return errors.New("cache item exceeds capacity")
	}
	if e := os.MkdirAll(filepath.Dir(c.path(name)), 0700); e != nil {
		return e
	}
	if e := appdata.NoSymlinks(filepath.Dir(c.path(name))); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(c.path(name)), "pending-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, e = f.Write(data); e != nil {
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
	old := c.entries[name]
	extra, additional := int64(len(data)), 1
	if old != nil {
		extra -= old.size
		additional = 0
		old.used = time.Now()
		heap.Fix(&c.order, old.index)
	}
	if e = c.trim(extra, additional); e != nil {
		return e
	}
	if e = os.Rename(tmp, c.path(name)); e != nil {
		return e
	}
	if old = c.entries[name]; old != nil {
		c.drop(old)
	}
	entry := &cacheEntry{name: name, size: int64(len(data)), used: time.Now()}
	c.entries[name] = entry
	c.total += entry.size
	heap.Push(&c.order, entry)
	return nil
}

func valid(name string) bool {
	if len(name) != 68 || !strings.HasSuffix(name, ".png") {
		return false
	}
	for _, r := range name[:64] {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
func (c *Cache) path(name string) string { return filepath.Join(c.dir, name[:2], name) }

// Remove serializes invalidation with reads, writes and LRU bookkeeping.
func (c *Cache) Remove(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !valid(name) {
		return errors.New("invalid cache key")
	}
	if e := c.init(); e != nil {
		return e
	}
	if e := appdata.NoSymlinks(c.path(name)); e != nil {
		return e
	}
	e := os.Remove(c.path(name))
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if item := c.entries[name]; item != nil {
		c.drop(item)
	}
	return nil
}
func (c *Cache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.init(); e != nil {
		return e
	}
	for len(c.order) > 0 {
		item := c.order[0]
		if e := os.Remove(c.path(item.name)); e != nil && !os.IsNotExist(e) {
			return e
		}
		c.drop(item)
	}
	return nil
}

// RemoveIfUnchanged makes cleanup conditional on the exact file seen in its
// preview. Stat and unlink share the same lock as Put, so a replacement cannot
// be deleted between the comparison and removal.
func (c *Cache) RemoveIfUnchanged(name string, size, mtime int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !valid(name) {
		return errors.New("invalid cache key")
	}
	if e := c.init(); e != nil {
		return e
	}
	path := c.path(name)
	if e := appdata.NoSymlinks(path); e != nil {
		return e
	}
	info, e := os.Stat(path)
	if os.IsNotExist(e) {
		if item := c.entries[name]; item != nil {
			c.drop(item)
		}
		return nil
	}
	if e != nil {
		return e
	}
	if info.Size() != size || info.ModTime().UnixNano() != mtime {
		return nil
	}
	if e = os.Remove(path); e != nil && !os.IsNotExist(e) {
		return e
	}
	if item := c.entries[name]; item != nil {
		c.drop(item)
	}
	return nil
}

// Peek reads an already materialized image without initialization, LRU updates,
// migration, eviction or publication. Maintenance browsing uses this path.
func (c *Cache) Peek(name string) ([]byte, error) {
	if !valid(name) {
		return nil, fmt.Errorf("invalid cache key")
	}
	path := c.path(name)
	if e := appdata.NoSymlinks(path); e != nil {
		return nil, e
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return nil, fmt.Errorf("cache image limit")
	}
	raw, e := io.ReadAll(io.LimitReader(f, 8<<20+1))
	if e != nil {
		return nil, e
	}
	if len(raw) > 8<<20 {
		return nil, fmt.Errorf("cache image limit")
	}
	return raw, nil
}
