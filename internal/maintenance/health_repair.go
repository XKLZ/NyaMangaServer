package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type healthFileReceipt struct {
	Key     string `json:"key"`
	Before  string `json:"before"`
	Existed bool   `json:"existed"`
	After   string `json:"after"`
}
type healthStepReceipt struct {
	Snapshot string              `json:"snapshot"`
	AfterDB  string              `json:"after_db"`
	State    string              `json:"state"`
	Files    []healthFileReceipt `json:"files"`
}
type healthJournal struct {
	Version      int                 `json:"version"`
	InitialReady bool                `json:"initial_ready"`
	RepairID     string              `json:"repair_id"`
	Phase        string              `json:"phase"`
	Steps        []healthStepReceipt `json:"steps"`
}

func (h *HealthService) Plan(ctx context.Context, in model.HealthPlanInput) (model.HealthPlan, error) {
	if e := model.AuthorizeAdministrator(ctx, h.repo); e != nil {
		return model.HealthPlan{}, e
	}

	if len(in.RequestKey) < 8 || len(in.RequestKey) > 128 || len(in.FindingIDs) < 1 || len(in.FindingIDs) > 1000 {
		return model.HealthPlan{}, model.ErrInput
	}
	seen := map[string]bool{}
	for _, id := range in.FindingIDs {
		if id == "" || seen[id] {
			return model.HealthPlan{}, model.ErrInput
		}
		seen[id] = true
	}
	size, e := h.repo.HealthDatabaseBytes(ctx)
	if e != nil {
		return model.HealthPlan{}, e
	}
	required := size*2 + 16<<20
	for _, id := range in.FindingIDs {
		o, err := h.repo.HealthRepairObject(ctx, model.HealthAction{FindingID: id})
		if err != nil {
			return model.HealthPlan{}, err
		}
		required += 4<<20 + o.ObservedSize*2
		if o.Kind == "dictionary" {
			required += o.ObservedSize * 16
		}
	}
	plan, e := h.repo.CreateHealthPlan(ctx, in, required)
	if e != nil {
		return plan, e
	}
	if e = h.validateTargets(ctx, plan.Actions); e != nil {
		return model.HealthPlan{}, e
	}
	if e = h.validateRepairInputs(ctx, plan.Actions); e != nil {
		return model.HealthPlan{}, e
	}
	return plan, nil
}
func (h *HealthService) Execute(ctx context.Context, id string, in model.HealthExecuteInput) (model.HealthRepair, error) {
	if e := model.AuthorizeAdministrator(ctx, h.repo); e != nil {
		return model.HealthRepair{}, e
	}

	if !in.ConfirmMaintenance || in.ExpectedRevision < 1 || len(in.RequestKey) < 8 || len(in.RequestKey) > 128 {
		return model.HealthRepair{}, model.ErrInput
	}
	prior, err := h.repo.HealthRepairByRequest(ctx, in.RequestKey)
	if err == nil {
		if prior.PlanID != id {
			return model.HealthRepair{}, model.ErrConflict
		}
		return prior, nil
	}
	if err != model.ErrNotFound {
		return model.HealthRepair{}, err
	}
	plan, e := h.repo.HealthPlan(ctx, id)
	if e != nil {
		return model.HealthRepair{}, e
	}
	if e = h.validateTargets(ctx, plan.Actions); e != nil {
		return model.HealthRepair{}, e
	}
	v, e := h.repo.ExecuteHealthPlan(ctx, id, in)
	if e == nil {
		h.Wake()
	}
	return v, e
}
func (h *HealthService) fault(phase string) error {
	if h.BeforeIO != nil {
		return h.BeforeIO(phase)
	}
	return nil
}
func (h *HealthService) privateWrite(path string, raw []byte) error {
	if e := appdata.NoSymlinks(path); e != nil {
		return e
	}
	if e := h.fault("stage"); e != nil {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	if e := appdata.NoSymlinks(path); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), "pending-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return e
	}
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
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func (h *HealthService) saveJournal(root string, v healthJournal) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return h.privateWrite(filepath.Join(root, "journal.json"), raw)
}
func fileDigest(path string) (string, int64, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	hash := sha256.New()
	n, e := io.Copy(hash, f)
	return fmt.Sprintf("%x", hash.Sum(nil)), n, e
}
func (h *HealthService) snapshot(ctx context.Context, id, key string) error {
	if e := h.fault("snapshot"); e != nil {
		return e
	}
	path, e := h.managed(key)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	if e = h.repo.HealthSnapshot(ctx, path); e != nil {
		return e
	}
	digest, n, e := fileDigest(path)
	if e != nil {
		return e
	}
	return h.repo.PinHealthArtifact(ctx, id, key, digest, "database", n)
}
func (h *HealthService) loadJournal(id string) (healthJournal, string, error) {
	path, e := h.managed("health/repairs/" + id + "/journal.json")
	if e != nil {
		return healthJournal{}, "", e
	}
	root := filepath.Dir(path)
	f, e := os.Open(path)
	if e != nil {
		return healthJournal{}, root, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if e != nil {
		return healthJournal{}, root, e
	}
	if len(raw) > 1<<20 {
		return healthJournal{}, root, model.ErrHealthRecovery
	}
	var v healthJournal
	e = json.Unmarshal(raw, &v)
	if v.Version != 1 || v.RepairID != id {
		return v, root, model.ErrHealthRecovery
	}
	return v, root, e
}
func (h *HealthService) runRepair(parent context.Context, j model.Job) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	watch := make(chan struct{})
	go func() {
		defer close(watch)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if h.repo.CheckExecution(ctx, j) != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-watch }()
	r, e := h.repo.HealthRepair(ctx, j.ID)
	if e != nil {
		return
	}
	currentStep := -1
	fail := func(err error) {
		if parent.Err() == nil {
			_ = h.repo.SetHealthRepair(context.WithoutCancel(ctx), j, "paused", healthCode(err), currentStep)
		}
	}

	if h.Drain != nil {
		if e = h.Drain(ctx); e != nil {
			fail(e)
			return
		}
	}
	release, e := h.repo.HealthStorageLease(ctx)
	if e != nil {
		fail(e)
		return
	}
	defer release()
	journal, root, e := h.loadJournal(r.ID)
	if os.IsNotExist(e) {
		plan, err := h.repo.HealthPlan(ctx, r.PlanID)
		if err != nil {
			fail(err)
			return
		}
		if err = h.fault("space"); err != nil {
			fail(err)
			return
		}
		var stat syscall.Statfs_t
		if err = syscall.Statfs(h.c.DataDir, &stat); err != nil {
			fail(err)
			return
		}
		size, err := h.repo.HealthDatabaseBytes(ctx)
		if err != nil {
			fail(err)
			return
		}
		required := plan.RequiredBytes + max(int64(0), size-plan.SnapshotBytes)*2
		if uint64(required) > stat.Bavail*uint64(stat.Bsize) {
			fail(syscall.ENOSPC)
			return
		}
		journal = healthJournal{Version: 1, RepairID: r.ID, Phase: "preserving", Steps: make([]healthStepReceipt, len(r.Steps))}
		if e = h.saveJournal(root, journal); e != nil {
			fail(e)
			return
		}

	} else if e != nil {
		fail(e)
		return
	}
	baseline := "health/repairs/" + r.ID + "/initial.sqlite"
	if !journal.InitialReady {
		if err := h.validateArtifact(ctx, baseline); err != nil {
			for _, step := range journal.Steps {
				if step.State != "" && step.State != "preserved" {
					fail(model.ErrHealthRecovery)
					return
				}
			}
			path, _ := h.managed(baseline)
			if _, err = os.Stat(path); err == nil {
				orphan, err := os.CreateTemp(root, "unregistered-baseline-")
				if err != nil {
					fail(err)
					return
				}
				name := orphan.Name()
				orphan.Close()
				if err = os.Rename(path, name); err != nil {
					fail(err)
					return
				}
			} else if !os.IsNotExist(err) {
				fail(err)
				return
			}
			if err = h.snapshot(ctx, r.ID, baseline); err != nil {
				fail(err)
				return
			}
		}
		journal.InitialReady = true
		if err := h.saveJournal(root, journal); err != nil {
			fail(err)
			return
		}
	}
	if len(journal.Steps) != len(r.Steps) {
		fail(model.ErrHealthRecovery)
		return
	}
	if e = h.validateJournal(ctx, r, journal); e != nil {
		fail(e)
		return
	}
	if r.Operation == "rollback" {
		if e = h.rollback(ctx, j, r, journal, root, &currentStep); e != nil {
			fail(e)
		}
		return
	}
	_ = h.repo.SetHealthRepair(ctx, j, "executing", "", -1)
	for i, step := range r.Steps {
		currentStep = i
		if e = h.repo.CheckExecution(ctx, j); e != nil {
			return
		}
		receipt := &journal.Steps[i]
		if receipt.State == "verified" {
			if err := h.repo.VerifyHealthPostState(ctx, step.Action, receipt.AfterDB); err != nil {
				fail(err)
				return
			}
			if err := h.verifyAction(ctx, step.Action, receipt.Files); err != nil {
				fail(err)
				return
			}
			if step.State != "verified" {
				if err := h.repo.FinishHealthStep(ctx, j, i, "verified", receipt.Snapshot, receipt.AfterDB); err != nil {
					fail(err)
					return
				}
			}
			continue
		}
		_, _, committedAfter, receiptErr := h.repo.HealthStepReceipt(ctx, r.ID, i)
		if receiptErr != nil {
			fail(receiptErr)
			return
		}
		if committedAfter != "" {
			_, key, after, err := h.repo.HealthStepReceipt(ctx, r.ID, i)
			if err != nil {
				fail(err)
				return
			}
			receipt.Snapshot = key
			receipt.AfterDB = after
			if err = h.repo.VerifyHealthPostState(ctx, step.Action, after); err != nil {
				fail(err)
				return
			}
			if err = h.verifyAction(ctx, step.Action, receipt.Files); err != nil {
				fail(err)
				return
			}
			receipt.State = "verified"
			if err = h.saveJournal(root, journal); err != nil {
				fail(err)
				return
			}
			if err = h.repo.FinishHealthStep(ctx, j, i, "verified", key, after); err != nil {
				fail(err)
				return
			}
			continue
		}
		if receipt.State == "applying" && len(receipt.Files) > 0 {
			if h.verifyFiles(receipt.Files) == nil {
				after, err := h.repo.ApplyHealthRepair(ctx, j, r.ID, i, step.Action, receipt.Snapshot)
				if err != nil {
					fail(err)
					return
				}
				receipt.AfterDB = after
				receipt.State = "applied"
				if err = h.saveJournal(root, journal); err != nil {
					fail(err)
					return
				}
				if err = h.verifyAction(ctx, step.Action, receipt.Files); err != nil {
					fail(err)
					return
				}
				receipt.State = "verified"
				if err = h.saveJournal(root, journal); err != nil {
					fail(err)
					return
				}
				if err = h.repo.FinishHealthStep(ctx, j, i, "verified", receipt.Snapshot, after); err != nil {
					fail(err)
					return
				}
				continue
			}
			for _, f := range receipt.Files {
				if !h.fileIsBefore(f) {
					fail(model.ErrHealthStale)
					return
				}
			}
		}

		receipt.Snapshot = baseline
		receipt.State = "preserved"
		if e = h.saveJournal(root, journal); e != nil {
			fail(e)
			return
		}
		a := step.Action
		if receipt.State != "applying" {
			if err := h.validateTargets(ctx, []model.HealthAction{a}); err != nil {
				fail(err)
				return
			}
		}
		switch a.Kind {
		case "cover_cache", "tag_cache":
			var key string
			var data []byte
			if a.Kind == "cover_cache" {
				if h.CoverBytes == nil {
					fail(model.ErrInput)
					return
				}
				key, data, e = h.CoverBytes(ctx, a.ObjectID, a.AllowSources)
			} else {
				if h.TagBytes == nil {
					fail(model.ErrInput)
					return
				}
				parts := strings.Split(a.ObjectID, ":")
				if len(parts) != 2 {
					fail(model.ErrInput)
					return
				}
				key, data, e = h.TagBytes(ctx, parts[0], parts[1])
			}
			if e != nil {
				fail(e)
				return
			}
			o, err := h.repo.HealthRepairObject(ctx, a)
			if err != nil {
				fail(err)
				return
			}
			if key != o.Key {
				fail(model.ErrHealthStale)
				return
			}
			f, err := h.preserveFile(ctx, r.ID, key, i)
			if err != nil {
				fail(err)
				return
			}
			if !f.Existed {
				fail(model.ErrHealthStale)
				return
			}
			digest, size, err := h.repo.HealthArtifact(ctx, f.Before)
			if err != nil {
				fail(err)
				return
			}
			if digest != o.ObservedDigest || size != o.ObservedSize {
				fail(model.ErrHealthStale)
				return
			}
			f.After = fmt.Sprintf("%x", sha256.Sum256(data))
			receipt.Files = []healthFileReceipt{f}
			receipt.State = "applying"
			if e = h.saveJournal(root, journal); e != nil {
				fail(e)
				return
			}
			if e = h.repo.CheckExecution(ctx, j); e != nil {
				return
			}
			target, err := h.managed(key)
			if err != nil {
				fail(err)
				return
			}
			if e = h.repo.HealthFileWrite(ctx, j, func() error { return h.privateWrite(target, data) }); e != nil {
				fail(e)
				return
			}
		case "quarantine_recommendation":
			o, err := h.repo.HealthRepairObject(ctx, a)
			if err != nil {
				fail(err)
				return
			}
			f, err := h.preserveFile(ctx, r.ID, o.Key, i)
			if err != nil {
				fail(err)
				return
			}
			if !f.Existed {
				fail(model.ErrHealthStale)
				return
			}
			digest, size, err := h.repo.HealthArtifact(ctx, f.Before)
			if err != nil {
				fail(err)
				return
			}
			if digest != o.ObservedDigest || size != o.ObservedSize {
				fail(model.ErrHealthStale)
				return
			}
			f.After = "missing"
			receipt.Files = []healthFileReceipt{f}
			receipt.State = "applying"
			if e = h.saveJournal(root, journal); e != nil {
				fail(e)
				return
			}
			target, _ := h.managed(o.Key)
			if e = h.fault("switch"); e != nil {
				fail(e)
				return
			}
			if e = h.repo.HealthFileWrite(ctx, j, func() error {
				err := os.Remove(target)
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}); e != nil {
				fail(e)
				return
			}
		case "dictionary":
			if h.DictionaryBytes == nil {
				fail(model.ErrInput)
				return
			}
			data, err := h.DictionaryBytes(ctx)
			if err != nil {
				fail(err)
				return
			}
			a.Dictionary = data

		}
		if receipt.State != "applying" {
			receipt.State = "applying"
			if e = h.saveJournal(root, journal); e != nil {
				fail(e)
				return
			}
		}
		if e = h.fault("commit"); e != nil {
			fail(e)
			return
		}
		receipt.AfterDB, e = h.repo.ApplyHealthRepair(ctx, j, r.ID, i, a, receipt.Snapshot)
		if e != nil {
			fail(e)
			return
		}
		receipt.State = "applied"
		if e = h.saveJournal(root, journal); e != nil {
			fail(e)
			return
		}
		if e = h.verifyAction(ctx, a, receipt.Files); e != nil {
			fail(e)
			return
		}
		receipt.State = "verified"
		if e = h.saveJournal(root, journal); e != nil {
			fail(e)
			return
		}
		if e = h.repo.FinishHealthStep(ctx, j, i, "verified", receipt.Snapshot, receipt.AfterDB); e != nil {
			fail(e)
			return
		}
	}
	journal.Phase = "completed"
	if e = h.saveJournal(root, journal); e != nil {
		fail(e)
		return
	}
	if e = h.repo.EndHealthMaintenance(ctx, j, false); e != nil {
		fail(e)
	}
}
func (h *HealthService) preserveFile(ctx context.Context, id, key string, ordinal int) (healthFileReceipt, error) {
	f := healthFileReceipt{Key: key, Before: fmt.Sprintf("health/repairs/%s/file-%04d.bin", id, ordinal)}
	path, e := h.managed(key)
	if e != nil {
		return f, e
	}
	raw, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return f, nil
	}
	if e != nil {
		return f, e
	}
	f.Existed = true
	if e = h.privateWrite(filepath.Join(h.c.DataDir, f.Before), raw); e != nil {
		return f, e
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	return f, h.repo.PinHealthArtifact(ctx, id, f.Before, digest, "file", int64(len(raw)))
}
func (h *HealthService) verifyAction(ctx context.Context, a model.HealthAction, files []healthFileReceipt) error {
	if e := h.verifyFiles(files); e != nil {
		return e
	}
	if a.Kind == "dictionary" {
		if e := h.repo.HealthDictionaryValid(ctx); e != nil {
			return e
		}
		data, e := h.DictionaryBytes(ctx)
		if e != nil {
			return e
		}
		ok, e := h.repo.HealthDictionaryMatches(ctx, data)
		if e != nil {
			return e
		}
		if !ok {
			return model.ErrHealthRecovery
		}
		return nil
	}
	kind := ""
	switch a.Kind {
	case "projection", "tag_evidence":
		kind = "edition"
	case "preferred":
		kind = "work"
	case "series_positions":
		kind = "series"
	}
	if kind != "" {
		findings, e := h.repo.HealthRules(ctx, model.HealthObject{Kind: kind, ID: a.ObjectID})
		if e != nil {
			return e
		}
		for _, f := range findings {
			if f.Status == "confirmed" && f.Repair == a.Kind {
				return model.ErrHealthRecovery
			}
		}
	}
	return nil
}
func (h *HealthService) rollback(ctx context.Context, j model.Job, r model.HealthRepair, journal healthJournal, root string, currentStep *int) error {
	for i := len(journal.Steps) - 1; i >= 0; i-- {
		*currentStep = i
		step := &journal.Steps[i]
		if step.State == "" || step.State == "preserved" || step.State == "rolled_back" {
			continue
		}
		a := r.Steps[i].Action
		dbstate, key, after, e := h.repo.HealthStepReceipt(ctx, r.ID, i)
		if e != nil {
			return e
		}
		if step.AfterDB == "" && after != "" {
			step.AfterDB = after
			step.Snapshot = key
		}
		for _, f := range step.Files {
			if !h.fileIsBefore(f) {
				if e = h.verifyFiles([]healthFileReceipt{f}); e != nil {
					return e
				}
			}
		}
		if step.AfterDB != "" && dbstate != "rolled_back" {
			if e = h.validateArtifact(ctx, step.Snapshot); e != nil {
				return e
			}
			if e := h.repo.RollbackHealthStep(ctx, j, i, a, filepath.Join(h.c.DataDir, step.Snapshot), step.AfterDB); e != nil {
				return e
			}
		} else if r.Steps[i].State == "applied" {
			return model.ErrHealthRecovery
		}
		for _, f := range step.Files {
			if h.fileIsBefore(f) {
				continue
			}
			if f.Existed {
				if e := h.validateArtifact(ctx, f.Before); e != nil {
					return e
				}
			}
			path, e := h.managed(f.Key)
			if e != nil {
				return e
			}
			digest, _, err := fileDigest(path)
			if os.IsNotExist(err) {
				digest = "missing"
			} else if err != nil {
				return err
			}
			if digest != f.After {
				return model.ErrHealthStale
			}
			if f.Existed {
				raw, e := os.ReadFile(filepath.Join(h.c.DataDir, f.Before))
				if e != nil {
					return e
				}
				if e = h.repo.HealthFileWrite(ctx, j, func() error { return h.privateWrite(path, raw) }); e != nil {
					return e
				}
			} else {
				if e = h.repo.HealthFileWrite(ctx, j, func() error {
					err := os.Remove(path)
					if os.IsNotExist(err) {
						return nil
					}
					return err
				}); e != nil {
					return e
				}
			}
		}
		step.State = "rolled_back"
		if e := h.saveJournal(root, journal); e != nil {
			return e
		}
	}
	journal.Phase = "rolled_back"
	if e := h.saveJournal(root, journal); e != nil {
		return e
	}
	return h.repo.EndHealthMaintenance(ctx, j, true)
}
func (h *HealthService) Rollback(ctx context.Context, id string, in model.HealthExecuteInput) (model.HealthRepair, error) {
	if e := model.AuthorizeAdministrator(ctx, h.repo); e != nil {
		return model.HealthRepair{}, e
	}

	if !in.ConfirmMaintenance || in.ExpectedRevision < 1 || len(in.RequestKey) < 8 || len(in.RequestKey) > 128 {
		return model.HealthRepair{}, model.ErrInput
	}
	requester, ok := h.repo.(interface {
		RequestHealthRollback(context.Context, string, model.HealthExecuteInput) (model.HealthRepair, error)
	})
	if !ok {
		return model.HealthRepair{}, model.ErrInput
	}
	v, e := requester.RequestHealthRollback(ctx, id, in)
	if e == nil {
		h.Wake()
	}
	return v, e
}

