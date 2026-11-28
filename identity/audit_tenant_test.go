package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestManager_RecordTenantSignIn pins the row of a tenant's own sign-in setting: done to
// the tenant, by its key and its name, in that tenant, by the caller, with the act's one
// part where it takes one (the role a tenant is opened to its directory's people with,
// the directory it took); a tenant the application names nothing is named by its key.
// An act that is not a tenant's, and a part an act does not take or one it is not
// handed, are refused and record nothing.
func TestManager_RecordTenantSignIn(t *testing.T) {
	t.Parallel()

	alice := recordCaller(accesstypes.DomainScope("alpha"))
	tenantRow := func(act auditlog.Act, tenant, name string) auditlog.Record {
		return auditlog.Record{
			Act: act, Source: eventsource.UserEvent(alice), SubjectKind: auditlog.SubjectTenant, SubjectID: tenant, SubjectName: name,
			TenantID: tenant, TenantName: name,
		}
	}
	opened := tenantRow(auditlog.ActDirectoryOpened, "alpha", "Alpha Sector")
	opened.RoleNames = []string{"Reader"}
	taken := tenantRow(auditlog.ActDirectoryTaken, "alpha", "Alpha Sector")
	taken.NewValue = "Lakeside"

	tests := []struct {
		name   string
		tenant accesstypes.Domain
		act    auditlog.Act
		named  string
		// want is the row; wantWhat what a person reads of it; wantErr the refusal's
		// words, where there is one.
		want     []auditlog.Record
		wantWhat string
		wantErr  string
	}{
		{
			name: "opened to its directory's people, with the role", tenant: "alpha", act: auditlog.ActDirectoryOpened, named: "Reader",
			want: []auditlog.Record{opened}, wantWhat: "Opened it to its directory's people with the role Reader",
		},
		{
			name: "a directory taken, by its name", tenant: "alpha", act: auditlog.ActDirectoryTaken, named: "Lakeside",
			want: []auditlog.Record{taken}, wantWhat: "Took the directory Lakeside as its own",
		},
		{
			name: "directory only turned on", tenant: "alpha", act: auditlog.ActDirectoryOnlyOn,
			want: []auditlog.Record{tenantRow(auditlog.ActDirectoryOnlyOn, "alpha", "Alpha Sector")}, wantWhat: "Turned directory only on",
		},
		{
			name: "a directory removed", tenant: "alpha", act: auditlog.ActDirectoryRemoved,
			want: []auditlog.Record{tenantRow(auditlog.ActDirectoryRemoved, "alpha", "Alpha Sector")}, wantWhat: "Removed its directory",
		},
		{
			name: "a tenant the application names nothing, by its key", tenant: "gamma", act: auditlog.ActDirectorySecondFactorVouched,
			want:     []auditlog.Record{tenantRow(auditlog.ActDirectorySecondFactorVouched, "gamma", "gamma")},
			wantWhat: "Said its directory asks for a second factor",
		},
		{name: "an act that is not a tenant's", tenant: "alpha", act: auditlog.ActRoleGiven, named: "Reader", wantErr: "roleGiven is not an act on a tenant's sign-in"},
		{name: "opened with no role", tenant: "alpha", act: auditlog.ActDirectoryOpened, wantErr: "directoryOpened names the role"},
		{name: "a directory taken with no name", tenant: "alpha", act: auditlog.ActDirectoryTaken, wantErr: "directoryTaken names the directory"},
		{name: "an act that takes no part, handed one", tenant: "alpha", act: auditlog.ActDirectoryInviteOnly, named: "Reader", wantErr: "directoryInviteOnly takes no part"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{}
			sessions := fakeSessions{tenantNames: map[accesstypes.Domain]string{"alpha": "Alpha Sector"}}
			m, err := New(managedAuth, store, &fakeEngine{roles: engineRoles}, sessions, WithTenantWord("sector"))
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			err = inTransaction(t, func(_ context.Context, txn resource.ReadWriteTransaction) error {
				return m.RecordTenantSignIn(alice, txn, tt.tenant, tt.act, tt.named)
			})
			switch {
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("RecordTenantSignIn() error = %v, want one that says %q", err, tt.wantErr)
			case tt.wantErr == "" && err != nil:
				t.Fatalf("RecordTenantSignIn() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, store.audit, cmp.AllowUnexported(eventsource.EventSource{})); diff != "" {
				t.Fatalf("audit rows (-want +got):\n%s", diff)
			}
			if len(tt.want) == 0 {
				return
			}
			stored := storedOf(t, &store.audit[0])
			if got := stored.Sentences("sector").What; got != tt.wantWhat {
				t.Errorf("the row reads %q, want %q", got, tt.wantWhat)
			}
		})
	}
}

