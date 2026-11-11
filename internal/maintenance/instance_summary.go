package maintenance

import (
	"context"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
)

type InstanceTargetRoot struct {
	Key  string `json:"root_key"`
	Name string `json:"display_name"`
}
type InstanceRestoreSummary struct {
	Build               InstanceBuild        `json:"build"`
	Schema              int                  `json:"schema_version"`
	Counts              map[string]int64     `json:"object_counts"`
	Roots               []model.InstanceRoot `json:"source_roots"`
	Files               int                  `json:"files"`
	ExpandedBytes       int64                `json:"expanded_bytes"`
	CredentialsIncluded bool                 `json:"contains_credentials_and_password_hashes"`
}
type InstanceRestoreDetail struct {
	Operation     model.InstanceOperation     `json:"operation"`
	Summary       *InstanceRestoreSummary     `json:"summary"`
	TargetRoots   []InstanceTargetRoot        `json:"target_roots"`
	SavedMappings []model.InstanceRootMapping `json:"root_mappings"`
	Preview       *InstanceRestorePreview     `json:"preview"`
}

func (s *InstanceService) RestoreDetail(ctx context.Context, id string) (InstanceRestoreDetail, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return InstanceRestoreDetail{}, e
	}

	out := InstanceRestoreDetail{TargetRoots: []InstanceTargetRoot{}, SavedMappings: []model.InstanceRootMapping{}}
	v, e := s.repo.InstanceOperation(ctx, id)
	if e != nil {
		return out, e
	}
	if v.Kind != "restore" {
		return out, model.ErrNotFound
	}
	out.Operation = v
	for _, root := range s.c.Roots {
		out.TargetRoots = append(out.TargetRoots, InstanceTargetRoot{Key: root.Key, Name: root.Name})
	}
	root, e := s.root(id)
	if e != nil {
		return out, e
	}
	var summary InstanceRestoreSummary
	if e = readInstanceJSON(filepath.Join(root, "summary.json"), &summary, 8<<20); e == nil {
		out.Summary = &summary
	} else if !os.IsNotExist(e) {
		return out, e
	}
	raw, e := s.repo.InstancePrivate(ctx, id)
	if e != nil {
		return out, e
	}
	var private instanceRestorePrivate
	if e = instanceDecodePrivate(raw, &private); e != nil {
		return out, e
	}
	if private.Mappings != nil {
		out.SavedMappings = private.Mappings
	}
	out.Preview = private.Preview
	return out, nil
}
func instanceSummary(m InstanceManifest) InstanceRestoreSummary {
	out := InstanceRestoreSummary{Build: m.Build, Schema: m.Inventory.Version, Counts: m.Inventory.Counts, Roots: m.Inventory.Roots, Files: len(m.Files), CredentialsIncluded: true}
	for _, f := range m.Files {
		out.ExpandedBytes += f.Size
	}
	return out
}
