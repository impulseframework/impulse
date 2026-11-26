package auditlog

import (
	"slices"

	"github.com/impulseframework/impulse/accesstypes"
)

// Act is the fixed name of one act of the audit log, as a row stores it in its Act column
// and as a filter of the log names it. Acts lists them all.
type Act string

// The acts, in the order Acts gives them. Each is stored under its own words, and each
// row of one names whom or what it was done to (SubjectKind).
const (
	// ActInvited is an invitation sent, with its roles, with none, or to make a tenant.
	ActInvited Act = "invited"
	// ActInvitationWithdrawn is an invitation withdrawn before it was accepted.
	ActInvitationWithdrawn Act = "invitationWithdrawn"
	// ActInvitationSentAgain is an invitation sent again, under a new link.
	ActInvitationSentAgain Act = "invitationSentAgain"
	// ActInvitationAccepted is an invitation accepted by the account it made or reached.
	ActInvitationAccepted Act = "invitationAccepted"
	// ActPasswordResetLinkSent is a link that resets an account's password, mailed.
	ActPasswordResetLinkSent Act = "passwordResetLinkSent"
	// ActPasswordChosen is a password chosen for an account that had none, or through a
	// reset link.
	ActPasswordChosen Act = "passwordChosen"
	// ActPasswordChanged is a password changed by the person who knew the old one.
	ActPasswordChanged Act = "passwordChanged"
	// ActPasswordRemoved is an account's password removed.
	ActPasswordRemoved Act = "passwordRemoved"
	// ActPasswordChangeRequired is an account required to choose a new password.
	ActPasswordChangeRequired Act = "passwordChangeRequired"
	// ActIdentityUnlinked is a sign-in identity unlinked from an account.
	ActIdentityUnlinked Act = "identityUnlinked"
	// ActAddressChangeAsked is a change of an account's address asked for: a link mailed
	// to the new address, which changes nothing until it is followed.
	ActAddressChangeAsked Act = "addressChangeAsked"
	// ActAddressChanged is an account's address changed.
	ActAddressChanged Act = "addressChanged"
	// ActUsernameChanged is an account's username changed.
	ActUsernameChanged Act = "usernameChanged"
	// ActAccountDisabled is an account disabled.
	ActAccountDisabled Act = "accountDisabled"
	// ActAccountEnabled is an account enabled again.
	ActAccountEnabled Act = "accountEnabled"
	// ActAccountDeleted is an account deleted, alone or with its tenant.
	ActAccountDeleted Act = "accountDeleted"
	// ActTokenMade is a token made for an account.
	ActTokenMade Act = "tokenMade"
	// ActTokenRevoked is an account's token revoked.
	ActTokenRevoked Act = "tokenRevoked"
	// ActBrowsersForgotten is the browsers an account had remembered, forgotten.
	ActBrowsersForgotten Act = "browsersForgotten"
	// ActRoleGiven is a role given to an account, with its place and its period.
	ActRoleGiven Act = "roleGiven"
	// ActRoleTaken is a role taken from an account.
	ActRoleTaken Act = "roleTaken"
	// ActRoleEnded is an account's role that ended with its period, cleared.
	ActRoleEnded Act = "roleEnded"
	// ActGroupMade is a group made.
	ActGroupMade Act = "groupMade"
	// ActGroupChanged is a group's description changed.
	ActGroupChanged Act = "groupChanged"
	// ActGroupRemoved is a group removed.
	ActGroupRemoved Act = "groupRemoved"
	// ActGroupMemberAdded is an account put in a group, with its period.
	ActGroupMemberAdded Act = "groupMemberAdded"
	// ActGroupMemberRemoved is an account taken out of a group.
	ActGroupMemberRemoved Act = "groupMemberRemoved"
	// ActGroupPlaceEnded is an account's place in a group that ended with its period,
	// cleared.
	ActGroupPlaceEnded Act = "groupPlaceEnded"
	// ActGroupRoleGiven is a role given to a group, with its place and its period.
	ActGroupRoleGiven Act = "groupRoleGiven"
	// ActGroupRoleTaken is a role taken from a group.
	ActGroupRoleTaken Act = "groupRoleTaken"
	// ActDirectoryTaken is a directory a tenant took as its own.
	ActDirectoryTaken Act = "directoryTaken"
	// ActDirectoryRemoved is a tenant's directory removed.
	ActDirectoryRemoved Act = "directoryRemoved"
	// ActDirectoryOnlyOn is a tenant's people held to its directory.
	ActDirectoryOnlyOn Act = "directoryOnlyOn"
	// ActDirectoryOnlyOff is a tenant's people no longer held to its directory.
	ActDirectoryOnlyOff Act = "directoryOnlyOff"
	// ActDirectoryOpened is a tenant opened to its directory's people, with the role they
	// start with.
	ActDirectoryOpened Act = "directoryOpened"
	// ActDirectoryInviteOnly is a tenant made invite only for its directory's people.
	ActDirectoryInviteOnly Act = "directoryInviteOnly"
	// ActDirectorySecondFactorVouched is a tenant's word that its directory asks for a
	// second factor.
	ActDirectorySecondFactorVouched Act = "directorySecondFactorVouched"
	// ActDirectorySecondFactorUnvouched is a tenant's word that its directory asks for a
	// second factor, taken back.
	ActDirectorySecondFactorUnvouched Act = "directorySecondFactorUnvouched"
	// ActSignUpOpened is a sign-in method opened to sign-up, with the role a sign-up
	// starts with, or with none where each person who signs up makes a tenant of their own.
	ActSignUpOpened Act = "signUpOpened"
	// ActSignUpClosed is a sign-in method made invite only.
	ActSignUpClosed Act = "signUpClosed"
	// ActSecondFactorRequired is a second factor required of everyone.
	ActSecondFactorRequired Act = "secondFactorRequired"
	// ActSecondFactorNotRequired is a second factor no longer required of everyone.
	ActSecondFactorNotRequired Act = "secondFactorNotRequired"
	// ActRememberedBrowsersSet is how long a browser is remembered, set.
	ActRememberedBrowsersSet Act = "rememberedBrowsersSet"
	// ActSelfServiceResetOn is people let to ask at the sign-in page for a link that
	// resets their own password.
	ActSelfServiceResetOn Act = "selfServiceResetOn"
	// ActSelfServiceResetOff is people no longer let to ask at the sign-in page for a link
	// that resets their own password.
	ActSelfServiceResetOff Act = "selfServiceResetOff"
)

