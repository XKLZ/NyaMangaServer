package maintenance

import (
	"context"
	"nyamangaserver.local/server/internal/config"
	"os"
	"path/filepath"
	"syscall"
)

type InstanceSpace struct {
	RequiredAdditionalBytes int64  `json:"required_additional_bytes"`
	AvailableBytes          uint64 `json:"available_bytes"`
	UploadBytes             int64  `json:"upload_bytes"`
	ValidatedBytes          int64  `json:"validated_bytes"`
	PreparedBytes           int64  `json:"prepared_bytes"`
	TargetPreservationBytes int64  `json:"target_preservation_bytes"`
	TemporaryInstallBytes   int64  `json:"temporary_install_bytes"`
	SafetyBytes             int64  `json:"safety_bytes"`
}

func instanceFreeSpace(path string) (uint64, error) {
	for {
		if _, e := os.Stat(path); os.IsNotExist(e) {
			parent := filepath.Dir(path)
			if parent == path {
				return 0, e
			}
			path = parent
			continue
		} else if e != nil {
			return 0, e
		}
		break
	}
	var stat syscall.Statfs_t
	if e := syscall.Statfs(path, &stat); e != nil {
		return 0, e
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
func (s *InstanceService) space(ctx context.Context, m InstanceManifest, archiveBytes int64) (InstanceSpace, error) {
	return instanceRestoreSpace(ctx, s.c, m, archiveBytes, s.SpaceProbe)
}
func instanceRestoreSpace(ctx context.Context, c config.Config, m InstanceManifest, archiveBytes int64, probe func(string) (uint64, error)) (InstanceSpace, error) {
	out := InstanceSpace{UploadBytes: archiveBytes, SafetyBytes: 64 << 20}
	if probe == nil {
		probe = instanceFreeSpace
	}
	keys, e := instanceTargetMembers(ctx, c, true, DefaultInstanceBudget())
	if e != nil {
		return out, e
	}
	var target, maxFile int64
	for _, key := range keys {
		path, e := instanceTargetPath(c, key)
		if e != nil {
			return out, e
		}
		i, e := os.Lstat(path)
		if e != nil {
			return out, e
		}
		target += i.Size()
		maxFile = max(maxFile, i.Size())
	}
	for _, f := range m.Files {
		out.ValidatedBytes += f.Size
		maxFile = max(maxFile, f.Size)
	}
	out.PreparedBytes = out.ValidatedBytes
	out.TargetPreservationBytes = target * 2
	out.TemporaryInstallBytes = maxFile
	out.RequiredAdditionalBytes = out.PreparedBytes + out.TargetPreservationBytes + out.TemporaryInstallBytes + out.SafetyBytes
	out.AvailableBytes, e = probe(c.DataDir)
	if e != nil {
		return out, e
	}
	if uint64(out.RequiredAdditionalBytes) > out.AvailableBytes {
		return out, syscall.ENOSPC
	}
	// Secrets may be on separate mounted writable filesystems. They are small,
	// but each explicit parent must still have room for a same-filesystem install.
	for _, path := range []string{c.ProviderKeyFile, c.ProviderCredentialsFile} {
		if path != "" {
			free, e := probe(filepath.Dir(path))
			if e != nil {
				return out, e
			}
			if free < 128<<10 {
				return out, syscall.ENOSPC
			}
		}
	}
	return out, nil
}
