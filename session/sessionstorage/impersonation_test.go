package sessionstorage

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/postgres"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/spanner"
	gomock "go.uber.org/mock/gomock"
)

func TestNewImpersonationTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		tableName string
		wantErr   bool
	}{
		{name: "valid", tableName: "PartnerSessionImpersonations"},
		{name: "underscore prefix", tableName: "_imp"},
		{name: "rejects digits first", tableName: "1imp", wantErr: true},
		{name: "rejects punctuation", tableName: "Session-Impersonations", wantErr: true},
		{name: "rejects empty", tableName: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			table, err := NewImpersonationTable(tt.tableName)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewImpersonationTable() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && table.TableName() != tt.tableName {
				t.Errorf("TableName() = %q, want %q", table.TableName(), tt.tableName)
			}
		})
	}
}

func TestWithImpersonation_EnablesBothDrivers(t *testing.T) {
	t.Parallel()

	table, err := NewImpersonationTable("SessionImpersonations")
	if err != nil {
		t.Fatal(err)
	}
	opt := WithImpersonation(table)

	sp := spanner.NewSessionStorageDriver(nil)
	opt.applySpanner(sp)
	if !sp.ImpersonationEnabled() {
		t.Error("spanner driver not enabled")
	}

	pg := postgres.NewSessionStorageDriver(nil)
	opt.applyPostgres(pg)
	if !pg.ImpersonationEnabled() {
		t.Error("postgres driver not enabled")
	}

	if spanner.NewSessionStorageDriver(nil).ImpersonationEnabled() {
		t.Error("driver enabled without the option")
	}
}

