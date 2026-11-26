package auditlog

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// testSession is the session every test context of the package carries, as its column
// holds it.
const testSession = "de6e1a12-2d4d-4c4d-aaf1-d82cb9a9eff5"

// aliceID and bobID are the accounts of the tests' two people: alice acts, and bob is the
// account acts are done to.
var (
	aliceID = impulse.Must(impulse.UUIDFromString("a11ce000-0000-4000-8000-000000000001"))
	bobID   = impulse.Must(impulse.UUIDFromString("b0b00000-0000-4000-8000-000000000002"))
)

// accountCtx is a context with a session of the account and its username, acting as imp's
// principal where imp is not nil, as an Auth's sessions are.
func accountCtx(username string, userID impulse.UUID, imp *sessioninfo.Impersonation) context.Context {
	return context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo:   &sessioninfo.SessionInfo{ID: impulse.Must(impulse.UUIDFromString(testSession)), Username: username},
		Impersonation: imp,
		UserID:        impulse.NullUUIDFromUUID(userID),
	})
}

// alice is alice in her own session.
func alice() eventsource.EventSource {
	return eventsource.UserEvent(accountCtx("alice", aliceID, nil))
}

// storedFrom reads a row's columns back by their names, as a read of the table fills a
// Stored.
func storedFrom(columns map[string]any) Stored {
	text := func(name string) string {
		value, _ := columns[name].(string)

		return value
	}
	moment := func(name string) time.Time {
		value, _ := columns[name].(time.Time)

		return value
	}
	var roles []string
	if role := text("RoleName"); role != "" {
		roles = append(roles, role)
	}
	if others, ok := columns["OtherRoleNames"].([]string); ok {
		roles = append(roles, others...)
	}

	return Stored{
		ID: text("Id"), At: moment("CreatedAt"), Act: Act(text("Act")), Form: Form(text("Form")),
		Source: eventsource.Stored{
			Auth: text("Auth"), ActorUserID: text("ActorUserId"), ActorUsername: text("ActorUsername"),
			PrincipalUserID: text("PrincipalUserId"), PrincipalUsername: text("PrincipalUsername"), PrincipalRole: text("PrincipalRole"),
			SessionID: text("SessionId"), Process: text("Process"), RPC: text("Rpc"),
		},
		Directory: text("Directory"), SubjectKind: SubjectKind(text("SubjectKind")), SubjectID: text("SubjectId"), SubjectName: text("SubjectName"),
		TenantID: text("TenantId"), TenantName: text("TenantName"), RoleNames: roles, RoleScope: RoleScope(text("RoleScope")),
		GroupID: text("GroupId"), GroupName: text("GroupName"), OldValue: text("OldValue"), NewValue: text("NewValue"),
		PeriodStart: moment("PeriodStart"), PeriodEnd: moment("PeriodEnd"),
		SeatingTenantID: text("SeatingTenantId"), SeatingTenantName: text("SeatingTenantName"), Reason: text("Reason"),
	}
}

// The subjects of the tests' records, one of each kind.
const (
	invitationID = "c0000000-0000-4000-8000-000000000003"
	groupID      = "90000000-0000-4000-8000-000000000004"
)

