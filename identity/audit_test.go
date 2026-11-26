package identity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

func (f *fakeStore) InsertAuditRecord(_ context.Context, _ resource.ReadWriteTransaction, record *auditlog.Record) error {
	if err := f.fail("InsertAuditRecord"); err != nil {
		return err
	}
	// The generated store writes what Columns answers, which refuses a record that is
	// not its act's; the fake refuses it too, so a rule that writes a wrong one fails.
	if err := record.Validate(); err != nil {
		return errors.Wrap(err, "auditlog.Record.Validate()")
	}
	f.audit = append(f.audit, *record)

	return nil
}

var (
	// recordActor is the account of the person who acts in the tests of record, and
	// recordSubject the account they act on.
	recordActor   = impulse.Must(impulse.UUIDFromString("a11ce000-0000-4000-8000-000000000001"))
	recordSubject = impulse.Must(impulse.UUIDFromString("b0b00000-0000-4000-8000-000000000002"))
	// recordSession is the actor's session.
	recordSession = impulse.Must(impulse.UUIDFromString("5e550000-0000-4000-8000-000000000003"))
)

// recordCaller is the context of a request alice made on an outlet of the auth, checked
// in the scope: her session, the auth the routes bound, and the caller the frame stamped.
func recordCaller(scope accesstypes.Scope) context.Context {
	ctx := context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{ID: recordSession, Username: "alice"},
		UserID:      impulse.NullUUIDFromUUID(recordActor),
	})
	ctx = resource.WithAuth(ctx, managedAuth)

	return resource.WithCaller(ctx, &resource.Caller{Permissions: callerPermissions{user: User(recordActor)}, Scope: scope})
}

// disabledBob is the row of an account disabled, as a rule hands it to record: the act
// and its subject, and nothing of who acted or the tenant.
func disabledBob() *auditlog.Record {
	return &auditlog.Record{
		Act: auditlog.ActAccountDisabled, SubjectKind: auditlog.SubjectAccount,
		SubjectID: recordSubject.String(), SubjectName: "bob",
	}
}

// storedOf is the record as the table keeps it and a list reads it back: its columns,
// read as the generated store reads a row.
func storedOf(t *testing.T, record *auditlog.Record) auditlog.Stored {
	t.Helper()

	columns, err := record.Columns()
	if err != nil {
		t.Fatalf("Record.Columns() error = %v", err)
	}
	text := func(column string) string {
		value, _ := columns[column].(string)

		return value
	}
	at := func(column string) time.Time {
		value, _ := columns[column].(time.Time)

		return value
	}
	var roles []string
	if role := text("RoleName"); role != "" {
		roles = append(roles, role)
	}
	others, _ := columns["OtherRoleNames"].([]string)

	return auditlog.Stored{
		ID: text("Id"), Act: auditlog.Act(text("Act")), Form: auditlog.Form(text("Form")),
		Source: eventsource.Stored{
			Auth: text("Auth"), ActorUserID: text("ActorUserId"), ActorUsername: text("ActorUsername"),
			PrincipalUserID: text("PrincipalUserId"), PrincipalUsername: text("PrincipalUsername"), PrincipalRole: text("PrincipalRole"),
			SessionID: text("SessionId"), Process: text("Process"), RPC: text("Rpc"),
		},
		Directory: text("Directory"), SubjectKind: auditlog.SubjectKind(text("SubjectKind")), SubjectID: text("SubjectId"),
		SubjectName: text("SubjectName"), TenantID: text("TenantId"), TenantName: text("TenantName"),
		RoleNames: append(roles, others...), RoleScope: auditlog.RoleScope(text("RoleScope")),
		GroupID: text("GroupId"), GroupName: text("GroupName"), OldValue: text("OldValue"), NewValue: text("NewValue"),
		PeriodStart: at("PeriodStart"), PeriodEnd: at("PeriodEnd"),
		SeatingTenantID: text("SeatingTenantId"), SeatingTenantName: text("SeatingTenantName"), Reason: text("Reason"),
	}
}

