package auditlog

import (
	"testing"
	"time"
)

// TestWhat pins the sentence of every act and form, word for word, from a row that holds
// exactly the act's parts: a role in words, a list of roles, each of the three places,
// each of the four periods, and a tenant word handed and not.
func TestWhat(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 12, 1, 2, 30, 0, 0, time.FixedZone("MST", -7*60*60))
	end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		row        Stored
		tenantWord string
		want       string
	}{
		{name: "invited, one role, in its tenant", row: Stored{Act: ActInvited, RoleNames: []string{"Editor"}, RoleScope: RoleScopeTenant}, want: "Invited with the role Editor"},
		{name: "invited, two roles, for the whole application", row: Stored{Act: ActInvited, RoleNames: []string{"Editor", "NightShiftLead"}, RoleScope: RoleScopeGlobal}, want: "Invited with the roles Editor, Night shift lead for the whole application"},
		{name: "invited, in every tenant", row: Stored{Act: ActInvited, RoleNames: []string{"NightShiftLead"}, RoleScope: RoleScopeEveryTenant}, tenantWord: "company", want: "Invited with the role Night shift lead in every company"},
		{name: "invited, in every tenant, no tenant word", row: Stored{Act: ActInvited, RoleNames: []string{"Editor"}, RoleScope: RoleScopeEveryTenant}, want: "Invited with the role Editor in every tenant"},
		{name: "invited to make a tenant", row: Stored{Act: ActInvited, Form: FormNewTenant}, tenantWord: "company", want: "Invited to make a company"},
		{name: "invited with no role, into a tenant", row: Stored{Act: ActInvited, TenantID: "lakeside", TenantName: "Lakeside"}, want: "Invited to Lakeside"},
		{name: "invited with no role", row: Stored{Act: ActInvited}, want: "Invited"},
		{name: "invited to make a tenant, no tenant word", row: Stored{Act: ActInvited, Form: FormNewTenant}, want: "Invited to make a tenant"},
		{name: "invited to make a tenant, a word said with a vowel", row: Stored{Act: ActInvited, Form: FormNewTenant}, tenantWord: "office", want: "Invited to make an office"},
		{name: "invitation withdrawn", row: Stored{Act: ActInvitationWithdrawn}, want: "Withdrew the invitation"},
		{name: "invitation sent again", row: Stored{Act: ActInvitationSentAgain}, want: "Sent the invitation again"},
		{name: "invitation accepted", row: Stored{Act: ActInvitationAccepted}, want: "Accepted the invitation"},
		{name: "password reset link sent", row: Stored{Act: ActPasswordResetLinkSent}, want: "Sent a password reset link"},
		{name: "password chosen", row: Stored{Act: ActPasswordChosen}, want: "Chose a password"},
		{name: "password changed", row: Stored{Act: ActPasswordChanged}, want: "Changed the password"},
		{name: "password removed", row: Stored{Act: ActPasswordRemoved}, want: "Removed the password"},
		{name: "password change required", row: Stored{Act: ActPasswordChangeRequired}, want: "Required a new password"},
		{name: "identity unlinked", row: Stored{Act: ActIdentityUnlinked, OldValue: "Acme directory"}, want: "Unlinked the sign-in through Acme directory"},
		{name: "address change asked", row: Stored{Act: ActAddressChangeAsked, OldValue: "bob@old.example", NewValue: "bob@new.example"}, want: "Asked to change the email address from bob@old.example to bob@new.example"},
		{name: "address change asked, no old address", row: Stored{Act: ActAddressChangeAsked, NewValue: "bob@new.example"}, want: "Asked to set the email address to bob@new.example"},
		{name: "address change asked, the address held", row: Stored{Act: ActAddressChangeAsked, OldValue: "bob@example.com", NewValue: "bob@example.com"}, want: "Asked to confirm the email address bob@example.com"},
		{name: "address changed", row: Stored{Act: ActAddressChanged, OldValue: "bob@old.example", NewValue: "bob@new.example"}, want: "Changed the email address from bob@old.example to bob@new.example"},
		{name: "address changed, no old address", row: Stored{Act: ActAddressChanged, NewValue: "bob@new.example"}, want: "Set the email address to bob@new.example"},
		{name: "address changed, the address held", row: Stored{Act: ActAddressChanged, OldValue: "bob@example.com", NewValue: "bob@example.com"}, want: "Confirmed the email address bob@example.com"},
		{name: "address changed, the address held spelled otherwise", row: Stored{Act: ActAddressChanged, OldValue: "Bob@example.com", NewValue: "bob@example.com"}, want: "Changed the email address from Bob@example.com to bob@example.com"},
		{name: "username changed", row: Stored{Act: ActUsernameChanged, OldValue: "bob", NewValue: "robert"}, want: "Changed the username from bob to robert"},
		{name: "account disabled", row: Stored{Act: ActAccountDisabled}, want: "Disabled the account"},
		{name: "account enabled", row: Stored{Act: ActAccountEnabled}, want: "Enabled the account"},
		{name: "account deleted", row: Stored{Act: ActAccountDeleted}, want: "Deleted the account"},
		{name: "account deleted with its tenant", row: Stored{Act: ActAccountDeleted, Form: FormWithTenant}, tenantWord: "company", want: "Deleted the account with its company"},
		{name: "account deleted with its tenant, no tenant word", row: Stored{Act: ActAccountDeleted, Form: FormWithTenant}, want: "Deleted the account with its tenant"},
		{name: "token made", row: Stored{Act: ActTokenMade, NewValue: "deploy"}, want: "Made the token deploy"},
		{name: "token made, no name", row: Stored{Act: ActTokenMade}, want: "Made a token"},
		{name: "token revoked", row: Stored{Act: ActTokenRevoked}, want: "Revoked a token"},
		{name: "every token revoked", row: Stored{Act: ActTokenRevoked, Form: FormEveryToken}, want: "Revoked every token"},
		{name: "browsers forgotten", row: Stored{Act: ActBrowsersForgotten}, want: "Forgot the remembered browsers"},
		{name: "role given, no period", row: Stored{Act: ActRoleGiven, RoleNames: []string{"Editor"}, RoleScope: RoleScopeTenant}, want: "Gave the role Editor"},
		{name: "role given, from", row: Stored{Act: ActRoleGiven, RoleNames: []string{"Editor"}, RoleScope: RoleScopeTenant, PeriodStart: start}, want: "Gave the role Editor from 2026-12-01 09:30 UTC"},
		{name: "role given, until", row: Stored{Act: ActRoleGiven, RoleNames: []string{"Editor"}, RoleScope: RoleScopeTenant, PeriodEnd: end}, want: "Gave the role Editor until 2026-12-31 00:00 UTC"},
		{name: "role given, from and until", row: Stored{Act: ActRoleGiven, RoleNames: []string{"NightShiftLead"}, RoleScope: RoleScopeGlobal, PeriodStart: start, PeriodEnd: end}, want: "Gave the role Night shift lead for the whole application from 2026-12-01 09:30 UTC until 2026-12-31 00:00 UTC"},
		{name: "role given, in every tenant", row: Stored{Act: ActRoleGiven, RoleNames: []string{"Editor"}, RoleScope: RoleScopeEveryTenant, PeriodEnd: end}, tenantWord: "company", want: "Gave the role Editor in every company until 2026-12-31 00:00 UTC"},
		{name: "role given, a seat", row: Stored{Act: ActRoleGiven, Form: FormSeat, RoleNames: []string{"Editor"}, RoleScope: RoleScopeTenant}, want: "Gave the role Editor, the role its directory's people start with"},
		{name: "role taken", row: Stored{Act: ActRoleTaken, RoleNames: []string{"Editor"}, RoleScope: RoleScopeTenant}, want: "Took the role Editor"},
		{name: "role taken, for the whole application", row: Stored{Act: ActRoleTaken, RoleNames: []string{"Editor"}, RoleScope: RoleScopeGlobal}, want: "Took the role Editor for the whole application"},
		{name: "role taken by a move", row: Stored{Act: ActRoleTaken, Form: FormMove, RoleNames: []string{"Editor"}, RoleScope: RoleScopeTenant, SeatingTenantID: "globex", SeatingTenantName: "Globex"}, want: "Took the role Editor: they signed in through the directory of Globex and belong to it now"},
		{name: "role ended", row: Stored{Act: ActRoleEnded, RoleNames: []string{"Editor"}, RoleScope: RoleScopeEveryTenant}, want: "The role Editor in every tenant ended with its period"},
		{name: "group made", row: Stored{Act: ActGroupMade}, want: "Made the group"},
		{name: "group changed", row: Stored{Act: ActGroupChanged}, want: "Changed the group's description"},
		{name: "group removed", row: Stored{Act: ActGroupRemoved}, want: "Removed the group"},
		{name: "put in a group", row: Stored{Act: ActGroupMemberAdded, GroupID: "g1", GroupName: "Night crew"}, want: "Put in the group Night crew"},
		{name: "put in a group for a period", row: Stored{Act: ActGroupMemberAdded, GroupID: "g1", GroupName: "Night crew", PeriodStart: start, PeriodEnd: end}, want: "Put in the group Night crew from 2026-12-01 09:30 UTC until 2026-12-31 00:00 UTC"},
		{name: "taken out of a group", row: Stored{Act: ActGroupMemberRemoved, GroupID: "g1", GroupName: "Night crew"}, want: "Took out of the group Night crew"},
		{name: "taken out of a group by a move", row: Stored{Act: ActGroupMemberRemoved, Form: FormMove, GroupID: "g1", GroupName: "Night crew", SeatingTenantID: "globex", SeatingTenantName: "Globex"}, want: "Took out of the group Night crew: they signed in through the directory of Globex and belong to it now"},
		{name: "place in a group ended", row: Stored{Act: ActGroupPlaceEnded, GroupID: "g1", GroupName: "Night crew"}, want: "The place in the group Night crew ended with its period"},
		{name: "group given a role", row: Stored{Act: ActGroupRoleGiven, RoleNames: []string{"Editor"}, RoleScope: RoleScopeEveryTenant, PeriodStart: start}, tenantWord: "company", want: "Gave the group the role Editor in every company from 2026-12-01 09:30 UTC"},
		{name: "role taken from a group", row: Stored{Act: ActGroupRoleTaken, RoleNames: []string{"Editor"}, RoleScope: RoleScopeGlobal}, want: "Took the role Editor for the whole application from the group"},
		{name: "directory taken", row: Stored{Act: ActDirectoryTaken, NewValue: "Acme"}, want: "Took the directory Acme as its own"},
		{
			name: "directory taken, by its kind and its name", row: Stored{Act: ActDirectoryTaken, NewValue: "Microsoft Entra tenant Lakeside"},
			want: "Took the directory Microsoft Entra tenant Lakeside as its own",
		},
		{name: "directory taken, by its kind alone", row: Stored{Act: ActDirectoryTaken, NewValue: "Microsoft Entra tenant"}, want: "Took the directory Microsoft Entra tenant as its own"},
		{name: "directory removed", row: Stored{Act: ActDirectoryRemoved}, want: "Removed its directory"},
		{name: "directory only on", row: Stored{Act: ActDirectoryOnlyOn}, want: "Turned directory only on"},
		{name: "directory only off", row: Stored{Act: ActDirectoryOnlyOff}, want: "Turned directory only off"},
		{name: "directory opened", row: Stored{Act: ActDirectoryOpened, RoleNames: []string{"NightShiftLead"}}, want: "Opened it to its directory's people with the role Night shift lead"},
		{name: "directory invite only", row: Stored{Act: ActDirectoryInviteOnly}, want: "Made it invite only for its directory's people"},
		{name: "directory's second factor vouched for", row: Stored{Act: ActDirectorySecondFactorVouched}, want: "Said its directory asks for a second factor"},
		{name: "directory's second factor no longer vouched for", row: Stored{Act: ActDirectorySecondFactorUnvouched}, want: "Stopped saying its directory asks for a second factor"},
		{name: "sign-up opened", row: Stored{Act: ActSignUpOpened, SubjectName: "Google", RoleNames: []string{"Member"}}, want: "Opened Google to sign-up with the role Member"},
		{
			name: "sign-up opened, each person making a tenant", row: Stored{Act: ActSignUpOpened, SubjectName: "Google"}, tenantWord: "company",
			want: "Opened Google to sign-up, each person making a company of their own",
		},
		{name: "sign-up opened, each person making a tenant, no tenant word", row: Stored{Act: ActSignUpOpened, SubjectName: "Google"}, want: "Opened Google to sign-up, each person making a tenant of their own"},
		{name: "sign-up closed", row: Stored{Act: ActSignUpClosed, SubjectName: "Google"}, want: "Made Google invite only"},
		{name: "second factor required", row: Stored{Act: ActSecondFactorRequired}, want: "Required a second factor of everyone"},
		{name: "second factor not required", row: Stored{Act: ActSecondFactorNotRequired}, want: "Stopped requiring a second factor"},
		{name: "remembered browsers set", row: Stored{Act: ActRememberedBrowsersSet, NewValue: "30"}, want: "Set how long a browser is remembered to 30 days"},
		{name: "remembered browsers set to one day", row: Stored{Act: ActRememberedBrowsersSet, NewValue: "1"}, want: "Set how long a browser is remembered to 1 day"},
		{name: "self-service reset on", row: Stored{Act: ActSelfServiceResetOn}, want: "Let people reset their own password at the sign-in page"},
		{name: "self-service reset off", row: Stored{Act: ActSelfServiceResetOff}, want: "Stopped letting people reset their own password at the sign-in page"},
		{name: "an act it does not know", row: Stored{Act: "somethingElse", RoleNames: []string{"Editor"}}, want: "somethingElse"},
		{name: "a form its act does not have", row: Stored{Act: ActRoleTaken, Form: FormSeat, RoleNames: []string{"Editor"}, RoleScope: RoleScopeTenant}, want: "Took the role Editor"},
		{name: "a part the row lacks", row: Stored{Act: ActRoleGiven}, want: "Gave the role "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.row.what(tt.tenantWord); got != tt.want {
				t.Errorf("what() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestWhatCoversEveryAct holds TestWhat's list to every act and form: each has a sentence
// of its own, never its stored name.
func TestWhatCoversEveryAct(t *testing.T) {
	t.Parallel()

	if len(shapes()) < len(Acts()) || len(Acts()) == 0 {
		t.Fatalf("shapes() has %d acts and forms for %d acts", len(shapes()), len(Acts()))
	}
	for _, s := range shapes() {
		row := Stored{Act: s.act, Form: s.form}
		if got := row.what(""); got == "" || got == string(s.act) {
			t.Errorf("what() of %q, form %q = %q, want its sentence", s.act, s.form, got)
		}
	}
	for _, act := range Acts() {
		if _, ok := shapeOf(act, ""); !ok {
			t.Errorf("%q has no sentence", act)
		}
	}
}

// TestDoneTo pins whom or what an act was done to, as a person reads it, for each kind of
// subject and each setting.
func TestDoneTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  Stored
		want string
	}{
		{name: "an account", row: Stored{SubjectKind: SubjectAccount, SubjectID: "f1000000-0000-4000-8000-000000000001", SubjectName: "bob"}, want: "bob"},
		{name: "an account with no name kept", row: Stored{SubjectKind: SubjectAccount, SubjectID: "f1000000-0000-4000-8000-000000000001"}, want: "f1000000-0000-4000-8000-000000000001"},
		{name: "an invitation", row: Stored{SubjectKind: SubjectInvitation, SubjectID: "i1", SubjectName: "bob@example.com"}, want: "bob@example.com"},
		{name: "a group", row: Stored{SubjectKind: SubjectGroup, SubjectID: "g1", SubjectName: "Night crew"}, want: "Night crew"},
		{name: "a tenant", row: Stored{SubjectKind: SubjectTenant, SubjectID: "acme", SubjectName: "Acme"}, want: "Acme"},
		{name: "a tenant with no name", row: Stored{SubjectKind: SubjectTenant, SubjectID: "acme"}, want: "acme"},
		{name: "sign-up through a method", row: Stored{SubjectKind: SubjectSetting, SubjectID: SignUpSetting("google"), SubjectName: "Google"}, want: "Sign-up through Google"},
		{name: "the second factor", row: Stored{SubjectKind: SubjectSetting, SubjectID: SettingSecondFactor}, want: "Second factor"},
		{name: "remembered browsers", row: Stored{SubjectKind: SubjectSetting, SubjectID: SettingRememberedBrowsers}, want: "Remembered browsers"},
		{name: "the self-service reset", row: Stored{SubjectKind: SubjectSetting, SubjectID: SettingSelfServiceReset}, want: "Password reset at the sign-in page"},
		{name: "a setting it does not know", row: Stored{SubjectKind: SubjectSetting, SubjectID: "somethingElse"}, want: "somethingElse"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.row.doneTo(); got != tt.want {
				t.Errorf("doneTo() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSettings pins the fixed names a row of a setting stores as its SubjectId.
func TestSettings(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		got  string
		want string
	}{
		{got: SignUpSetting("google"), want: "signUp:google"},
		{got: SignUpSetting("password"), want: "signUp:password"},
		{got: SettingSecondFactor, want: "secondFactor"},
		{got: SettingRememberedBrowsers, want: "rememberedBrowsers"},
		{got: SettingSelfServiceReset, want: "selfServiceReset"},
	} {
		if tt.got != tt.want {
			t.Errorf("the setting is named %q, want %q", tt.got, tt.want)
		}
	}
}

// TestRememberedLength pins how long a browser is remembered, in the words of the list of
// lengths, from its days in text.
func TestRememberedLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		days string
		want string
	}{
		{days: "0", want: "Not remembered"},
		{days: "1", want: "1 day"},
		{days: "30", want: "30 days"},
		{days: "", want: ""},
		{days: "a fortnight", want: "a fortnight"},
	}
	for _, tt := range tests {
		t.Run(tt.days, func(t *testing.T) {
			t.Parallel()

			if got := rememberedLength(tt.days); got != tt.want {
				t.Errorf("rememberedLength(%q) = %q, want %q", tt.days, got, tt.want)
			}
		})
	}
}
