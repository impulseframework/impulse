package auditlog

import "github.com/impulseframework/impulse/accesstypes"

// Act is the fixed name of one act of the audit log, as a row stores it in its Act column
// and as a filter of the log names it. Acts lists them all.
type Act string

// The acts, in the order Acts gives them. Each is stored under its own words, and each
// row of one names whom or what it was done to (SubjectKind).
const (
	// ActInvited is an invitation sent, with its roles or to make a tenant.
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
	// starts with.
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
	for _, s := range shapes() {
		if s.form == "" {
			acts = append(acts, s.act)
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

// actShape is one form of an act: how a filter's choice says the act, and how the
// sentence of a row of it is written from the row's parts. A part is written in braces in
// the sentence (what fills each).
type actShape struct {
	act       Act
	form      Form
	words     string // the act as a filter's choice says it; the act's own form alone has them
	sentence  string // the act in words
	ifEmpty   string // a part whose emptiness picks the other sentence; "" for none
	otherwise string // the sentence of a row whose part ifEmpty is empty
}

// movedTail ends the sentence of what a move took from a person.
const movedTail = ": they signed in through the directory of {SeatingTenantName} and belong to it now"

// shapes is every act of the audit log, each followed by its other forms, in the order
// Acts gives the acts.
func shapes() []actShape {
	return []actShape{
		shape(ActInvited, "Invited", "Invited with the {roles}{place}"),
		formShape(ActInvited, FormNewTenant, "Invited to make {a tenant word}"),
		shape(ActInvitationWithdrawn, "Invitation withdrawn", "Withdrew the invitation"),
		shape(ActInvitationSentAgain, "Invitation sent again", "Sent the invitation again"),
		shape(ActInvitationAccepted, "Invitation accepted", "Accepted the invitation"),
		shape(ActPasswordResetLinkSent, "Password reset link sent", "Sent a password reset link"),
		shape(ActPasswordChosen, "Password chosen", "Chose a password"),
		shape(ActPasswordChanged, "Password changed", "Changed the password"),
		shape(ActPasswordRemoved, "Password removed", "Removed the password"),
		shape(ActPasswordChangeRequired, "New password required", "Required a new password"),
		shape(ActIdentityUnlinked, "Sign-in identity unlinked", "Unlinked the sign-in through {OldValue}"),
		shapeOrWhenEmpty(ActAddressChangeAsked, "Address change asked", "Asked to change the email address from {OldValue} to {NewValue}",
			"{OldValue}", "Asked to set the email address to {NewValue}"),
		shapeOrWhenEmpty(ActAddressChanged, "Address changed", "Changed the email address from {OldValue} to {NewValue}",
			"{OldValue}", "Set the email address to {NewValue}"),
		shape(ActUsernameChanged, "Username changed", "Changed the username from {OldValue} to {NewValue}"),
		shape(ActAccountDisabled, "Account disabled", "Disabled the account"),
		shape(ActAccountEnabled, "Account enabled", "Enabled the account"),
		shape(ActAccountDeleted, "Account deleted", "Deleted the account"),
		formShape(ActAccountDeleted, FormWithTenant, "Deleted the account with its {tenant word}"),
		shapeOrWhenEmpty(ActTokenMade, "Token made", "Made the token {NewValue}", "{NewValue}", "Made a token"),
		shape(ActTokenRevoked, "Token revoked", "Revoked a token"),
		shape(ActBrowsersForgotten, "Remembered browsers forgotten", "Forgot the remembered browsers"),
		shape(ActRoleGiven, "Role given", "Gave the role {role}{place}{period}"),
		formShape(ActRoleGiven, FormSeat, "Gave the role {role}, the role its directory's people start with"),
		shape(ActRoleTaken, "Role taken", "Took the role {role}{place}"),
		formShape(ActRoleTaken, FormMove, "Took the role {role}{place}"+movedTail),
		shape(ActRoleEnded, "Role ended", "The role {role}{place} ended with its period"),
		shape(ActGroupMade, "Group made", "Made the group"),
		shape(ActGroupChanged, "Group changed", "Changed the group's description"),
		shape(ActGroupRemoved, "Group removed", "Removed the group"),
		shape(ActGroupMemberAdded, "Put in a group", "Put in the group {GroupName}{period}"),
		shape(ActGroupMemberRemoved, "Taken out of a group", "Took out of the group {GroupName}"),
		formShape(ActGroupMemberRemoved, FormMove, "Took out of the group {GroupName}"+movedTail),
		shape(ActGroupPlaceEnded, "Place in a group ended", "The place in the group {GroupName} ended with its period"),
		shape(ActGroupRoleGiven, "Group given a role", "Gave the group the role {role}{place}{period}"),
		shape(ActGroupRoleTaken, "Role taken from a group", "Took the role {role}{place} from the group"),
		shape(ActDirectoryTaken, "Directory taken", "Took the directory {NewValue} as its own"),
		shape(ActDirectoryRemoved, "Directory removed", "Removed its directory"),
		shape(ActDirectoryOnlyOn, "Directory only on", "Turned directory only on"),
		shape(ActDirectoryOnlyOff, "Directory only off", "Turned directory only off"),
		shape(ActDirectoryOpened, "Opened to its directory", "Opened it to its directory's people with the role {role}"),
		shape(ActDirectoryInviteOnly, "Made invite only for its directory", "Made it invite only for its directory's people"),
		shape(ActDirectorySecondFactorVouched, "Directory's second factor vouched for", "Said its directory asks for a second factor"),
		shape(ActDirectorySecondFactorUnvouched, "Directory's second factor no longer vouched for", "Stopped saying its directory asks for a second factor"),
		shape(ActSignUpOpened, "Sign-up opened", "Opened {SubjectName} to sign-up with the role {role}"),
		shape(ActSignUpClosed, "Made invite only", "Made {SubjectName} invite only"),
		shape(ActSecondFactorRequired, "Second factor required", "Required a second factor of everyone"),
		shape(ActSecondFactorNotRequired, "Second factor no longer required", "Stopped requiring a second factor"),
		shape(ActRememberedBrowsersSet, "Remembered browsers set", "Set how long a browser is remembered to {length}"),
		shape(ActSelfServiceResetOn, "Own password reset turned on", "Let people reset their own password at the sign-in page"),
		shape(ActSelfServiceResetOff, "Own password reset turned off", "Stopped letting people reset their own password at the sign-in page"),
	}
}

// shape is an act's own form: its words as a filter's choice, and its sentence.
func shape(act Act, words, sentence string) actShape {
	return actShape{act: act, form: "", words: words, sentence: sentence, ifEmpty: "", otherwise: ""}
}

// formShape is another form of an act, which a filter's choice says as the act.
func formShape(act Act, form Form, sentence string) actShape {
	return actShape{act: act, form: form, words: "", sentence: sentence, ifEmpty: "", otherwise: ""}
}

// shapeOrWhenEmpty is an act's own form whose sentence is another for a row whose part is
// empty.
func shapeOrWhenEmpty(act Act, words, sentence, part, otherwise string) actShape {
	return actShape{act: act, form: "", words: words, sentence: sentence, ifEmpty: part, otherwise: otherwise}
}

// shapeOf answers the shape of an act's form, and the act's own form for a form the act
// does not have. It answers false for an act that is not one of Acts'.
func shapeOf(act Act, form Form) (actShape, bool) {
	var own actShape
	found := false
	for _, s := range shapes() {
		if s.act != act {
			continue
		}
		if s.form == form {
			return s, true
		}
		if s.form == "" {
			own, found = s, true
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
// SubjectId column. A sign-up setting is the prefix and the sign-in method's name.
const (
	settingSignUpPrefix       = "signUp:"
	settingSecondFactor       = "secondFactor"
	settingRememberedBrowsers = "rememberedBrowsers"
	settingSelfServiceReset   = "selfServiceReset"
)

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
