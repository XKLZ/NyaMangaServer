package maintenance

import (
	"context"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"os"
	"syscall"
	"testing"
)

func TestInstanceDurableErrorsAreStableAndPrivate(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{model.ErrInstanceBuild, "instance_build_mismatch"}, {model.ErrInstancePackage, "invalid_instance_package"},
		{syscall.ENOSPC, "storage_full"}, {os.ErrPermission, "permission_denied"}, {os.ErrNotExist, "instance_material_missing"},
		{provider.ErrSecrets, "instance_secrets_unavailable"}, {context.Canceled, "cancelled"}, {context.DeadlineExceeded, "request_timeout"},
		{model.ErrInstanceRecovery, "instance_recovery_required"}, {model.ErrHealthMaintenance, "maintenance_active"},
		{errors.New("unknown failure"), "instance_operation_failed"},
	}
	for _, tc := range cases {
		wrapped := fmt.Errorf("private anonymous credential/path must not be exposed: %w", tc.err)
		if got := instanceFailureCode(wrapped); got != tc.code {
			t.Fatalf("category %s expected %s", got, tc.code)
		}
	}
}