// Acts lists every act of the audit log once, in the order a filter of the log offers
// them.
func Acts() []Act {
	var acts []Act
	all := shapes()
	for i := range all {
		if all[i].form == "" {
			acts = append(acts, all[i].act)
		}
	}

	return acts
}

// Words is the act as a filter's choice says it ("Role given"). An act that is not one
// of Acts' is its stored name.
func (a Act) Words() string {
	if s, ok := shapeOf(a, ""); ok {
		return s.words
	}

	return string(a)
}

// actShape is one form of an act: what a row of it is done to, the parts it holds, how a
// filter's choice says the act, and how the sentence of a row of it is written from the
// row's parts. A part is written in braces in the sentence (what fills each). A row is
// refused when it is done to another kind, lacks a part the form needs, or sets one the
// form does not give (Record.Validate).
type actShape struct {
	act       Act
	form      Form
	kind      SubjectKind // what a row of the form is done to
	parts     []partNeed  // the parts a row of the form holds beyond the always-set ones
	words     string      // the act as a filter's choice says it; the act's own form alone has them
	sentence  string      // the act in words
	ifEmpty   string      // a part whose emptiness picks the other sentence; "" for none
	otherwise string      // the sentence of a row whose part ifEmpty is empty
}

// partNeed is one part a row of an act's form holds beyond the always-set ones, who
// acted, the tenant and the reason: the part's column, and whether every row of the form
// sets it.
type partNeed struct {
	column   string
	required bool
}

// need is parts every row of the form sets, by their columns.
func need(columns ...string) []partNeed {
	needs := make([]partNeed, 0, len(columns))
	for _, column := range columns {
		needs = append(needs, partNeed{column: column, required: true})
	}

	return needs
}

// mayBeEmpty is parts a row of the form may set or leave empty, by their columns.
func mayBeEmpty(columns ...string) []partNeed {
	needs := make([]partNeed, 0, len(columns))
	for _, column := range columns {
		needs = append(needs, partNeed{column: column, required: false})
	}

	return needs
}

// movedTail ends the sentence of what a move took from a person.
const movedTail = ": they signed in through the directory of {SeatingTenantName} and belong to it now"

