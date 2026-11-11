package maintenance

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"nyamangaserver.local/server/internal/model"
)

type instanceUploadFaultRepo struct {
	InstanceRepository
	afterCreate   func(model.InstanceOperation)
	finalWrite    func(context.Context, string, int64, string, string, string, int64, string, string) (model.InstanceOperation, error)
	creationError error
	settleProbe   func(context.Context)
}

func (r instanceUploadFaultRepo) CreateInstanceOperation(ctx context.Context, id, kind, key string) (model.InstanceOperation, bool, error) {
	v, created, e := r.InstanceRepository.CreateInstanceOperation(ctx, id, kind, key)
	if created && e == nil && r.afterCreate != nil {
		r.afterCreate(v)
	}
	if created && e == nil && r.creationError != nil {
		return model.InstanceOperation{}, true, r.creationError
	}
	return v, created, e
}

func (r instanceUploadFaultRepo) FailPendingInstanceUpload(ctx context.Context, id string, revision, size int64, code string) (model.InstanceOperation, error) {
	if r.settleProbe != nil {
		r.settleProbe(ctx)
	}
	return r.InstanceRepository.FailPendingInstanceUpload(ctx, id, revision, size, code)
}
func (r instanceUploadFaultRepo) SetInstanceOperation(ctx context.Context, id string, revision int64, state, phase, private string, size int64, digest, code string) (model.InstanceOperation, error) {
	if state == "uploaded" && r.finalWrite != nil {
		return r.finalWrite(ctx, id, revision, state, phase, private, size, digest, code)
	}
	return r.InstanceRepository.SetInstanceOperation(ctx, id, revision, state, phase, private, size, digest, code)
}

type instanceUploadErrorReader struct{ err error }

