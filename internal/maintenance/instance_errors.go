package maintenance

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"os"
	"strings"
	"syscall"
)

// Persist a stable public category, never error.Error() (which can contain paths
// or credentials). Order keeps specific wrapped errors ahead of generic IO.
func instanceFailureCode(e error) string {
	switch {
	case e == nil:
		return ""
	case errors.Is(e, model.ErrForbidden), strings.Contains(e.Error(), "forbidden"):
		return "authorization_revoked"
	case errors.Is(e, model.ErrInstanceBuild):
		return "instance_build_mismatch"
	case errors.Is(e, model.ErrInstancePackage):
		return "invalid_instance_package"
	case errors.Is(e, model.ErrInstanceMapping):
		return "instance_mapping_invalid"
	case errors.Is(e, syscall.ENOSPC):
		return "storage_full"
	case errors.Is(e, os.ErrPermission):
		return "permission_denied"
	case errors.Is(e, provider.ErrSecrets):
		return "instance_secrets_unavailable"
	case errors.Is(e, os.ErrNotExist), errors.Is(e, model.ErrNotFound):
		return "instance_material_missing"
	case errors.Is(e, context.Canceled):
		return "cancelled"
	case errors.Is(e, context.DeadlineExceeded):
		return "request_timeout"
	case errors.Is(e, model.ErrHealthMaintenance):
		return "maintenance_active"
	case errors.Is(e, model.ErrInstanceRecovery):
		return "instance_recovery_required"
	case errors.Is(e, model.ErrConflict), errors.Is(e, model.ErrBusy):
		return "revision_conflict"
	case errors.Is(e, model.ErrInput):
		return "invalid_instance_request"
	default:
		return "instance_operation_failed"
	}
}