// shapes is every act of the audit log, each followed by its other forms, in the order
// Acts gives the acts.
func shapes() []actShape {
	return []actShape{
		shapeOrWhenEmpty(ActInvited, SubjectInvitation, "Invited", "Invited with the {roles}{place}", "{roles}", "Invited{to tenant}",
			mayBeEmpty(columnRoleName, columnRoleScope, columnOtherRoleNames)),
		formShape(ActInvited, FormNewTenant, SubjectInvitation, "Invited to make {a tenant word}"),
		shape(ActInvitationWithdrawn, SubjectInvitation, "Invitation withdrawn", "Withdrew the invitation"),
		shape(ActInvitationSentAgain, SubjectInvitation, "Invitation sent again", "Sent the invitation again"),
		shape(ActInvitationAccepted, SubjectAccount, "Invitation accepted", "Accepted the invitation"),
		shape(ActPasswordResetLinkSent, SubjectAccount, "Password reset link sent", "Sent a password reset link"),
		shape(ActPasswordChosen, SubjectAccount, "Password chosen", "Chose a password"),
		shape(ActPasswordChanged, SubjectAccount, "Password changed", "Changed the password"),
		shape(ActPasswordRemoved, SubjectAccount, "Password removed", "Removed the password"),
		shape(ActPasswordChangeRequired, SubjectAccount, "New password required", "Required a new password"),
		shape(ActIdentityUnlinked, SubjectAccount, "Sign-in identity unlinked", "Unlinked the sign-in through {OldValue}", need(columnOldValue)),
		shapeOrWhenEmpty(ActAddressChangeAsked, SubjectAccount, "Address change asked", "Asked to change the email address from {OldValue} to {NewValue}",
			"{OldValue}", "Asked to set the email address to {NewValue}", mayBeEmpty(columnOldValue), need(columnNewValue)),
		shapeOrWhenEmpty(ActAddressChanged, SubjectAccount, "Address changed", "Changed the email address from {OldValue} to {NewValue}",
			"{OldValue}", "Set the email address to {NewValue}", mayBeEmpty(columnOldValue), need(columnNewValue)),
		shape(ActUsernameChanged, SubjectAccount, "Username changed", "Changed the username from {OldValue} to {NewValue}", need(columnOldValue, columnNewValue)),
		shape(ActAccountDisabled, SubjectAccount, "Account disabled", "Disabled the account"),
		shape(ActAccountEnabled, SubjectAccount, "Account enabled", "Enabled the account"),
		shape(ActAccountDeleted, SubjectAccount, "Account deleted", "Deleted the account"),
		formShape(ActAccountDeleted, FormWithTenant, SubjectAccount, "Deleted the account with its {tenant word}"),
		shapeOrWhenEmpty(ActTokenMade, SubjectAccount, "Token made", "Made the token {NewValue}", "{NewValue}", "Made a token", mayBeEmpty(columnNewValue)),
		shape(ActTokenRevoked, SubjectAccount, "Token revoked", "Revoked a token"),
		shape(ActBrowsersForgotten, SubjectAccount, "Remembered browsers forgotten", "Forgot the remembered browsers"),
		shape(ActRoleGiven, SubjectAccount, "Role given", "Gave the role {role}{place}{period}", need(columnRoleName, columnRoleScope), mayBeEmpty(columnPeriodStart, columnPeriodEnd)),
		formShape(ActRoleGiven, FormSeat, SubjectAccount, "Gave the role {role}, the role its directory's people start with", need(columnRoleName, columnRoleScope)),
		shape(ActRoleTaken, SubjectAccount, "Role taken", "Took the role {role}{place}", need(columnRoleName, columnRoleScope)),
		formShape(ActRoleTaken, FormMove, SubjectAccount, "Took the role {role}{place}"+movedTail, need(columnRoleName, columnRoleScope), need(columnSeatingTenantID, columnSeatingTenantName)),
		shape(ActRoleEnded, SubjectAccount, "Role ended", "The role {role}{place} ended with its period", need(columnRoleName, columnRoleScope)),
		shape(ActGroupMade, SubjectGroup, "Group made", "Made the group"),
		shape(ActGroupChanged, SubjectGroup, "Group changed", "Changed the group's description"),
		shape(ActGroupRemoved, SubjectGroup, "Group removed", "Removed the group"),
		shape(ActGroupMemberAdded, SubjectAccount, "Put in a group", "Put in the group {GroupName}{period}", need(columnGroupID, columnGroupName), mayBeEmpty(columnPeriodStart, columnPeriodEnd)),
		shape(ActGroupMemberRemoved, SubjectAccount, "Taken out of a group", "Took out of the group {GroupName}", need(columnGroupID, columnGroupName)),
		formShape(ActGroupMemberRemoved, FormMove, SubjectAccount, "Took out of the group {GroupName}"+movedTail, need(columnGroupID, columnGroupName), need(columnSeatingTenantID, columnSeatingTenantName)),
		shape(ActGroupPlaceEnded, SubjectAccount, "Place in a group ended", "The place in the group {GroupName} ended with its period", need(columnGroupID, columnGroupName)),
		shape(ActGroupRoleGiven, SubjectGroup, "Group given a role", "Gave the group the role {role}{place}{period}", need(columnRoleName, columnRoleScope), mayBeEmpty(columnPeriodStart, columnPeriodEnd)),
		shape(ActGroupRoleTaken, SubjectGroup, "Role taken from a group", "Took the role {role}{place} from the group", need(columnRoleName, columnRoleScope)),
		shape(ActDirectoryTaken, SubjectTenant, "Directory taken", "Took the directory {NewValue} as its own", need(columnNewValue)),
		shape(ActDirectoryRemoved, SubjectTenant, "Directory removed", "Removed its directory"),
		shape(ActDirectoryOnlyOn, SubjectTenant, "Directory only on", "Turned directory only on"),
		shape(ActDirectoryOnlyOff, SubjectTenant, "Directory only off", "Turned directory only off"),
		shape(ActDirectoryOpened, SubjectTenant, "Opened to its directory", "Opened it to its directory's people with the role {role}", need(columnRoleName)),
		shape(ActDirectoryInviteOnly, SubjectTenant, "Made invite only for its directory", "Made it invite only for its directory's people"),
		shape(ActDirectorySecondFactorVouched, SubjectTenant, "Directory's second factor vouched for", "Said its directory asks for a second factor"),
		shape(ActDirectorySecondFactorUnvouched, SubjectTenant, "Directory's second factor no longer vouched for", "Stopped saying its directory asks for a second factor"),
		shapeOrWhenEmpty(ActSignUpOpened, SubjectSetting, "Sign-up opened", "Opened {SubjectName} to sign-up with the role {role}",
			"{role}", "Opened {SubjectName} to sign-up, each person making {a tenant word} of their own", mayBeEmpty(columnRoleName)),
		shape(ActSignUpClosed, SubjectSetting, "Made invite only", "Made {SubjectName} invite only"),
		shape(ActSecondFactorRequired, SubjectSetting, "Second factor required", "Required a second factor of everyone"),
		shape(ActSecondFactorNotRequired, SubjectSetting, "Second factor no longer required", "Stopped requiring a second factor"),
		shape(ActRememberedBrowsersSet, SubjectSetting, "Remembered browsers set", "Set how long a browser is remembered to {length}", need(columnNewValue)),
		shape(ActSelfServiceResetOn, SubjectSetting, "Own password reset turned on", "Let people reset their own password at the sign-in page"),
		shape(ActSelfServiceResetOff, SubjectSetting, "Own password reset turned off", "Stopped letting people reset their own password at the sign-in page"),
	}
}

