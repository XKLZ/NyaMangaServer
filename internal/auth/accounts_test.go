package auth

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func TestAccountsLifecycleCASAndLegacyLogin(t *testing.T) {
	s := service(t)
	ctx := context.Background()
	_, original, e := s.Setup(ctx, "anonymous-setup", "anonymous password")
	if e != nil {
		t.Fatal(e)
	}
	admin := model.WithActor(ctx, *original.Account)
	a, e := s.CreateAccount(admin, "reader", "reader password")
	if e != nil {
		t.Fatal(e)
	}
	if a.Role != "user" || !a.MustChangePassword || len(a.Grants) != 0 {
		t.Fatal(a)
	}
	if _, _, e = s.Login(ctx, "reader password"); !errors.Is(e, ErrCredentials) {
		t.Fatal("legacy login tried another account", e)
	}
	_, session, e := s.LoginUsername(ctx, "reader", "reader password")
	if e != nil || session.Account == nil || !session.Account.MustChangePassword {
		t.Fatal(session, e)
	}
	if e = s.ChangePassword(ctx, a.ID, "reader password", "reader new password"); e != nil {
		t.Fatal(e)
	}
	if v, e := s.store.Session(ctx, session.Hash, s.Now().Unix()); e != nil || v != nil {
		t.Fatal("password change retained session", e)
	}
	_, session, e = s.LoginUsername(ctx, "READER", "reader new password")
	if e != nil || session.Account.MustChangePassword {
		t.Fatal(session, e)
	}
	rows, e := s.Accounts(admin)
	if e != nil || len(rows) != 2 {
		t.Fatal(rows, e)
	}
	for _, v := range rows {
		if v.ID == a.ID {
			a = v
		}
	}
	if e = s.UpdateAccount(admin, a.ID, model.AccountUpdate{Expected: a.Revision, Role: "user", Enabled: false}, nil); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.LoginUsername(ctx, "reader", "reader new password"); !errors.Is(e, ErrCredentials) {
		t.Fatal("disabled login", e)
	}
	if e = s.UpdateAccount(admin, a.ID, model.AccountUpdate{Expected: a.Revision, Role: "user", Enabled: true}, nil); !errors.Is(e, model.ErrAccountConflict) {
		t.Fatal("stale CAS", e)
	}
	if e = s.UpdateAccount(admin, original.AccountID, model.AccountUpdate{Expected: 1, Role: "user", Enabled: true}, nil); !errors.Is(e, model.ErrLastAdministrator) {
		t.Fatal("last admin", e)
	}
	if _, e = s.Accounts(model.WithActor(ctx, *session.Account)); !errors.Is(e, model.ErrForbidden) {
		t.Fatal("ordinary admin call", e)
	}
}
