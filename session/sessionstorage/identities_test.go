package sessionstorage

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	gomock "go.uber.org/mock/gomock"
)

// TestAuth_StoreMethods proves each Auth identity method maps the driver's rows to the
// public type and propagates the driver's errors with their class.
func TestAuth_StoreMethods(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	link := &dbtype.SessionIdentity{ID: impulse.Must(impulse.NewUUID()), UserID: userID, Method: sessioninfo.MethodWorkOS, Connection: "conn", Subject: "sub"}
	identity := &sessioninfo.Identity{Method: sessioninfo.MethodWorkOS, Connection: "conn", Subject: "sub"}
	event := sessioninfo.AuthEvent{Method: "email-otp"}
	refused := httpio.NewConflictMessageWithError(ErrLastSignInMethod, "last")
	changed := httpio.NewConflictMessageWithError(ErrPasswordChanged, "changed")
	hasher := securehash.New(securehash.Argon2())
	proven, err := hasher.Hash("proven")
	if err != nil {
		t.Fatal(err)
	}
	chosen, err := hasher.Hash("chosen")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		prepare func(db *Mockdb)
		call    func(ctx context.Context, a *Auth[struct{}]) (any, error)
		want    any
		wantErr error
	}{
		{
			name: "Identity maps the link",
			prepare: func(db *Mockdb) {
				db.EXPECT().Identity(gomock.Any(), sessioninfo.MethodWorkOS, "conn", "sub").Return(link, nil)
			},
			call: func(ctx context.Context, a *Auth[struct{}]) (any, error) {
				return a.Identity(ctx, sessioninfo.MethodWorkOS, "conn", "sub")
			},
			want: (*SessionIdentity)(link),
		},
		{
			name: "IdentitiesByUser maps every link",
			prepare: func(db *Mockdb) {
				db.EXPECT().IdentitiesByUser(gomock.Any(), userID).Return([]*dbtype.SessionIdentity{link}, nil)
			},
			call: func(ctx context.Context, a *Auth[struct{}]) (any, error) { return a.IdentitiesByUser(ctx, userID) },
			want: []*SessionIdentity{(*SessionIdentity)(link)},
		},
		{
			name:    "LinkIdentity maps the new link",
			prepare: func(db *Mockdb) { db.EXPECT().LinkIdentity(gomock.Any(), userID, identity, "tenant").Return(link, nil) },
			call: func(ctx context.Context, a *Auth[struct{}]) (any, error) {
				return a.LinkIdentity(ctx, userID, identity, "tenant")
			},
			want: (*SessionIdentity)(link),
		},
		{
			name:    "UnlinkIdentity propagates the last-means-of-sign-in refusal",
			prepare: func(db *Mockdb) { db.EXPECT().UnlinkIdentity(gomock.Any(), link.ID).Return(refused) },
			call:    func(ctx context.Context, a *Auth[struct{}]) (any, error) { return nil, a.UnlinkIdentity(ctx, link.ID) },
			wantErr: ErrLastSignInMethod,
		},
		{
			name: "RemoveUserPassword hands the driver the identities that stand and propagates the last-means-of-sign-in refusal",
			prepare: func(db *Mockdb) {
				db.EXPECT().RemoveUserPassword(gomock.Any(), userID, []impulse.UUID{link.ID}).Return(refused)
			},
			call: func(ctx context.Context, a *Auth[struct{}]) (any, error) {
				return nil, a.RemoveUserPassword(ctx, userID, []impulse.UUID{link.ID})
			},
			wantErr: ErrLastSignInMethod,
		},
		{
			name: "RemoveProvenUserPassword hands the driver the proven hash and the identities that stand",
			prepare: func(db *Mockdb) {
				db.EXPECT().RemoveProvenUserPassword(gomock.Any(), userID, proven, []impulse.UUID{link.ID}).Return(nil)
			},
			call: func(ctx context.Context, a *Auth[struct{}]) (any, error) {
				return nil, a.RemoveProvenUserPassword(ctx, userID, proven, []impulse.UUID{link.ID})
			},
		},
		{
			name: "RemoveProvenUserPassword propagates the refusal of a password that changed since it was proved",
			prepare: func(db *Mockdb) {
				db.EXPECT().RemoveProvenUserPassword(gomock.Any(), userID, proven, gomock.Nil()).Return(changed)
			},
			call: func(ctx context.Context, a *Auth[struct{}]) (any, error) {
				return nil, a.RemoveProvenUserPassword(ctx, userID, proven, nil)
			},
			wantErr: ErrPasswordChanged,
		},
		{
			name:    "ChangeUserPassword hands the driver the proven hash beside the new one",
			prepare: func(db *Mockdb) { db.EXPECT().ChangeUserPassword(gomock.Any(), userID, proven, chosen).Return(nil) },
			call: func(ctx context.Context, a *Auth[struct{}]) (any, error) {
				return nil, a.ChangeUserPassword(ctx, userID, proven, chosen)
			},
		},
		{
			name:    "ChangeUserPassword propagates the refusal of a password that changed since it was proved",
			prepare: func(db *Mockdb) { db.EXPECT().ChangeUserPassword(gomock.Any(), userID, proven, chosen).Return(changed) },
			call: func(ctx context.Context, a *Auth[struct{}]) (any, error) {
				return nil, a.ChangeUserPassword(ctx, userID, proven, chosen)
			},
			wantErr: ErrPasswordChanged,
		},
		{
			name:    "DestroyUserSessions forwards the user",
			prepare: func(db *Mockdb) { db.EXPECT().DestroyUserSessions(gomock.Any(), userID).Return(nil) },
			call: func(ctx context.Context, a *Auth[struct{}]) (any, error) {
				return nil, a.DestroyUserSessions(ctx, userID)
			},
		},
		{
			name:    "AppendAuthEvent forwards the event",
			prepare: func(db *Mockdb) { db.EXPECT().AppendAuthEvent(gomock.Any(), userID, &event).Return(nil) },
			call: func(ctx context.Context, a *Auth[struct{}]) (any, error) {
				return nil, a.AppendAuthEvent(ctx, userID, &event)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)

			db := NewMockdb(ctrl)
			tt.prepare(db)
			a := &Auth[struct{}]{userStorage: userStorage{sessionStorage: sessionStorage{db: db}}}

			got, err := tt.call(t.Context(), a)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) || !httpio.HasConflict(err) {
					t.Errorf("error = %v, want %v as a Conflict", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if tt.want != nil {
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("result mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// TestAuth_ChangeUserPasswordNeedsAHash proves a change with no new hash never reaches
// the driver, where it would remove the password without the removal's rule.
func TestAuth_ChangeUserPasswordNeedsAHash(t *testing.T) {
	t.Parallel()

	proven, err := securehash.New(securehash.Argon2()).Hash("proven")
	if err != nil {
		t.Fatal(err)
	}
	a := &Auth[struct{}]{userStorage: userStorage{sessionStorage: sessionStorage{db: NewMockdb(gomock.NewController(t))}}}

	if err := a.ChangeUserPassword(t.Context(), impulse.Must(impulse.NewUUID()), proven, nil); err == nil {
		t.Error("ChangeUserPassword() with no new hash error = nil, want a refusal before the driver is called")
	}
}