func (h *HealthService) verifyFiles(files []healthFileReceipt) error {
	for _, f := range files {
		path, e := h.managed(f.Key)
		if e != nil {
			return e
		}
		digest, _, e := fileDigest(path)
		if f.After == "missing" && os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if digest != f.After {
			return model.ErrHealthStale
		}
	}
	return nil
}
func (h *HealthService) fileIsBefore(f healthFileReceipt) bool {
	path, e := h.managed(f.Key)
	if e != nil {
		return false
	}
	digest, _, e := fileDigest(path)
	if !f.Existed {
		return os.IsNotExist(e)
	}
	before, _, err := fileDigest(filepath.Join(h.c.DataDir, f.Before))
	return e == nil && err == nil && digest == before
}
func (h *HealthService) validateArtifact(ctx context.Context, key string) error {
	expected, size, e := h.repo.HealthArtifact(ctx, key)
	if e != nil {
		return e
	}
	path, e := h.managed(key)
	if e != nil {
		return e
	}
	got, n, e := fileDigest(path)
	if e != nil {
		return e
	}
	if got != expected || n != size {
		return model.ErrHealthRecovery
	}
	return nil
}

func (h *HealthService) validateTargets(ctx context.Context, actions []model.HealthAction) error {
	for _, a := range actions {
		if a.Kind != "cover_cache" && a.Kind != "tag_cache" && a.Kind != "dictionary" && a.Kind != "quarantine_recommendation" {
			continue
		}
		o, e := h.repo.HealthRepairObject(ctx, a)
		if e != nil {
			return e
		}
		if !healthManagedPrefix(o.Kind, o.Key) {
			return model.ErrInput
		}
		path, e := h.managed(o.Key)
		if e != nil {
			return e
		}
		digest, n, e := fileDigest(path)
		if e != nil {
			return e
		}
		if o.ObservedDigest == "" || digest != o.ObservedDigest || n != o.ObservedSize {
			return model.ErrHealthStale
		}
		if a.Kind == "cover_cache" && !a.AllowSources {
			var extra struct {
				Sources bool `json:"requires_sources"`
			}
			if json.Unmarshal(o.Extra, &extra) != nil || extra.Sources {
				return model.ErrInput
			}
		}

	}
	return nil
}