func Test_sessionStorage_Session_MapsImpersonation(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000"))
	started := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		row  *dbtype.Impersonation
		want *sessioninfo.Impersonation
	}{
		{name: "not impersonated"},
		{
			name: "impersonated role",
			row: &dbtype.Impersonation{
				SessionID:     sessionID,
				ActorUsername: "alice",
				PrincipalKind: dbtype.PrincipalKindRole,
				PrincipalRole: strPtr("Editor"),
				StartedAt:     started,
				ExpiresAt:     started.Add(time.Hour),
			},
			want: &sessioninfo.Impersonation{
				SessionID: sessionID,
				Actor:     "alice",
				Principal: accesstypes.RolePrincipal("Editor"),
				StartedAt: started,
				ExpiresAt: started.Add(time.Hour),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockDB := NewMockdb(ctrl)
			mockDB.EXPECT().Session(gomock.Any(), sessionID).Return(&dbtype.SessionData{
				Session:       &dbtype.Session{ID: sessionID, Username: "alice"},
				Impersonation: tt.row,
			}, nil)

			s := &sessionStorage{db: mockDB}
			got, err := s.Session(context.Background(), sessionID)
			if err != nil {
				t.Fatalf("Session() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got.Impersonation, principalComparer, maskComparer); diff != "" {
				t.Errorf("Session().Impersonation mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// lockedLog is a buffer the standard logger may write from any goroutine.
type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	n, err := l.buf.Write(p)
	if err != nil {
		return n, errors.Wrap(err, "bytes.Buffer.Write()")
	}

	return n, nil
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.String()
}

// Test_sessionStorage_unreadableMaskIsLogged pins what the store does with a record
// whose mask column is no mask's text, when it loads a session and when it lists the
// live ones: the record keeps nothing, and the log says which session's mask does not
// read and why, so a session that can do nothing is explained where an operator looks.
// A record whose mask reads is not mentioned. The standard logger is the process's: the
// test takes its output and looks for the lines that name its own sessions.
func Test_sessionStorage_unreadableMaskIsLogged(t *testing.T) {
	t.Parallel()

	var output lockedLog
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })

	loaded := impulse.Must(impulse.UUIDFromString("0bad0001-0000-4000-8000-00000000a001"))
	listed := impulse.Must(impulse.UUIDFromString("0bad0002-0000-4000-8000-00000000a002"))
	readable := impulse.Must(impulse.UUIDFromString("0bad0003-0000-4000-8000-00000000a003"))
	started := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	row := func(id impulse.UUID, mask string) *dbtype.Impersonation {
		return &dbtype.Impersonation{
			SessionID:       id,
			ActorUsername:   "alice",
			PrincipalKind:   dbtype.PrincipalKindUser,
			PrincipalUserID: strPtr("bob"),
			Mask:            &mask,
			StartedAt:       started,
			ExpiresAt:       started.Add(time.Hour),
		}
	}

	ctrl := gomock.NewController(t)
	mockDB := NewMockdb(ctrl)
	mockDB.EXPECT().Session(gomock.Any(), loaded).Return(&dbtype.SessionData{
		Session:       &dbtype.Session{ID: loaded, Username: "bob"},
		Impersonation: row(loaded, "*;class:credential"),
	}, nil)
	mockDB.EXPECT().ActiveImpersonations(gomock.Any(), started, gomock.Any()).Return([]*dbtype.Impersonation{
		row(listed, "List,Read;-everything"),
		row(readable, "*;-class:credential"),
	}, nil)
	s := &sessionStorage{db: mockDB}

	sess, err := s.Session(context.Background(), loaded)
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	if !sess.Impersonation.Mask.Equal(accesstypes.DenyAll()) {
		t.Errorf("Session() read the mask %q, want the mask that keeps nothing", sess.Impersonation.Mask.Text())
	}
	imps, err := s.ActiveImpersonations(context.Background(), started, nil)
	if err != nil {
		t.Fatalf("ActiveImpersonations() error = %v", err)
	}
	if len(imps) != 2 || !imps[0].Mask.Equal(accesstypes.DenyAll()) || imps[1].Mask.Text() != "*;-class:credential" {
		t.Fatalf("ActiveImpersonations() masks = %v, want the unreadable one kept to nothing and the other as written", imps)
	}

	logged := output.String()
	for _, want := range []string{
		"impersonation: the mask stored for session " + loaded.String() + ` ("*;class:credential") does not read, so the session is held to nothing: `,
		`the part "class:credential" is no refusal: one opens with "-class:" or "-on:"`,
		"impersonation: the mask stored for session " + listed.String() + ` ("List,Read;-everything") does not read, so the session is held to nothing: `,
		`the part "-everything" is no refusal: one opens with "-class:" or "-on:"`,
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log does not carry %q:\n%s", want, logged)
		}
	}
	if strings.Contains(logged, readable.String()) {
		t.Errorf("the log names the session whose mask reads:\n%s", logged)
	}
}

func Test_sessionStorage_CreateImpersonatedSession(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000"))
	started := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	expires := started.Add(time.Hour)

	tests := []struct {
		name    string
		prepare func(*Mockdb)
		wantErr bool
	}{
		{
			name: "renders the insert row and returns the id",
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().
					InsertImpersonatedSession(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, session *dbtype.InsertSession, req *sessioninfo.NewSessionRequest, imp *dbtype.InsertImpersonation) (impulse.UUID, error) {
						if session.Username != "bob" || req.Reason != sessioninfo.ReasonImpersonation {
							return impulse.NilUUID, errors.Newf("unexpected session %+v / request %+v", session, req)
						}
						want := &dbtype.InsertImpersonation{
							ActorUsername:   "alice",
							PrincipalKind:   dbtype.PrincipalKindUser,
							PrincipalUserID: strPtr("bob"),
							Mask:            strPtr("List,Read"),
							ExpiresAt:       expires,
						}
						if diff := cmp.Diff(want, imp); diff != "" {
							return impulse.NilUUID, errors.New("unexpected InsertImpersonation: " + diff)
						}

						return sessionID, nil
					})
			},
		},
		{
			name: "propagates the driver error",
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().InsertImpersonatedSession(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(impulse.NilUUID, errors.New("not configured"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockDB := NewMockdb(ctrl)
			tt.prepare(mockDB)

			s := &sessionStorage{db: mockDB}
			imp := &sessioninfo.Impersonation{
				Actor:     "alice",
				Principal: accesstypes.UserPrincipal("bob"),
				Mask:      accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.Read, accesstypes.List),
				StartedAt: started,
				ExpiresAt: expires,
			}
			got, err := s.CreateImpersonatedSession(context.Background(), &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: "bob"}, imp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CreateImpersonatedSession() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != sessionID {
				t.Errorf("CreateImpersonatedSession() = %v, want %v", got, sessionID)
			}
		})
	}
}

func Test_sessionStorage_ImpersonationDelegates(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000"))

	t.Run("ImpersonationEnabled", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		mockDB := NewMockdb(ctrl)
		mockDB.EXPECT().ImpersonationEnabled().Return(true)

		if !(&sessionStorage{db: mockDB}).ImpersonationEnabled() {
			t.Error("ImpersonationEnabled() = false, want true")
		}
	})

	t.Run("EndImpersonation passes the reason as text and wraps errors", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		mockDB := NewMockdb(ctrl)
		mockDB.EXPECT().EndImpersonation(gomock.Any(), sessionID, "Expired").Return(errors.New("boom"))

		if err := (&sessionStorage{db: mockDB}).EndImpersonation(context.Background(), sessionID, sessioninfo.ImpersonationEndedByExpiry); err == nil {
			t.Error("EndImpersonation() error = nil, want error")
		}
	})

	t.Run("DestroyImpersonatedSession passes the reason as text and wraps errors", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		mockDB := NewMockdb(ctrl)
		mockDB.EXPECT().DestroyImpersonatedSession(gomock.Any(), sessionID, "Released").Return(errors.New("boom"))

		if err := (&sessionStorage{db: mockDB}).DestroyImpersonatedSession(context.Background(), sessionID, sessioninfo.ImpersonationEndedByRelease); err == nil {
			t.Error("DestroyImpersonatedSession() error = nil, want error")
		}
	})

	t.Run("DestroyImpersonatedSessions delegates by actor", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		mockDB := NewMockdb(ctrl)
		mockDB.EXPECT().DestroyImpersonatedSessions(gomock.Any(), "alice").Return(nil)

		if err := (&sessionStorage{db: mockDB}).DestroyImpersonatedSessions(context.Background(), "alice"); err != nil {
			t.Errorf("DestroyImpersonatedSessions() error = %v", err)
		}
	})
}