// acceptedRecords is one record the log takes for each act and form, with the parts A1.3's
// table gives it, and a few more where a part may be empty.
func acceptedRecords() []Record {
	bob := bobID.String()
	end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	account := func(act Act, form Form) Record {
		return Record{Act: act, Form: form, SubjectKind: SubjectAccount, SubjectID: bob, SubjectName: "bob"}
	}
	invitation := func(act Act, form Form) Record {
		return Record{Act: act, Form: form, SubjectKind: SubjectInvitation, SubjectID: invitationID, SubjectName: "carol@example.com"}
	}
	group := func(act Act) Record {
		return Record{Act: act, SubjectKind: SubjectGroup, SubjectID: groupID, SubjectName: "Night crew", TenantID: "anvil", TenantName: "Anvil"}
	}
	tenant := func(act Act) Record {
		return Record{Act: act, SubjectKind: SubjectTenant, SubjectID: "anvil", SubjectName: "Anvil", TenantID: "anvil", TenantName: "Anvil"}
	}
	setting := func(act Act, id, name string) Record {
		return Record{Act: act, SubjectKind: SubjectSetting, SubjectID: id, SubjectName: name}
	}
	with := func(r Record, set func(r *Record)) Record {
		set(&r)

		return r
	}
	role := func(r *Record) {
		r.RoleNames, r.RoleScope, r.TenantID, r.TenantName = []string{"Editor"}, RoleScopeTenant, "anvil", "Anvil"
	}
	inGroup := func(r *Record) { r.GroupID, r.GroupName = groupID, "Night crew" }
	seated := func(r *Record) { r.SeatingTenantID, r.SeatingTenantName = "globex", "Globex" }

	return []Record{
		with(invitation(ActInvited, ""), func(r *Record) { r.RoleNames, r.RoleScope = []string{"Editor", "NightShiftLead"}, RoleScopeGlobal }),
		with(invitation(ActInvited, ""), role),
		invitation(ActInvited, FormNewTenant),
		invitation(ActInvitationWithdrawn, ""),
		invitation(ActInvitationSentAgain, ""),
		account(ActInvitationAccepted, ""),
		account(ActPasswordResetLinkSent, ""),
		account(ActPasswordChosen, ""),
		account(ActPasswordChanged, ""),
		account(ActPasswordRemoved, ""),
		account(ActPasswordChangeRequired, ""),
		with(account(ActIdentityUnlinked, ""), func(r *Record) { r.OldValue = "Acme" }),
		with(account(ActAddressChangeAsked, ""), func(r *Record) { r.OldValue, r.NewValue = "bob@old.example", "bob@new.example" }),
		with(account(ActAddressChangeAsked, ""), func(r *Record) { r.NewValue = "bob@new.example" }),
		with(account(ActAddressChanged, ""), func(r *Record) { r.OldValue, r.NewValue = "bob@old.example", "bob@new.example" }),
		with(account(ActUsernameChanged, ""), func(r *Record) { r.OldValue, r.NewValue = "bob", "robert" }),
		account(ActAccountDisabled, ""),
		account(ActAccountEnabled, ""),
		account(ActAccountDeleted, ""),
		account(ActAccountDeleted, FormWithTenant),
		with(account(ActTokenMade, ""), func(r *Record) { r.NewValue = "deploy" }),
		account(ActTokenMade, ""),
		account(ActTokenRevoked, ""),
		account(ActBrowsersForgotten, ""),
		with(account(ActRoleGiven, ""), role),
		with(account(ActRoleGiven, ""), func(r *Record) { role(r); r.Period = accesstypes.Period{Start: time.Time{}, End: end} }),
		with(account(ActRoleGiven, FormSeat), role),
		with(account(ActRoleTaken, ""), role),
		with(account(ActRoleTaken, FormMove), func(r *Record) { role(r); seated(r) }),
		with(account(ActRoleEnded, ""), role),
		group(ActGroupMade),
		group(ActGroupChanged),
		group(ActGroupRemoved),
		with(account(ActGroupMemberAdded, ""), inGroup),
		with(account(ActGroupMemberRemoved, ""), inGroup),
		with(account(ActGroupMemberRemoved, FormMove), func(r *Record) { inGroup(r); seated(r) }),
		with(account(ActGroupPlaceEnded, ""), inGroup),
		with(group(ActGroupRoleGiven), role),
		with(group(ActGroupRoleTaken), role),
		with(tenant(ActDirectoryTaken), func(r *Record) { r.NewValue = "Anvil Workspace" }),
		tenant(ActDirectoryRemoved),
		tenant(ActDirectoryOnlyOn),
		tenant(ActDirectoryOnlyOff),
		with(tenant(ActDirectoryOpened), func(r *Record) { r.RoleNames = []string{"Editor"} }),
		tenant(ActDirectoryInviteOnly),
		tenant(ActDirectorySecondFactorVouched),
		tenant(ActDirectorySecondFactorUnvouched),
		with(setting(ActSignUpOpened, SignUpSetting("google"), "Google"), func(r *Record) { r.RoleNames = []string{"Viewer"} }),
		setting(ActSignUpOpened, SignUpSetting("google"), "Google"),
		setting(ActSignUpClosed, SignUpSetting("google"), "Google"),
		setting(ActSecondFactorRequired, SettingSecondFactor, ""),
		setting(ActSecondFactorNotRequired, SettingSecondFactor, ""),
		with(setting(ActRememberedBrowsersSet, SettingRememberedBrowsers, ""), func(r *Record) { r.NewValue = "30" }),
		setting(ActSelfServiceResetOn, SettingSelfServiceReset, ""),
		setting(ActSelfServiceResetOff, SettingSelfServiceReset, ""),
	}
}