func (h *HealthService) validateJournal(ctx context.Context, r model.HealthRepair, v healthJournal) error {
	if v.Phase != "preserving" && v.Phase != "completed" && v.Phase != "rolled_back" {
		return model.ErrHealthRecovery
	}
	if v.Phase == "rolled_back" && r.Operation != "rollback" {
		return model.ErrHealthRecovery
	}
	for i, step := range v.Steps {
		switch step.State {
		case "", "preserved", "applying", "applied", "verified", "rolled_back":
		default:
			return model.ErrHealthRecovery
		}
		if step.Snapshot != "" && step.Snapshot != "health/repairs/"+r.ID+"/initial.sqlite" {
			return model.ErrHealthRecovery
		}
		if len(step.Files) > 1 {
			return model.ErrHealthRecovery
		}
		for _, f := range step.Files {
			o, e := h.repo.HealthRepairObject(ctx, r.Steps[i].Action)
			if e != nil {
				return e
			}
			if f.Key != o.Key || !f.Existed {
				return model.ErrHealthRecovery
			}
			kind := ""
			switch r.Steps[i].Action.Kind {
			case "cover_cache":
				kind = "cover_cache"
			case "tag_cache":
				kind = "tag_cache"
			case "quarantine_recommendation":
				kind = "recommendation"
			default:
				return model.ErrHealthRecovery
			}
			if f.Before != fmt.Sprintf("health/repairs/%s/file-%04d.bin", r.ID, i) || !healthManagedPrefix(kind, f.Key) {
				return model.ErrHealthRecovery
			}
		}
	}
	return nil
}

func (h *HealthService) validateRepairInputs(ctx context.Context, actions []model.HealthAction) error {
	for _, a := range actions {
		switch a.Kind {
		case "cover_cache":
			if h.CoverBytes == nil {
				return model.ErrInput
			}
			key, _, e := h.CoverBytes(ctx, a.ObjectID, a.AllowSources)
			if e != nil {
				return e
			}
			o, e := h.repo.HealthRepairObject(ctx, a)
			if e != nil {
				return e
			}
			if key != o.Key {
				return model.ErrHealthStale
			}
		case "tag_cache":
			if h.TagBytes == nil {
				return model.ErrInput
			}
			parts := strings.Split(a.ObjectID, ":")
			if len(parts) != 2 {
				return model.ErrInput
			}
			key, _, e := h.TagBytes(ctx, parts[0], parts[1])
			if e != nil {
				return e
			}
			o, e := h.repo.HealthRepairObject(ctx, a)
			if e != nil {
				return e
			}
			if key != o.Key {
				return model.ErrHealthStale
			}
		case "dictionary":
			if h.DictionaryBytes == nil {
				return model.ErrInput
			}
			if _, e := h.DictionaryBytes(ctx); e != nil {
				return e
			}
		}
	}
	return nil
}