func (r instanceUploadErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestInstanceUploadFilesystemFailuresSettlePending(t *testing.T) {
	for _, kind := range []string{"directory", "partial", "rename", "enospc", "permission"} {
		t.Run(kind, func(t *testing.T) {
			s, db, c := instanceFixture(t)
			repo := instanceUploadFaultRepo{InstanceRepository: s.repo, afterCreate: func(v model.InstanceOperation) {
				root := filepath.Join(c.DataDir, "instance-recovery", v.ID)
				switch kind {
				case "directory":
					if e := os.WriteFile(filepath.Dir(root), []byte("anonymous non-directory blocker"), 0600); e != nil {
						t.Fatal(e)
					}
				case "partial":
					if e := instancePrivateDir(root); e != nil {
						t.Fatal(e)
					}
					if e := os.WriteFile(filepath.Join(root, "upload.partial"), []byte("retained conflicting partial"), 0600); e != nil {
						t.Fatal(e)
					}
				case "rename":
					if e := instancePrivateDir(filepath.Join(root, "upload.zip")); e != nil {
						t.Fatal(e)
					}
				}
			}}
			s.repo = repo
			var reader io.Reader = strings.NewReader("anonymous staged upload")
			want := "instance_operation_failed"
			if kind == "enospc" {
				reader = instanceUploadErrorReader{syscall.ENOSPC}
				want = "storage_full"
			}
			if kind == "permission" {
				reader = instanceUploadErrorReader{os.ErrPermission}
				want = "permission_denied"
			}
			key := "anonymous-upload-failure-" + kind
			returned, e := s.UploadRestore(context.Background(), key, reader)
			if e == nil {
				t.Fatal("injected upload failure was ignored")
			}
			items, queryErr := db.InstanceOperations(context.Background(), "restore", 1, 50, key)
			if queryErr != nil || len(items) != 1 {
				t.Fatalf("identity lost: %+v %v", items, queryErr)
			}
			got := items[0]
			if got.State != "failed" || got.Phase != "upload" || got.Error != want || got.Revision != 2 {
				t.Fatalf("failure not durably classified: %+v", got)
			}
			if returned.ID != got.ID {
				t.Fatal("failed upload lost its request identity")
			}
			if kind == "partial" {
				raw, e := os.ReadFile(filepath.Join(c.DataDir, "instance-recovery", got.ID, "upload.partial"))
				if e != nil || string(raw) != "retained conflicting partial" {
					t.Fatal("foreign partial was removed")
				}
			}
		})
	}
}

func TestInstanceUploadFinalResultAndCancellationDoNotDemoteNewStates(t *testing.T) {
	for _, mode := range []string{"cancel_before_sql", "committed_response_lost", "committed_then_cancelled", "creation_response_cancelled"} {
		t.Run(mode, func(t *testing.T) {
			s, db, _ := instanceFixture(t)
			inner := s.repo
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			settled := false
			repo := instanceUploadFaultRepo{InstanceRepository: inner, settleProbe: func(ctx context.Context) {
				settled = true
				deadline, ok := ctx.Deadline()
				if ctx.Err() != nil || !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 3*time.Second {
					t.Error("settlement did not use an independent bounded context")
				}
			}}
			wantErr := context.Canceled
			if mode == "creation_response_cancelled" {
				repo.creationError = context.Canceled
				repo.afterCreate = func(model.InstanceOperation) { cancel() }
			}
			repo.finalWrite = func(writeCtx context.Context, id string, revision int64, state, phase, private string, size int64, digest, code string) (model.InstanceOperation, error) {
				if mode == "cancel_before_sql" {
					cancel()
					return inner.SetInstanceOperation(writeCtx, id, revision, state, phase, private, size, digest, code)
				}
				if _, e := inner.SetInstanceOperation(writeCtx, id, revision, state, phase, private, size, digest, code); e != nil {
					return model.InstanceOperation{}, e
				}
				if mode == "committed_then_cancelled" {
					cancel()
					return model.InstanceOperation{}, context.Canceled
				}
				return model.InstanceOperation{}, context.DeadlineExceeded
			}
			if mode == "committed_response_lost" {
				wantErr = context.DeadlineExceeded
			}
			s.repo = repo
			key := "anonymous-final-upload-" + mode
			returned, e := s.UploadRestore(ctx, key, strings.NewReader("anonymous durable upload"))
			if !errors.Is(e, wantErr) || !settled {
				t.Fatalf("failure/settlement missing for %s", mode)
			}
			items, e := db.InstanceOperations(context.Background(), "restore", 1, 50, key)
			if e != nil || len(items) != 1 || returned.ID == "" || returned.ID != items[0].ID {
				t.Fatal("request identity was lost")
			}
			got := items[0]
			if strings.HasPrefix(mode, "committed_") {
				if got.State != "uploaded" || got.Phase != "uploaded" || got.Error != "" || got.Revision != 2 || got.Digest == "" || returned.State != "uploaded" {
					t.Fatalf("successful SQL was demoted: %+v", got)
				}
			} else if got.State != "failed" || got.Phase != "upload" || got.Error != "cancelled" || got.Revision != 2 {
				t.Fatalf("cancelled pending result not settled: %+v", got)
			}
		})
	}
}

func TestInstanceUploadFailureCannotOverwriteAbandonedOrNewPendingGeneration(t *testing.T) {
	for _, state := range []string{"abandoned", "uploaded", "pending"} {
		t.Run(state, func(t *testing.T) {
			s, db, c := instanceFixture(t)
			inner := s.repo
			s.repo = instanceUploadFaultRepo{InstanceRepository: inner, afterCreate: func(v model.InstanceOperation) {
				if _, e := inner.SetInstanceOperation(context.Background(), v.ID, v.Revision, state, "new_generation", "{}", 17, "retained-digest", "retained-code"); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(filepath.Join(c.DataDir, "instance-recovery"), []byte("anonymous blocker"), 0600); e != nil {
					t.Fatal(e)
				}
			}}
			key := "anonymous-preserve-upload-" + state
			returned, e := s.UploadRestore(context.Background(), key, strings.NewReader("unused upload"))
			if e == nil {
				t.Fatal("filesystem failure missing")
			}
			items, e := db.InstanceOperations(context.Background(), "restore", 1, 50, key)
			if e != nil || len(items) != 1 {
				t.Fatal("identity missing")
			}
			got := items[0]
			if got.State != state || got.Phase != "new_generation" || got.Revision != 2 || got.Bytes != 17 || got.Digest != "retained-digest" || got.Error != "retained-code" || returned.ID != got.ID {
				t.Fatalf("new state overwritten: %+v", got)
			}
		})
	}
}