var (
	principalComparer = cmp.Comparer(func(a, b accesstypes.Principal) bool { return a == b })
	maskComparer      = cmp.Comparer(func(a, b accesstypes.PermissionMask) bool { return a.String() == b.String() })
)

func strPtr(s string) *string { return &s }

func Test_sessionStorage_ActiveImpersonations(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000"))
	started := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	activeSince := started.Add(-10 * time.Minute)
	q := &sessioninfo.ImpersonationQuery{Actor: "alice"}

	tests := []struct {
		name    string
		prepare func(*Mockdb)
		want    []*sessioninfo.Impersonation
		wantErr bool
	}{
		{
			name: "maps every row and passes the cutoff and query through",
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().ActiveImpersonations(gomock.Any(), activeSince, q).Return([]*dbtype.Impersonation{{
					SessionID:     sessionID,
					ActorUsername: "alice",
					PrincipalKind: dbtype.PrincipalKindRole,
					PrincipalRole: strPtr("Editor"),
					StartedAt:     started,
					ExpiresAt:     started.Add(time.Hour),
				}}, nil)
			},
			want: []*sessioninfo.Impersonation{{
				SessionID: sessionID,
				Actor:     "alice",
				Principal: accesstypes.RolePrincipal("Editor"),
				StartedAt: started,
				ExpiresAt: started.Add(time.Hour),
			}},
		},
		{
			name: "no rows is an empty listing",
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().ActiveImpersonations(gomock.Any(), activeSince, q).Return([]*dbtype.Impersonation{}, nil)
			},
			want: []*sessioninfo.Impersonation{},
		},
		{
			name: "propagates the driver error",
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().ActiveImpersonations(gomock.Any(), activeSince, q).Return(nil, errors.New("not configured"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockDB := NewMockdb(ctrl)
			tt.prepare(mockDB)

			got, err := (&sessionStorage{db: mockDB}).ActiveImpersonations(context.Background(), activeSince, q)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ActiveImpersonations() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got, principalComparer, maskComparer); diff != "" {
				t.Errorf("ActiveImpersonations() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
