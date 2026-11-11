package httpapi

import (
	"net/http"
	"nyamangaserver.local/server/internal/model"
)

func actorResponse(a *model.Account) any {
	if a == nil {
		return nil
	}
	return map[string]any{"id": a.ID, "username": a.Username, "role": a.Role, "enabled": a.Enabled, "must_change_password": a.MustChangePassword, "acl_revision": a.ACLRevision, "library_grants": a.LibraryGrants, "capabilities": map[string]bool{"manage_accounts": a.Role == "admin" && !a.MustChangePassword, "global_maintenance": a.Role == "admin" && !a.MustChangePassword, "manage_global_tags": a.Role == "admin" && !a.MustChangePassword, "personal_state": !a.MustChangePassword}}
}
func (h *Handler) accountRoutes(p *http.ServeMux) {
	p.HandleFunc("/api/v1/accounts", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			v, e := h.svc.Accounts(r.Context())
			if e != nil {
				h.failure(w, r, e)
				return
			}
			h.json(w, 200, map[string]any{"items": v})
		case "POST":
			var in struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if !h.decode(w, r, &in) {
				return
			}
			v, e := h.svc.CreateAccount(r.Context(), in.Username, in.Password)
			if e != nil {
				h.failure(w, r, e)
				return
			}
			h.json(w, 201, v)
		default:
			h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		}
	})
	p.HandleFunc("/api/v1/accounts/{id}", h.method("PUT", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Expected int64             `json:"expected_revision"`
			Role     string            `json:"role"`
			Enabled  bool              `json:"enabled"`
			Grants   map[string]string `json:"library_grants"`
			Password *string           `json:"new_password,omitempty"`
		}
		if !h.decode(w, r, &in) {
			return
		}
		e := h.svc.UpdateAccount(r.Context(), r.PathValue("id"), model.AccountUpdate{Expected: in.Expected, Role: in.Role, Enabled: in.Enabled, Grants: in.Grants}, in.Password)
		if e != nil {
			h.failure(w, r, e)
			return
		}
		w.WriteHeader(204)
	}))
}