// shape is an act's own form: what it is done to, its words as a filter's choice, its
// sentence, and its parts.
func shape(act Act, kind SubjectKind, words, sentence string, parts ...[]partNeed) actShape {
	return actShape{act: act, form: "", kind: kind, parts: slices.Concat(parts...), words: words, sentence: sentence, ifEmpty: "", otherwise: ""}
}

// formShape is another form of an act, which a filter's choice says as the act.
func formShape(act Act, form Form, kind SubjectKind, sentence string, parts ...[]partNeed) actShape {
	return actShape{act: act, form: form, kind: kind, parts: slices.Concat(parts...), words: "", sentence: sentence, ifEmpty: "", otherwise: ""}
}

// shapeOrWhenEmpty is an act's own form whose sentence is another for a row whose part is
// empty.
func shapeOrWhenEmpty(act Act, kind SubjectKind, words, sentence, part, otherwise string, parts ...[]partNeed) actShape {
	return actShape{
		act: act, form: "", kind: kind, parts: slices.Concat(parts...), words: words, sentence: sentence, ifEmpty: part, otherwise: otherwise,
	}
}

// formOf answers the shape of exactly an act's form, and false where the act is not one of
// Acts' or has no such form.
func formOf(act Act, form Form) (*actShape, bool) {
	all := shapes()
	for i := range all {
		if all[i].act == act && all[i].form == form {
			return &all[i], true
		}
	}

	return nil, false
}

// shapeOf answers the shape of an act's form, and the act's own form for a form the act
// does not have. It answers false for an act that is not one of Acts'.
func shapeOf(act Act, form Form) (actShape, bool) {
	var own actShape
	found := false
	all := shapes()
	for i := range all {
		if all[i].act != act {
			continue
		}
		if all[i].form == form {
			return all[i], true
		}
		if all[i].form == "" {
			own, found = all[i], true
		}
	}

	return own, found
}

// SubjectKind is what an act was done to, as a row stores it in its SubjectKind column.
type SubjectKind string

