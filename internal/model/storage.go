package model

import (
	"crypto/sha256"
	"fmt"
)

// Shared cache identities keep cleanup and producers in agreement.
func CoverCacheName(id string, content, parsing int64, page int) string {
	key := fmt.Sprintf("%s:%d:page=%d:320x480:nearest:png:v2", id, content, page)
	if parsing > 1 {
		key += fmt.Sprintf(":parse=%d", parsing)
	}
	return fmt.Sprintf("%x.png", sha256.Sum256([]byte(key)))
}
func SelectedCoverCacheName(id string, content, parsing, selection int64, mode string) string {
	if mode == "auto" {
		return CoverCacheName(id, content, parsing, 0)
	}
	key := fmt.Sprintf("%s:%d:selection=%d:320x480:png:v1", id, content, selection)
	if mode == "page" && parsing > 1 {
		key += fmt.Sprintf(":parse=%d", parsing)
	}
	return fmt.Sprintf("%x.png", sha256.Sum256([]byte(key)))
}
func TagImageCacheName(digest, role string) string {
	spec := "contain:768x768"
	if role == "background" {
		spec = "center-crop:1024x512"
	}
	return fmt.Sprintf("%x.png", sha256.Sum256([]byte(digest+":"+role+":"+spec+":png:v1")))
}

type StorageAsset struct {
	ID, Key, Digest string
	Size            int64
	Referenced      bool
}
type StorageOperation struct {
	Kind, State string
	Revision    int64
}
type StorageFacts struct {
	ActiveTasks     int
	Assets          map[string]StorageAsset
	Caches          map[string]bool
	Operations      map[string]StorageOperation
	Health          map[string]string
	HealthArtifacts map[string]string
	Receipts        map[string]string
	InstanceOwner   string
	HealthOwner     string
	Epoch           int64
}
type StorageMaterial struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	OperationID string `json:"operation_id"`
	State       string `json:"state"`
	Revision    int64  `json:"revision"`
}

// StorageDeletion is an internal command, never an HTTP/file-path DTO. Remove
// rechecks the prehashed object's identity immediately before unlink.
type StorageDeletion struct {
	Kind, Key, AssetID, Digest string
	OnlyRedundant              bool
	Ordinal, Size              int64
	Material                   StorageMaterial
	Remove                     func() (bool, error)
}