// TestManager_record pins the package's one way to a row of the audit log: who acted is
// what a sign-in hook carried, else the person of the session a generated frame stamped a
// caller in, else the application; the tenant is the one the act named, else the one the
// request was checked in; and the tenant's name is the application's, or its key where
// the application names none.
func TestManager_record(t *testing.T) {
	t.Parallel()

	signIn := eventsource.ProcessEvent(auditlog.ProcessDirectorySignIn)
	tests := []struct {
		name string
		ctx  context.Context
		// tenant is the tenant the act itself names, "" for none.
		tenant        string
		wantSource    eventsource.EventSource
		wantDirectory string
		wantTenant    string
		wantName      string
		// wantWho is what a person reads as who acted, and wantColumns the columns of who
		// acted the row holds.
		wantWho     string
		wantColumns []string
	}{
		{
			name: "a caller in a tenant's scope", ctx: recordCaller(accesstypes.DomainScope("alpha")),
			wantSource: eventsource.UserEvent(recordCaller(accesstypes.DomainScope("alpha"))), wantTenant: "alpha", wantName: "Alpha Sector",
			wantWho: "alice", wantColumns: []string{"Auth", "ActorUserId", "ActorUsername", "SessionId"},
		},
		{
			name: "a caller in a tenant the application names none", ctx: recordCaller(accesstypes.DomainScope("gamma")),
			wantSource: eventsource.UserEvent(recordCaller(accesstypes.DomainScope("gamma"))), wantTenant: "gamma", wantName: "gamma",
			wantWho: "alice", wantColumns: []string{"Auth", "ActorUserId", "ActorUsername", "SessionId"},
		},
		{
			name: "an act that names its own tenant", ctx: recordCaller(accesstypes.DomainScope("alpha")), tenant: "beta",
			wantSource: eventsource.UserEvent(recordCaller(accesstypes.DomainScope("alpha"))), wantTenant: "beta", wantName: "Beta Sector",
			wantWho: "alice", wantColumns: []string{"Auth", "ActorUserId", "ActorUsername", "SessionId"},
		},
		{
			name: "a caller in the whole application's scope", ctx: recordCaller(accesstypes.GlobalScope()),
			wantSource: eventsource.UserEvent(recordCaller(accesstypes.GlobalScope())),
			wantWho:    "alice", wantColumns: []string{"Auth", "ActorUserId", "ActorUsername", "SessionId"},
		},
		{
			name: "a call outside any request", ctx: context.Background(),
			wantSource: eventsource.ProcessEvent(auditlog.ProcessApplication),
			wantWho:    "the application", wantColumns: []string{"Process"},
		},
		{
			name: "a caller stamped where no session is", ctx: asCaller(context.Background(), User(recordActor)),
			wantSource: eventsource.ProcessEvent(auditlog.ProcessApplication),
			wantWho:    "the application", wantColumns: []string{"Process"},
		},
		{
			name: "what a sign-in hook carried", ctx: actingAs(recordCaller(accesstypes.DomainScope("alpha")), signIn, "Acme"),
			wantSource: signIn, wantDirectory: "Acme", wantTenant: "alpha", wantName: "Alpha Sector",
			wantWho: "the directory Acme", wantColumns: []string{"Process"},
		},
		{
			name: "what a hook carried outside any request", ctx: actingAs(context.Background(), eventsource.ProcessEvent(auditlog.ProcessInvitation), ""),
			wantSource: eventsource.ProcessEvent(auditlog.ProcessInvitation),
			wantWho:    "an invitation", wantColumns: []string{"Process"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{}
			names := map[accesstypes.Domain]string{"alpha": "Alpha Sector", "beta": "Beta Sector"}
			m, err := New(managedAuth, store, &fakeEngine{}, fakeSessions{tenantNames: names})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			record := disabledBob()
			record.TenantID = tt.tenant
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.record(tt.ctx, txn, record)
			}); err != nil {
				t.Fatalf("record() error = %v", err)
			}

			if len(store.audit) != 1 {
				t.Fatalf("record() buffered %d rows, want one", len(store.audit))
			}
			got := store.audit[0]
			want := *disabledBob()
			want.Source, want.Directory, want.TenantID, want.TenantName = tt.wantSource, tt.wantDirectory, tt.wantTenant, tt.wantName
			if diff := cmp.Diff(want, got, cmp.AllowUnexported(eventsource.EventSource{})); diff != "" {
				t.Errorf("record() buffered (-want +got):\n%s", diff)
			}
			var columns []string
			for _, column := range eventsource.ColumnNames() {
				if _, set := got.Source.Columns()[column]; set {
					columns = append(columns, column)
				}
			}
			if diff := cmp.Diff(tt.wantColumns, columns); diff != "" {
				t.Errorf("the row's columns of who acted (-want +got):\n%s", diff)
			}
			stored := storedOf(t, &got)
			if who := stored.Sentences("sector").Who; who != tt.wantWho {
				t.Errorf("the row reads as done by %q, want %q", who, tt.wantWho)
			}
		})
	}
}