// The kinds of thing an act is done to. A role is not one: a role given is done to an
// account or a group, and the row names the role.
const (
	// SubjectAccount is an account, by its id and its username.
	SubjectAccount SubjectKind = "account"
	// SubjectInvitation is an invitation, by its id and the address it was sent to, since
	// no account exists yet.
	SubjectInvitation SubjectKind = "invitation"
	// SubjectGroup is a group, by its id and its name.
	SubjectGroup SubjectKind = "group"
	// SubjectTenant is a tenant, by its key and what the application calls it.
	SubjectTenant SubjectKind = "tenant"
	// SubjectSetting is one of the auth's sign-in settings, by its fixed name.
	SubjectSetting SubjectKind = "setting"
)

// The fixed names of the auth's sign-in settings, as a row of a setting stores them in its
// SubjectId column. A sign-up setting's is SignUpSetting's.
const (
	// SettingSecondFactor is whether a second factor is required of everyone.
	SettingSecondFactor = "secondFactor"
	// SettingRememberedBrowsers is how long a browser is remembered.
	SettingRememberedBrowsers = "rememberedBrowsers"
	// SettingSelfServiceReset is whether people may ask at the sign-in page for a link
	// that resets their own password.
	SettingSelfServiceReset = "selfServiceReset"
)

// settingSignUpPrefix leads the fixed name of a sign-up setting.
const settingSignUpPrefix = "signUp:"

// SignUpSetting is the fixed name of the setting that says whether the sign-in method
// registered under method is open to sign-up: "signUp:" and the method's name.
func SignUpSetting(method string) string {
	return settingSignUpPrefix + method
}

// Form is which form of an act a row records, where the act has more than one that its
// parts cannot tell apart. Most acts have one form, and their rows store none.
type Form string

// The forms.
const (
	// FormNewTenant is an invitation that makes a tenant (ActInvited).
	FormNewTenant Form = "newTenant"
	// FormWithTenant is an account deleted with its tenant (ActAccountDeleted).
	FormWithTenant Form = "withTenant"
	// FormSeat is the role a tenant's directory gives a person who signs in through it
	// (ActRoleGiven).
	FormSeat Form = "seat"
	// FormMove is a role or a place in a group taken from a person whom another tenant's
	// directory seated (ActRoleTaken, ActGroupMemberRemoved).
	FormMove Form = "move"
)

// RoleScope is where the role an act names is held, as a row stores it in its RoleScope
// column. ScopeOf answers it for a policy scope.
type RoleScope string

// The places a role is held.
const (
	// RoleScopeTenant is one tenant, which the row's tenant columns name.
	RoleScopeTenant RoleScope = "tenant"
	// RoleScopeGlobal is the whole application.
	RoleScopeGlobal RoleScope = "global"
	// RoleScopeEveryTenant is every tenant, those there are and those made later.
	RoleScopeEveryTenant RoleScope = "everyTenant"
)

// ScopeOf answers where a role held in the scope is, as a row stores it, and the tenant
// it is held in: RoleScopeTenant and the domain for one tenant, RoleScopeGlobal and ""
// for the global scope, RoleScopeEveryTenant and "" for every tenant.
func ScopeOf(scope accesstypes.PolicyScope) (roleScope RoleScope, tenantID string) {
	switch {
	case scope.IsGlobal():
		return RoleScopeGlobal, ""
	case scope.IsEveryDomain():
		return RoleScopeEveryTenant, ""
	default:
		domain, _ := scope.Domain()

		return RoleScopeTenant, string(domain)
	}
}

// The processes that act with no person behind them, as a row stores them in its Process
// column. A row of one names no person as its actor.
const (
	// ProcessApplication is the application's own code, outside any request.
	ProcessApplication = "Application"
	// ProcessMigrate is the migrate command, which invites the first administrator.
	ProcessMigrate = "Migrate"
	// ProcessDirectorySignIn is a directory, at a sign-in through it; the row's Directory
	// column names it.
	ProcessDirectorySignIn = "DirectorySignIn"
	// ProcessResetLink is a password reset link, followed.
	ProcessResetLink = "ResetLink"
	// ProcessAddressLink is an address confirmation link, followed.
	ProcessAddressLink = "AddressLink"
	// ProcessInvitation is an invitation, accepted.
	ProcessInvitation = "Invitation"
	// ProcessSignUp is a sign-up.
	ProcessSignUp = "SignUp"
	// ProcessSignInPage is the sign-in page, for a link a visitor asked for there.
	ProcessSignInPage = "SignInPage"
	// ProcessDevice is a device the person approved.
	ProcessDevice = "Device"
	// ProcessPeriodEnd is a period's end, which clears what it ended.
	ProcessPeriodEnd = "PeriodEnd"
)
