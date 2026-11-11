package maintenance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
)

func TestInstanceSourcePathPlatformBoundaries(t *testing.T) {
	for _, path := range []string{"book.cbz", "日本語/Chapter 01.cbz", "folder/..title.zip"} {
		if !validInstanceSourcePath(path) {
			t.Errorf("valid source rejected: %q", path)
		}
	}
	for _, path := range []string{"folder/CHAPTER 01: EXPOSURE.zip", "folder/Re:M@STER.zip", "part: title/book.cbz"} {
		if got, want := validInstanceSourcePath(path), filepath.Separator != '\\'; got != want {
			t.Errorf("colon path %q: got %v want %v", path, got, want)
		}
		if instanceKey("assets/" + path) {
			t.Errorf("portable package member policy weakened: %q", path)
		}
	}
	for _, path := range []string{"", ".", "..", "../outside", "folder/../outside", "folder/./book", "folder//book", "folder/", "/absolute", "//server/share", "C:/book", "c:book", "C:", "folder/C:/book", "folder/c:book", `C:\book`, `\\server\share`, `folder\book`, "bad\x00.cbz", "bad\xff.cbz"} {
		if validInstanceSourcePath(path) {
			t.Errorf("unsafe source accepted: %q", path)
		}
	}
}

func TestInstanceColonSourceMappingAndActivationValidation(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("ordinary colon filenames require POSIX filesystem semantics")
	}
	ctx := context.Background()
	root := t.TempDir()
	c := config.Config{Roots: []config.Root{{Key: "target", Name: "Anonymous", Path: root}}}
	s := NewInstance(nil, c, InstanceBuild{}, nil)
	key := "target"
	in := InstanceRestoreInput{AcknowledgeStatOnly: true, Mappings: []model.InstanceRootMapping{{LibraryID: "library", TargetRootKey: &key}}}
	inventory := model.InstanceInventory{Roots: []model.InstanceRoot{{LibraryID: "library", RootKey: "old"}}}
	for _, path := range []string{"CHAPTER 01: EXAMPLE.zip", "Re:Example.cbz"} {
		filename := filepath.Join(root, path)
		if err := os.WriteFile(filename, []byte("anonymous stat-only fixture"), 0000); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filename)
		if err != nil {
			t.Fatal(err)
		}
		inventory.Sources = append(inventory.Sources, model.InstanceSource{EditionID: path, LibraryID: "library", Path: path, Size: info.Size(), Mtime: info.ModTime().UnixNano(), State: "available", Revision: 4})
	}
	roots, stats, unlinked, err := s.mapRestore(ctx, inventory, in)
	if err != nil || len(stats) != 2 || unlinked != 0 {
		t.Fatalf("colon source mapping: %d %d %v", len(stats), unlinked, err)
	}
	if err = ValidatePreparedInstanceRoots(ctx, c, inventory, roots); err != nil {
		t.Fatal("activation rejected prepared colon sources", err)
	}
	inventory.Sources[0].Size++
	if err = ValidatePreparedInstanceRoots(ctx, c, inventory, roots); !errors.Is(err, model.ErrInstanceMapping) {
		t.Fatal("size mismatch accepted", err)
	}
	inventory.Sources[0].Size--
	filename := filepath.Join(root, inventory.Sources[0].Path)
	if err = os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.mapRestore(ctx, inventory, in); !errors.Is(err, model.ErrInstanceMapping) {
		t.Fatal("missing available source accepted", err)
	}
	if err = os.Symlink(filepath.Join(root, inventory.Sources[1].Path), filename); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.mapRestore(ctx, inventory, in); !errors.Is(err, model.ErrInstanceMapping) {
		t.Fatal("symlink source accepted", err)
	}
}