// TestActingInSession pins who the rows of a route that serves a person and stamps no
// caller are done by, as the route by which a person makes their own tenant writes them:
// the first administrator's role and the directory the tenant was made with. Under
// ActingInSession they name the person of the session; without it, and where no session
// is, the application. What a sign-in's hook carries stays who acted.
func TestActingInSession(t *testing.T) {
	t.Parallel()

	session := context.WithValue(t.Context(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{ID: danaSession, Username: "dana"},
		UserID:      impulse.NullUUIDFromUUID(userID),
	})
	writes := []struct {
		name  string
		write func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
	}{
		{
			name: "the first administrator's role",
			write: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AssignRole(ctx, txn, alphaScope, userID, "Reader")
			},
		},
		{
			name: "the directory the tenant was made with",
			write: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RecordTenantSignIn(ctx, txn, "alpha", auditlog.ActDirectoryTaken, "Lakeside")
			},
		},
	}
	contexts := []struct {
		name string
		ctx  context.Context
		// wantPerson is the person the row is by, "" for none; wantProcess what acted
		// with them or alone.
		wantPerson  string
		wantProcess string
	}{
		{name: "under ActingInSession", ctx: ActingInSession(session), wantPerson: "dana"},
		{name: "in the session, without it", ctx: session, wantProcess: auditlog.ProcessApplication},
		{name: "under ActingInSession, where no session is", ctx: ActingInSession(t.Context()), wantProcess: auditlog.ProcessApplication},
		{name: "a sign-in's hook", ctx: actingAs(session, eventsource.ProcessEvent(auditlog.ProcessInvitation), ""), wantProcess: auditlog.ProcessInvitation},
	}
	for _, w := range writes {
		for _, tt := range contexts {
			t.Run(w.name+", "+tt.name, func(t *testing.T) {
				t.Parallel()

				store := groupStore()
				store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{}
				store.membershipReasons = map[access.MembershipKey]string{}
				m, err := New(managedAuth, store, &fakeEngine{roles: engineRoles}, fakeSessions{}, WithTenantWord("sector"))
				if err != nil {
					t.Fatalf("New() error = %v", err)
				}
				if err := inTransaction(t, func(_ context.Context, txn resource.ReadWriteTransaction) error {
					return w.write(tt.ctx, txn, m)
				}); err != nil {
					t.Fatalf("%s error = %v", w.name, err)
				}
				if len(store.audit) != 1 {
					t.Fatalf("%s recorded %d row(s), want one", w.name, len(store.audit))
				}
				who := storedOf(t, &store.audit[0]).Source
				if who.ActorUsername != tt.wantPerson || who.Process != tt.wantProcess {
					t.Errorf("the row is by %+v, want the person %q and the process %q", who, tt.wantPerson, tt.wantProcess)
				}
			})
		}
	}
}

// TestMakingTenant pins the name a row gives a tenant that is being made: the roster
// learns of a tenant only after the commit that makes it, so the rows of its making (a
// role given in it, and its own sign-in settings) name it by the name MakingTenant
// carries for exactly that tenant. Another tenant's name, an empty name, or none carried
// leave the key; a name the roster gives wins.
func TestMakingTenant(t *testing.T) {
	t.Parallel()

	writes := []struct {
		name  string
		write func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		// subject says the row is done to the tenant, so its SubjectName is the name too.
		subject bool
	}{
		{
			name: "a role given in the tenant",
			write: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AssignRole(ctx, txn, alphaScope, userID, "Reader")
			},
		},
		{
			name: "the directory the tenant was made with",
			write: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RecordTenantSignIn(ctx, txn, "alpha", auditlog.ActDirectoryTaken, "Lakeside")
			},
			subject: true,
		},
	}
	contexts := []struct {
		name string
		ctx  context.Context
		// rostered is the name the roster gives alpha, "" for none.
		rostered string
		want     string
	}{
		{name: "under MakingTenant", ctx: MakingTenant(t.Context(), "alpha", "Alpha School"), want: "Alpha School"},
		{name: "with no MakingTenant", ctx: t.Context(), want: "alpha"},
		{name: "under MakingTenant of another tenant", ctx: MakingTenant(t.Context(), "beta", "Beta School"), want: "alpha"},
		{name: "under MakingTenant with no name", ctx: MakingTenant(t.Context(), "alpha", ""), want: "alpha"},
		{name: "under MakingTenant, where the roster names the tenant", ctx: MakingTenant(t.Context(), "alpha", "Alpha School"), rostered: "Alpha Sector", want: "Alpha Sector"},
	}
	for _, w := range writes {
		for _, tt := range contexts {
			t.Run(w.name+", "+tt.name, func(t *testing.T) {
				t.Parallel()

				store := groupStore()
				store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{}
				store.membershipReasons = map[access.MembershipKey]string{}
				sessions := fakeSessions{}
				if tt.rostered != "" {
					sessions.tenantNames = map[accesstypes.Domain]string{"alpha": tt.rostered}
				}
				m, err := New(managedAuth, store, &fakeEngine{roles: engineRoles}, sessions, WithTenantWord("sector"))
				if err != nil {
					t.Fatalf("New() error = %v", err)
				}
				if err := inTransaction(t, func(_ context.Context, txn resource.ReadWriteTransaction) error {
					return w.write(tt.ctx, txn, m)
				}); err != nil {
					t.Fatalf("%s error = %v", w.name, err)
				}
				if len(store.audit) != 1 {
					t.Fatalf("%s recorded %d row(s), want one", w.name, len(store.audit))
				}
				row := store.audit[0]
				if row.TenantName != tt.want {
					t.Errorf("the row's tenant is named %q, want %q", row.TenantName, tt.want)
				}
				if w.subject && row.SubjectName != tt.want {
					t.Errorf("the row is done to the tenant named %q, want %q", row.SubjectName, tt.want)
				}
			})
		}
	}
}