// TestValidateAcceptsEveryActAndForm holds the log open to a record of every act and form
// that holds the parts the act gives it: each one of acceptedRecords is taken, and between
// them they cover every act and form.
func TestValidateAcceptsEveryActAndForm(t *testing.T) {
	t.Parallel()

	type actForm struct {
		act  Act
		form Form
	}
	covered := map[actForm]bool{}
	for _, r := range acceptedRecords() {
		r.Source = alice()
		if err := r.Validate(); err != nil {
			t.Errorf("Validate() of a record of %s error = %v, want none", r.named(), err)
		}
		covered[actForm{act: r.Act, form: r.Form}] = true
	}
	for _, s := range shapes() {
		if !covered[actForm{act: s.act, form: s.form}] {
			t.Errorf("no accepted record of %s, form %q", s.act, s.form)
		}
	}
}

// TestValidate pins each refusal of a record the log must not hold, one case each, and
// the setting that keeps no name.
func TestValidate(t *testing.T) {
	t.Parallel()

	bob := bobID.String()
	disabled := Record{Act: ActAccountDisabled, Source: alice(), SubjectKind: SubjectAccount, SubjectID: bob, SubjectName: "bob"}
	given := Record{
		Act: ActRoleGiven, Source: alice(), SubjectKind: SubjectAccount, SubjectID: bob, SubjectName: "bob",
		RoleNames: []string{"Editor"}, RoleScope: RoleScopeGlobal,
	}
	with := func(r Record, set func(r *Record)) Record {
		set(&r)

		return r
	}
	tests := []struct {
		name    string
		record  Record
		wantErr string
	}{
		{name: "an act that is not one of the log's", record: with(disabled, func(r *Record) { r.Act = "accountLent" }), wantErr: `"accountLent" is not an act of the audit log`},
		{name: "a form the act does not have", record: with(given, func(r *Record) { r.Form = FormMove }), wantErr: `the act roleGiven has no form "move"`},
		{name: "nobody who acted", record: with(disabled, func(r *Record) { r.Source = eventsource.EventSource{} }), wantErr: "a record of accountDisabled names nobody who acted"},
		{name: "a directory's sign-in with no directory", record: with(given, func(r *Record) { r.Source = eventsource.ProcessEvent(ProcessDirectorySignIn) }), wantErr: "a record of roleGiven made by a directory's sign-in names no directory"},
		{name: "a subject of another kind", record: with(given, func(r *Record) { r.SubjectKind = SubjectGroup }), wantErr: `a record of roleGiven is done to the subject kind "account", not "group"`},
		{name: "a subject with no id", record: with(disabled, func(r *Record) { r.SubjectID = "" }), wantErr: "a record of accountDisabled names its subject by no id"},
		{name: "an account with no name", record: with(disabled, func(r *Record) { r.SubjectName = "" }), wantErr: "a record of accountDisabled names its subject " + bob + " by no name"},
		{
			name:    "a sign-up setting with no name",
			record:  Record{Act: ActSignUpClosed, Source: alice(), SubjectKind: SubjectSetting, SubjectID: SignUpSetting("google")},
			wantErr: "a record of signUpClosed names its subject signUp:google by no name",
		},
		{name: "a tenant's name with no tenant", record: with(disabled, func(r *Record) { r.TenantName = "Anvil" }), wantErr: `a record of accountDisabled names the tenant "Anvil" and no tenant id`},
		{name: "a part the act needs, empty", record: with(given, func(r *Record) { r.RoleScope = "" }), wantErr: "a record of roleGiven needs RoleScope, which is empty"},
		{name: "a part the act does not give, set", record: with(disabled, func(r *Record) { r.RoleNames = []string{"Editor"} }), wantErr: "a record of accountDisabled sets RoleName, which the act does not give"},
		{name: "a second role where the act names one", record: with(given, func(r *Record) { r.RoleNames = []string{"Editor", "Viewer"} }), wantErr: "a record of roleGiven sets OtherRoleNames, which the act does not give"},
		{name: "a part of another form, set", record: with(given, func(r *Record) { r.SeatingTenantID = "globex" }), wantErr: "a record of roleGiven sets SeatingTenantId, which the act does not give"},
		{name: "a setting that keeps no name", record: Record{Act: ActSecondFactorRequired, Source: alice(), SubjectKind: SubjectSetting, SubjectID: SettingSecondFactor}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.record.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("Validate() error = %v, want none", err)
			case tt.wantErr != "" && (err == nil || !strings.HasSuffix(err.Error(), "error="+tt.wantErr)):
				t.Errorf("Validate() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// TestRecordColumns pins the row of a role given in a tenant for a period with a reason:
// a new id, every column that holds something, by the table's names, and none that holds
// nothing; no CreatedAt, which is the writer's. A record the log refuses answers no row.
func TestRecordColumns(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 12, 1, 9, 30, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	r := Record{
		Act: ActRoleGiven, Source: alice(), SubjectKind: SubjectAccount, SubjectID: bobID.String(), SubjectName: "bob",
		TenantID: "anvil", TenantName: "Anvil", RoleNames: []string{"NightShiftLead"}, RoleScope: RoleScopeTenant,
		Period: accesstypes.Period{Start: start, End: end}, Reason: "Covers the night shift",
	}
	got, err := r.Columns()
	if err != nil {
		t.Fatalf("Columns() error = %v", err)
	}
	id, _ := got["Id"].(string)
	if _, err := impulse.UUIDFromString(id); err != nil {
		t.Errorf("Columns()'s Id = %q, want a new UUID: %v", id, err)
	}
	delete(got, "Id")
	want := map[string]any{
		"Act": "roleGiven", "ActorUserId": aliceID.String(), "ActorUsername": "alice", "SessionId": testSession,
		"SubjectKind": "account", "SubjectId": bobID.String(), "SubjectName": "bob", "TenantId": "anvil", "TenantName": "Anvil",
		"RoleName": "NightShiftLead", "RoleScope": "tenant", "PeriodStart": start, "PeriodEnd": end, "Reason": "Covers the night shift",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Columns() mismatch (-want +got):\n%s", diff)
	}

	again, err := r.Columns()
	if err != nil {
		t.Fatalf("Columns() again error = %v", err)
	}
	if again["Id"] == id {
		t.Errorf("Columns() made the id %q twice, want a new one each time", id)
	}

	r.RoleScope = ""
	if columns, err := r.Columns(); err == nil {
		t.Errorf("Columns() of a record with no RoleScope = %v, want Validate's error", columns)
	}
}

// TestRecordColumnsHoldNoEmptyValue holds every accepted record's row free of an empty
// text, an empty list, a zero time and CreatedAt.
func TestRecordColumnsHoldNoEmptyValue(t *testing.T) {
	t.Parallel()

	for _, r := range acceptedRecords() {
		r.Source = alice()
		columns, err := r.Columns()
		if err != nil {
			t.Fatalf("Columns() of a record of %s error = %v", r.named(), err)
		}
		for name, value := range columns {
			switch v := value.(type) {
			case string:
				if v == "" {
					t.Errorf("a row of %s holds %s empty", r.named(), name)
				}
			case []string:
				if len(v) == 0 {
					t.Errorf("a row of %s holds %s empty", r.named(), name)
				}
			case time.Time:
				if v.IsZero() {
					t.Errorf("a row of %s holds %s as the zero time", r.named(), name)
				}
			default:
				t.Errorf("a row of %s holds %s as %T, want a text, a list of texts or a time", r.named(), name, value)
			}
		}
		if _, ok := columns["CreatedAt"]; ok {
			t.Errorf("a row of %s holds CreatedAt, which is the writer's", r.named())
		}
	}
}

// TestPostgresInsert pins the INSERT of a row on PostgreSQL: the columns it is handed,
// quoted, in the table's order, and CreatedAt last as now(), with the arguments in the
// columns' order.
func TestPostgresInsert(t *testing.T) {
	t.Parallel()

	columns := map[string]any{
		"OtherRoleNames": []string{"Viewer"}, "SubjectId": invitationID, "Id": "e1111111-1111-4111-8111-111111111111",
		"RoleName": "Editor", "ActorUsername": "alice", "Act": "invited", "SubjectKind": "invitation",
	}
	statement, args := PostgresInsert("CrewSessionAuditRecords", columns)
	wantStatement := `INSERT INTO "CrewSessionAuditRecords" ("Id", "Act", "ActorUsername", "SubjectKind", "SubjectId", "RoleName", "OtherRoleNames", "CreatedAt") ` +
		`VALUES ($1, $2, $3, $4, $5, $6, $7, now())`
	if statement != wantStatement {
		t.Errorf("PostgresInsert() statement = %q, want %q", statement, wantStatement)
	}
	wantArgs := []any{"e1111111-1111-4111-8111-111111111111", "invited", "alice", "invitation", invitationID, "Editor", []string{"Viewer"}}
	if diff := cmp.Diff(wantArgs, args); diff != "" {
		t.Errorf("PostgresInsert() args mismatch (-want +got):\n%s", diff)
	}
}

// TestPostgresInsertNamesTheColumnsOfEveryRecord holds the INSERT of each accepted
// record's row to exactly the columns Columns answered and CreatedAt, each argument once.
func TestPostgresInsertNamesTheColumnsOfEveryRecord(t *testing.T) {
	t.Parallel()

	for _, r := range acceptedRecords() {
		r.Source = alice()
		columns, err := r.Columns()
		if err != nil {
			t.Fatalf("Columns() of a record of %s error = %v", r.named(), err)
		}
		statement, args := PostgresInsert("T", columns)
		named, _, _ := strings.Cut(strings.TrimPrefix(statement, `INSERT INTO "T" (`), ")")
		got := strings.Split(named, ", ")
		want := []string{`"CreatedAt"`}
		for name := range maps.Keys(columns) {
			want = append(want, `"`+name+`"`)
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) || len(args) != len(columns) {
			t.Errorf("PostgresInsert() of a row of %s names %q with %d arguments, want %q with %d", r.named(), got, len(args), want, len(columns))
		}
	}
}

// TestColumnsHoldTheColumnsOfWhoActed holds the table's columns of who acted to the nine
// eventsource names, in their order.
func TestColumnsHoldTheColumnsOfWhoActed(t *testing.T) {
	t.Parallel()

	names := eventsource.ColumnNames()
	all := Columns()
	i := slices.Index(all, names[0])
	if i < 0 || i+len(names) > len(all) || !slices.Equal(all[i:i+len(names)], names) {
		t.Errorf("Columns() = %q, want it to hold %q in order", all, names)
	}
}

// TestSentencesWho pins who acted, as a person reads it: a person, a person acting as
// another and as a role, each of the library's processes with no person, a directory, a
// person through a process, and a process the library does not know.
func TestSentencesWho(t *testing.T) {
	t.Parallel()

	asBob := accountCtx("bob", bobID, &sessioninfo.Impersonation{
		Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.UserPrincipal(accesstypes.User(bobID.String())),
	})
	asSupport := accountCtx("alice", aliceID, &sessioninfo.Impersonation{
		Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.RolePrincipal("Support"),
	})
	tests := []struct {
		name      string
		source    eventsource.EventSource
		directory string
		want      string
	}{
		{name: "a person", source: alice(), want: "alice"},
		{name: "a person acting as another", source: eventsource.UserEvent(asBob), want: "alice, acting as bob"},
		{name: "a person acting as a role", source: eventsource.UserEvent(asSupport), want: "alice, acting as the role Support"},
		{name: "the application", source: eventsource.ProcessEvent(ProcessApplication), want: "the application"},
		{name: "the migrate command", source: eventsource.ProcessEvent(ProcessMigrate), want: "the migrate command"},
		{name: "a password reset link", source: eventsource.ProcessEvent(ProcessResetLink), want: "a password reset link"},
		{name: "an address confirmation link", source: eventsource.ProcessEvent(ProcessAddressLink), want: "an address confirmation link"},
		{name: "an invitation", source: eventsource.ProcessEvent(ProcessInvitation), want: "an invitation"},
		{name: "a sign-up", source: eventsource.ProcessEvent(ProcessSignUp), want: "a sign-up"},
		{name: "the sign-in page", source: eventsource.ProcessEvent(ProcessSignInPage), want: "the sign-in page"},
		{name: "a device", source: eventsource.ProcessEvent(ProcessDevice), want: "a device the person approved"},
		{name: "a period's end", source: eventsource.ProcessEvent(ProcessPeriodEnd), want: "the end of its period"},
		{name: "a directory", source: eventsource.ProcessEvent(ProcessDirectorySignIn), directory: "Acme", want: "the directory Acme"},
		{name: "a person through a process", source: eventsource.UserProcessEvent(accountCtx("alice", aliceID, nil), ProcessSignUp), want: "alice, through the process SignUp"},
		{name: "a process the library does not know", source: eventsource.ProcessEvent("Nightly"), want: "the process Nightly"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := Record{Act: ActAccountDisabled, Source: tt.source, Directory: tt.directory, SubjectKind: SubjectAccount, SubjectID: bobID.String(), SubjectName: "bob"}
			columns, err := r.Columns()
			if err != nil {
				t.Fatalf("Columns() error = %v", err)
			}
			stored := storedFrom(columns)
			want := Sentences{Who: tt.want, What: "Disabled the account", DoneTo: "bob"}
			if got := stored.Sentences(""); got != want {
				t.Errorf("Sentences() = %+v, want %+v", got, want)
			}
		})
	}
}

// TestSentencesOfARowReadBack pins what a person reads of a row written from a record and
// read back by its columns' names, for three acts: an invitation of two roles in every
// tenant, a role a move took, and a setting the application set.
func TestSentencesOfARowReadBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		record Record
		want   Sentences
	}{
		{
			name: "an invitation of two roles in every tenant",
			record: Record{
				Act: ActInvited, Source: alice(), SubjectKind: SubjectInvitation, SubjectID: invitationID, SubjectName: "carol@example.com",
				RoleNames: []string{"Editor", "NightShiftLead"}, RoleScope: RoleScopeEveryTenant,
			},
			want: Sentences{Who: "alice", What: "Invited with the roles Editor, Night shift lead in every sector", DoneTo: "carol@example.com"},
		},
		{
			name: "a role a move took",
			record: Record{
				Act: ActRoleTaken, Form: FormMove, Source: eventsource.ProcessEvent(ProcessDirectorySignIn), Directory: "Globex Workspace",
				SubjectKind: SubjectAccount, SubjectID: bobID.String(), SubjectName: "bob", TenantID: "anvil", TenantName: "Anvil",
				RoleNames: []string{"NightShiftLead"}, RoleScope: RoleScopeTenant, SeatingTenantID: "globex", SeatingTenantName: "Globex",
			},
			want: Sentences{
				Who:    "the directory Globex Workspace",
				What:   "Took the role Night shift lead: they signed in through the directory of Globex and belong to it now",
				DoneTo: "bob",
			},
		},
		{
			name: "a setting the application set",
			record: Record{
				Act: ActRememberedBrowsersSet, Source: eventsource.ProcessEvent(ProcessApplication), SubjectKind: SubjectSetting,
				SubjectID: SettingRememberedBrowsers, NewValue: "30",
			},
			want: Sentences{Who: "the application", What: "Set how long a browser is remembered to 30 days", DoneTo: "Remembered browsers"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			columns, err := tt.record.Columns()
			if err != nil {
				t.Fatalf("Columns() error = %v", err)
			}
			stored := storedFrom(columns)
			if got := stored.Sentences("sector"); got != tt.want {
				t.Errorf("Sentences() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestDirectorySync pins what a sign-in hands the permission store in the context: what a
// context carries is what was put in it, and a context with none answers none.
func TestDirectorySync(t *testing.T) {
	t.Parallel()

	if sync, ok := DirectorySyncFrom(context.Background()); ok {
		t.Errorf("DirectorySyncFrom() of a context with none = %+v, true, want none", sync)
	}
	named := func(tenant accesstypes.Domain) string {
		return "the tenant " + string(tenant)
	}
	ctx := WithDirectorySync(context.Background(), DirectorySync{Directory: "Acme", UserID: bobID.String(), Username: "bob", TenantName: named})
	got, ok := DirectorySyncFrom(ctx)
	if !ok {
		t.Fatalf("DirectorySyncFrom() found nothing, want the sync")
	}
	if got.Directory != "Acme" || got.UserID != bobID.String() || got.Username != "bob" || got.TenantName == nil || got.TenantName("anvil") != "the tenant anvil" {
		t.Errorf("DirectorySyncFrom() = %+v, want the directory Acme, bob's account and the tenant's name", got)
	}
}
