package basesession

import (
	"context"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/mockstore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	gomock "go.uber.org/mock/gomock"
)

func TestBaseSession_ValidateSessionAPI_PrincipalResolver(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.UUIDFromString("92922509-82d2-4bc7-853a-d73b8926a55f"))
	aliceID := impulse.Must(impulse.UUIDFromString("a11ce000-0000-4000-8000-000000000001"))
	editor := accesstypes.RolePrincipal("Editor")

	type customData struct{ RoleID string }

	ordinary := func() *sessioninfo.SessionData {
		return &sessioninfo.SessionData{
			SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "alice", UpdatedAt: time.Now()},
			CustomData:  &customData{RoleID: "Editor"},
		}
	}
	impersonated := func(principal accesstypes.Principal) *sessioninfo.SessionData {
		s := ordinary()
		s.Username = "bob"
		s.Impersonation = &sessioninfo.Impersonation{SessionID: sessionID, Actor: "alice", Principal: principal, ExpiresAt: time.Now().Add(time.Hour)}

		return s
	}
	fromCustomData := func(ctx context.Context) (accesstypes.Principal, error) {
		data, err := sessioninfo.CustomDataFromCtx[*customData](ctx)
		if err != nil {
			return accesstypes.Principal{}, err
		}

		return accesstypes.RolePrincipal(accesstypes.Role(data.RoleID)), nil
	}

	tests := []struct {
		name          string
		session       *sessioninfo.SessionData
		resolver      func(ctx context.Context) (accesstypes.Principal, error)
		wantErr       bool
		wantPrincipal accesstypes.Principal
		wantStored    bool
	}{
		{
			name:          "no resolver: the session user's own principal",
			session:       ordinary(),
			wantPrincipal: accesstypes.UserPrincipal("alice"),
		},
		{
			name: "no resolver: a session that belongs to an account is the account, named by its id",
			session: func() *sessioninfo.SessionData {
				s := ordinary()
				s.UserID = impulse.NullUUIDFromUUID(aliceID)

				return s
			}(),
			wantPrincipal: accesstypes.UserPrincipal(accesstypes.User(aliceID.String())),
		},
		{
			name:          "an ordinary session acts as the resolver's choice, read from custom session data",
			session:       ordinary(),
			resolver:      fromCustomData,
			wantPrincipal: editor,
			wantStored:    true,
		},
		{
			name:    "a session that acts as a person skips the resolver",
			session: impersonated(accesstypes.UserPrincipal("bob")),
			resolver: func(context.Context) (accesstypes.Principal, error) {
				return accesstypes.Principal{}, errors.New("must not run")
			},
			wantPrincipal: accesstypes.UserPrincipal("bob"),
		},
		{
			name:    "a session that acts as a role skips the resolver",
			session: impersonated(accesstypes.RolePrincipal("PartnerViewer")),
			resolver: func(context.Context) (accesstypes.Principal, error) {
				return accesstypes.Principal{}, errors.New("must not run")
			},
			wantPrincipal: accesstypes.RolePrincipal("PartnerViewer"),
		},
		{
			name:    "the zero principal keeps the default",
			session: ordinary(),
			resolver: func(context.Context) (accesstypes.Principal, error) {
				return accesstypes.Principal{}, nil
			},
			wantPrincipal: accesstypes.UserPrincipal("alice"),
		},
		{
			name:    "returning the default leaves nothing recorded",
			session: ordinary(),
			resolver: func(ctx context.Context) (accesstypes.Principal, error) {
				return accesstypes.UserPrincipal(accesstypes.User(sessioninfo.FromCtx(ctx).Username)), nil
			},
			wantPrincipal: accesstypes.UserPrincipal("alice"),
		},
		{
			name:    "a resolver error fails the request as a server error, not unauthorized",
			session: ordinary(),
			resolver: func(context.Context) (accesstypes.Principal, error) {
				return accesstypes.Principal{}, errors.New("role lookup failed")
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			storage := mockstore.NewBaseStore(ctrl)
			storage.EXPECT().Session(gomock.Any(), sessionID).Return(tt.session, nil)

			s := &BaseSession{SessionTimeout: time.Minute, Storage: storage, PrincipalResolver: tt.resolver}

			ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)
			gotCtx, err := s.ValidateSessionAPI(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSessionAPI() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if httpio.HasUnauthorized(err) {
					t.Errorf("ValidateSessionAPI() error = %v, want a server error, not unauthorized", err)
				}

				return
			}
			if got := sessioninfo.PrincipalFromCtx(gotCtx); got != tt.wantPrincipal {
				t.Errorf("PrincipalFromCtx() = %v, want %v", got, tt.wantPrincipal)
			}
			if stored := tt.session.Principal != (accesstypes.Principal{}); stored != tt.wantStored {
				t.Errorf("SessionData.Principal recorded = %v, want %v", stored, tt.wantStored)
			}
		})
	}
}

// TestBaseSession_ValidateSessionAPI_noResolverDecidesAnImpersonatedSession holds whom
// a session that acts as someone else acts as to its record. The resolver here would
// answer another principal for every session, as one that reads a live lookup may, and
// it is never asked: not for a session that acts as a person, and not for one that acts
// as a role. The resolver is the application's code, and no code of the application's
// decides such a session.
func TestBaseSession_ValidateSessionAPI_noResolverDecidesAnImpersonatedSession(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.UUIDFromString("92922509-82d2-4bc7-853a-d73b8926a55f"))

	tests := []struct {
		name      string
		principal accesstypes.Principal
	}{
		{name: "a session that acts as a person acts as that person", principal: accesstypes.UserPrincipal("bob")},
		{name: "a session that acts as a role acts as that role", principal: accesstypes.RolePrincipal("PartnerViewer")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			storage := mockstore.NewBaseStore(gomock.NewController(t))
			session := &sessioninfo.SessionData{
				SessionInfo:   &sessioninfo.SessionInfo{ID: sessionID, Username: "bob", UpdatedAt: time.Now()},
				Impersonation: &sessioninfo.Impersonation{SessionID: sessionID, Actor: "alice", Principal: tt.principal, ExpiresAt: time.Now().Add(time.Hour)},
			}
			storage.EXPECT().Session(gomock.Any(), sessionID).Return(session, nil)

			asked := 0
			s := &BaseSession{
				SessionTimeout: time.Minute,
				Storage:        storage,
				PrincipalResolver: func(context.Context) (accesstypes.Principal, error) {
					asked++

					return accesstypes.UserPrincipal("carol"), nil
				},
			}

			ctx, err := s.ValidateSessionAPI(context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID))
			if err != nil {
				t.Fatalf("ValidateSessionAPI() error = %v", err)
			}
			if asked != 0 {
				t.Errorf("the resolver was asked %d time(s) about a session that acts as someone else, want never", asked)
			}
			if got := sessioninfo.PrincipalFromCtx(ctx); got != tt.principal {
				t.Errorf("PrincipalFromCtx() = %v, want the record's %v", got, tt.principal)
			}
			if got := session.Principal; got != (accesstypes.Principal{}) {
				t.Errorf("SessionData.Principal = %v, want nothing recorded", got)
			}
		})
	}
}