// TestManager_record_storeFails pins that a store that fails the write fails the act,
// under the store's method.
func TestManager_record_storeFails(t *testing.T) {
	t.Parallel()

	store := &fakeStore{failOn: "InsertAuditRecord"}
	m, _ := newManager(t, store, nil)
	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return m.record(ctx, txn, disabledBob())
	})
	if err == nil || !strings.Contains(err.Error(), "identity.Store.InsertAuditRecord()") {
		t.Errorf("record() error = %v, want the store's failure under identity.Store.InsertAuditRecord()", err)
	}
	if len(store.audit) != 0 {
		t.Errorf("record() buffered %d rows, want none", len(store.audit))
	}
}

// TestManager_accountSubject pins how a row names the account an act was done to: by its
// id, and by its username, or its id where no user holds it.
func TestManager_accountSubject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		store *fakeStore
		want  *auditlog.Record
	}{
		{
			name:  "an account",
			store: &fakeStore{usernames: map[impulse.UUID]string{recordSubject: "bob"}},
			want:  &auditlog.Record{SubjectKind: auditlog.SubjectAccount, SubjectID: recordSubject.String(), SubjectName: "bob"},
		},
		{
			name:  "an id no user holds",
			store: &fakeStore{},
			want:  &auditlog.Record{SubjectKind: auditlog.SubjectAccount, SubjectID: recordSubject.String(), SubjectName: recordSubject.String()},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, _ := newManager(t, tt.store, nil)
			var got *auditlog.Record
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				got, err = m.accountSubject(ctx, txn, recordSubject)

				return err
			}); err != nil {
				t.Fatalf("accountSubject() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(eventsource.EventSource{})); diff != "" {
				t.Errorf("accountSubject() (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("a store that fails", func(t *testing.T) {
		t.Parallel()

		m, _ := newManager(t, &fakeStore{failOn: "Username"}, nil)
		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			_, err := m.accountSubject(ctx, txn, recordSubject)

			return err
		})
		if err == nil || !strings.Contains(err.Error(), "identity.Store.Username()") {
			t.Errorf("accountSubject() error = %v, want the store's failure under identity.Store.Username()", err)
		}
	})
}

// TestTenantOfScope pins the tenant a scope names: the domain of one tenant's scope, and
// none for the whole application's.
func TestTenantOfScope(t *testing.T) {
	t.Parallel()

	if got := tenantOfScope(accesstypes.DomainScope("alpha")); got != "alpha" {
		t.Errorf("tenantOfScope(alpha's scope) = %q, want %q", got, "alpha")
	}
	if got := tenantOfScope(accesstypes.GlobalScope()); got != "" {
		t.Errorf("tenantOfScope(the global scope) = %q, want none", got)
	}
	var zero accesstypes.Scope
	if got := tenantOfScope(zero); got != "" {
		t.Errorf("tenantOfScope(the zero scope) = %q, want none", got)
	}
}
