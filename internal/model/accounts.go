package model

import "errors"

var ErrSharedSourcePermission = errors.New("shared_source_requires_admin")

var ErrForbidden = errors.New("forbidden")
var ErrPasswordChangeRequired = errors.New("password_change_required")
var ErrLastAdministrator = errors.New("last_administrator")
var ErrAccountConflict = errors.New("account_revision_conflict")

type AccountView struct {
	ID                 string            `json:"id"`
	Username           string            `json:"username"`
	Role               string            `json:"role"`
	Enabled            bool              `json:"enabled"`
	MustChangePassword bool              `json:"must_change_password"`
	Revision           int64             `json:"revision"`
	ACLRevision        int64             `json:"acl_revision"`
	Grants             map[string]string `json:"library_grants"`
}
type AccountUpdate struct {
	Expected int64             `json:"expected_revision"`
	Role     string            `json:"role"`
	Enabled  bool              `json:"enabled"`
	Grants   map[string]string `json:"library_grants"`
}
