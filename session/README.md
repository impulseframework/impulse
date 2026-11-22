# Session

## Overview

The Session repository is designed to handle the management of user sessions, including authorization, storage, and expiration. It provides a framework for managing sessions across different databases and supports multiple login types.

## Features

- `Session Management`: Efficient handling of user session creation, storage, and expiration.
- `Database Support`: Seamless integration with multiple databases.
  - PostgreSQL
  - Google Cloud Spanner
- `Auth Sessions`: One session that every sign-in method can establish, with users, explicit identity links, pending confirmation and MFA. See the "Auth sessions (multiple sign-in methods)" section.
- `Sign-in methods` of the Auth session, each in its own package:
  - Username/Password (`session.PasswordSignIn`)
  - Microsoft Entra ID (Azure) OIDC (`session/azure`), admitting one tenant, several, any tenant or Microsoft personal accounts, with role synchronization from the token's roles and provider-initiated logout (see the "Microsoft Entra ID OIDC" section)
  - Google Workspace OIDC (`session/google`), admitting one Workspace, several, any Workspace or any Google account, with role synchronization from Google Groups (see the "Google Workspace OIDC" section)
  - WorkOS SSO (`session/internal/workos`): built and tested, in an internal package, so an application cannot import it
- `The second factor`: every session carries one when the sign-in policy asks (`signin.RequireMFA`): the attestation a sign-in's connection makes, by evidence in the token (Entra's `amr`) or by the application's declaration of a directory it administers (`Attesting()`), or else the library's own factor, a one-time code mailed to the user's address, with the browser remembered afterwards as a trusted device and an administrator's reset. See "The second factor" under the Auth session.
- `Open to sign-up, or invite only`: whether a person with no account and no invitation may sign in is one choice an administrator makes in the running application, for each sign-in method, kept in a table of the auth; every method is invite only until it is opened, and an open method gives a new person its default role. See "Open to sign-up, or invite only" under the Auth session.
- `Invitations, email addresses and sign-up`: an invitation mailed to an address, carrying the roles its acceptance grants, accepted only by the account it was sent to and, wherever the sign-in does not itself prove the invited address, only by a code mailed to that address, or carrying the tenant its acceptance makes, held to an organization's directory where the tenant is entered through its directory only; a user's email address in two states, unverified and verified, the person proving it by a mailed link; and self-service sign-up with a password while an administrator has the password method open to it. See "Users and their email address", "Invitations" and "Sign-up" under the Auth session.
- `A person's first password`: a person who has only ever signed in through a directory chooses a password while signed in, with a code mailed to the address their account holds confirmed, so the password is tied to that mailbox. See "A person's first password".
- `Mail`: one interface the invitation, verification, sign-up and sign-in code flows send through (`session/mail`), with the Google Workspace SMTP relay as the first driver (`session/mail/workspace`). See the "Sending mail" section.
- `Preauth`: trust-the-caller stepping-stone sessions (`session.NewPreauth`).
- `Custom Session Data`: App-defined data attached to each session, resolved atomically at session creation and available to every request. See the "Custom session data" section.
- `Custom User Data`: App-defined durable data attached to the user record — it survives logout, expiry, and regeneration, and dies with the user. See the "Custom user data" section.
- `Login Refusal Codes`: A refused sign-in returns the browser to the login page with a code, never with text; the page maps the code to a sentence it holds itself. See the "Login refusal codes" section.
- `Attempt limiting`: The password method limits failed attempts per name, known or not, in memory and in the store, and refuses an attempt past the limit with `too_many_attempts` and the wait; an after-sign-in hook is told of every session a sign-in establishes. See the "Attempt limiting and the after-sign-in hook" section.
- `Machine outlets and bearer tokens`: a URL space of the application a program calls with a bearer token the Auth issued, never a cookie; the tokens a signed-in person issues for a program of their own, and the access and refresh tokens a command-line client obtains through the device flow (RFC 8628) with the person's approval, and the `session/deviceflow` package a command-line program signs in with. See the "Machine outlets and bearer tokens" section.

Every session type is generic over two data axes, custom session data and custom user
data: `Auth[SessionData, UserData, Tx]` (`Tx` is the storage's transaction type,
inferred from the store) and `Preauth[SessionData]` (Preauth has no user record, so no
user-data axis). An application that uses neither axis instantiates with
the `NoCustomData` sentinel:
`session.NewAuth[session.NoCustomData, session.NoCustomData](store, cookieKey, superAdministrators, mail)`.

## Schema

The DDL for every table the library owns ships as golang-migrate files under
`schema/spanner/` and `schema/postgresql/`, one set per starting point: `migrations`
(the base set: sessions and users), `auth` (Auth sessions on the base set: the sessions'
users, identity links, auth events and pending sign-ins) and `impersonation`. Which sets
a group applies is in "Storage and schema" below.

**Time columns are the database's.** Every column that records when something happened
(a session's `CreatedAt`, `UpdatedAt` and `AuthenticatedAt`, a user's `CreatedAt` and
`LastSignInAt`, an auth event's `OccurredAt`, an impersonation record's `StartedAt` and
`EndedAt`, an identity link's `CreatedAt` and `LastUsedAt`, a pending sign-in's
`CreatedAt` and `ConsumedAt`, a factor code's and a trusted device's time columns) is
written with the database's time, never
the application's: Spanner's commit timestamp, PostgreSQL's transaction time (`now()`).
A host clock that runs ahead of the database's therefore cannot be refused, and the
rows one write stores carry one time. On Spanner the columns carry
`allow_commit_timestamp = true`: the shipped sets mark them (a `CommitTimestamps`
migration in each), and a table created by hand or named per group needs the same
option before the driver writes to it, since a write to a column without it fails:

```sql
ALTER TABLE Sessions ALTER COLUMN CreatedAt SET OPTIONS (allow_commit_timestamp = true);
ALTER TABLE Sessions ALTER COLUMN UpdatedAt SET OPTIONS (allow_commit_timestamp = true);
```

A deadline (`ExpiresAt`) is the application's and is written as given. An auth event
the application hands the library (`sessioninfo.AuthEvent`) carries a zero `At` when it
happens now, and the database stamps it; an earlier event carries its time, stored as
given.

**The users table carries the user-management columns.** `SessionUsers` has, beside
the username, the password hash and `Disabled`, the password change mark
`PasswordChangeRequired` and the time columns `CreatedAt` and `LastSignInAt` (see
"Users and the forced password change"). The shipped `000002_SessionUsers` migration
creates them; a users table named per group takes the same DDL under its name. The
auth set adds the email address columns `Email`, `NormalizedEmail` and
`EmailVerifiedAt` (`SessionUserEmail`), the mail links table (`SessionMailLinks`) and
the second factor's tables (`SessionFactorCodes`, `SessionTrustedDevices`), the
person's name `Name` (`SessionNames`), the tenant an invitation makes, the mail
links table's `NewTenant` column (`SessionTenantInvitations`), the mark that an
invitation's roles are held in every tenant, its `EveryTenant` column
(`SessionInvitationPlaces`), the time a link's message could not be mailed, its
`MailFailedAt` column (`SessionUnmailedLinks`), and the invitation a code was mailed
for, the factor codes table's `InvitationId` column (`SessionCodeInvitations`); see
"Users and their
email address" for the two shapes the address columns take, "A person's name" and
"Invitations".

**PostgreSQL time columns are `timestamp with time zone`.** The driver writes `time.Time`
values as instants, which `timestamptz` stores faithfully whatever zone the host runs in.

## Auth sessions (multiple sign-in methods)

`Auth[SessionData, UserData, Tx]` is one session that any registered sign-in method can
establish: password, Microsoft Entra ID (Azure), Google Workspace, WorkOS SSO and any
OpenID Connect provider known by its issuer. An
application mounts one cookie, one session table and one middleware chain, and every
session belongs to a **user**, a `SessionUsers` record, by its ID. The password
method checks the user's password; an external method produces a verified
**identity** that is linked to a user through the identities table, keyed by
`(method, connection, subject)` and **never by email**. `Tx` is the storage's
transaction type (`pgx.Tx` or `*spanner.ReadWriteTransaction`); `NewAuth` infers it from
the store, and the rules the application gives the `Auth` and its methods are typed to it.

Each redirect-based method is its own package (`session/azure`, `session/google`; the
WorkOS method is `session/internal/workos`, which an application cannot import), built
on the `session/signin`
contract and the shared `session/oidc` machinery, which also holds the generic OpenID
Connect method for a provider with nothing vendor-specific about it; an application imports the packages of the methods it offers and registers
each with its `SignIn` function and `Config`.

| Method | Registration → handlers | Identity key (connection, subject) | Login parameters | Roles |
| --- | --- | --- | --- | --- |
| `password` | `PasswordSignIn(auth, options...)` → `*PasswordMethod`: `Login`, `ChangeUserPassword`, `RemoveUserPassword` | user ID | JSON `{"username", "password"}` | — |
| `azure` | `azure.SignIn(auth, name, rule, &azure.Config{...})` → `*azure.Method`: `Login`, `Callback`, `FrontChannelLogout` | (`tid`, `oid`) | `?returnUrl=`, `?prompt=select_account` | `signin.RoleSync(manager)` or `signin.DisableRoleSync()` |
| `google` | `google.SignIn(auth, name, rule, &google.Config{...})` → `*google.Method`: `Login`, `Callback` | (the Workspace the `Workspaces` slot selects, `sub`) | `?returnUrl=`, `?login_hint=`, `?prompt=select_account` | `google.RoleSync(...)` or `google.DisableRoleSync()` |
| `oidc` | `oidc.SignIn(auth, name, rule, &oidc.Config{...})` → `*oidc.Method`: `Login`, `Callback` | (`Config.Connection`, the issuer URL by default; `sub`) | `?returnUrl=`, `?login_hint=`, `?prompt=select_account` | `signin.RoleSync(manager)` (the `roles` claim) or `signin.DisableRoleSync()` |
| `workos` (internal) | `workos.SignIn(auth, name, rule, &workos.Config{...})` → `*workos.Method`: `Login`, `Callback` | (`connection_id`, `idp_id`) | `?organization=` (required), `?returnUrl=` | never synchronized |

`name` identifies the registration on the `Auth` and must be unique there: one method
may be registered several times under different names, so one `Auth` offers two Entra
tenants as two registrations, each with its own configuration and rules. The password
registration is named `password`. `rule` is the application's own **account rule** for
an identity that is not linked to any user yet, and `nil` is the usual value: the
library applies its **standard account rule** to a first sign-in, which reads what an
administrator chose for the registration in the running application, open to sign-up
or invite only (see "Storage and schema" and "Open to sign-up, or invite only").

### Storage and schema

Each auth group owns eleven tables: sessions and users (the base migrations,
`schema/*/migrations`), identities, auth events, pending sign-ins, sign-in attempts,
mail links, factor codes, trusted devices and sign-in methods (the auth migrations,
`schema/*/auth/migrations`, which also add `UserId`, `AuthenticatedAt`,
`ProviderSessionId` and `ProviderSessionIssuer` to the sessions table and the email
address columns and the person's name to the users table), and impersonations
(`schema/*/impersonation/migrations`) if you use it. A new group applies the base set,
then the auth set.

The mail links table holds the tenant an invitation makes in one nullable column,
`NewTenant`: a JSON document, `{"name", "directoryOnly", "details"}`, null on a
verification link, a sign-up link and an invitation that makes no tenant (see
"Invitations"). The drivers write and read it. An application that stores an
invitation's row in a transaction of its own (`PrepareInvitation`) writes the same
document, which the row answers (`InvitationRow.NewTenantColumn()`), and a mail links
table named per group, or kept in the application's own migrations, carries the column
as the shipped one does.

The mail links table also holds the place an invitation's roles are held in: `TenantId`
for one tenant, `EveryTenant` for every tenant, and neither for the application's
global surface. `EveryTenant` is a boolean that is never null (`EveryTenant BOOL NOT
NULL DEFAULT (false)`; `boolean NOT NULL DEFAULT false` on PostgreSQL), false on every
link that is no invitation. The drivers write and read it on every link, an
application that stores an invitation's row itself writes `InvitationRow.EveryTenant`
as it is, and a mail links table named per group, or kept in the application's own
migrations, carries the column as the shipped one does. A table without it fails the
first link written or read.

The schema keeps its own integrity: a session belongs to an existing user (`UserId`,
null only for a role-principal impersonation), and deleting a user deletes its
sessions, their auth events and its identity links; your custom data tables cascade
from the session and user rows likewise. Sessions are marked expired, never deleted, by
the library: retention is the application's policy, and auth events last exactly as
long as their session rows.

An application whose groups are named applies the sets under the group's names and
tells the `Auth` them: `WithSessionTableName`, `WithUserTableName`,
`WithIdentitiesTableName`, `WithPendingSignInsTableName`,
`WithSignInAttemptsTableName`, `WithMailLinksTableName`, `WithFactorCodesTableName`
and `WithTrustedDevicesTableName` on `NewAuth`, and
`NewAuthEventsTable(name)` / `NewImpersonationTable(name)` on the store. A table name
must be a plain identifier (a letter or underscore, then up to 127 letters, digits or
underscores): the store refuses any other, and the constructor reports it under the
option's name, so a name never reaches SQL unchecked. Name the users
table's unique indexes after the table as the shipped DDL does
(`<UsersTable>_NormalizedUsername_idx` and `<UsersTable>_NormalizedEmail_idx` on
PostgreSQL, `<UsersTable>ByNormalizedUsername`, `<UsersTable>ByUsername` and
`<UsersTable>ByNormalizedEmail` on Spanner): the drivers read a username or an address
conflict by those names. Build the
storage with `NewSpannerAuth` / `NewPostgresAuth`. The application's rules are given to
the `Auth` and to each method,
typed to the storage's transaction (`*spanner.ReadWriteTransaction` or `pgx.Tx`) so
they can read and write the application's own rows in the sign-in's transaction; a rule
written for the other driver's transaction does not compile. On Spanner a rule may run
more than once when the transaction retries,
so it must not have side effects outside it, and a row it writes with a mutation is
buffered until the commit, invisible to a later read in the same transaction: write with
DML what a later rule must read.

- The **sign-in policy** (`signin.Policy[Tx]`) is optional. Without one, a sign-in is
  decided by the library's own checks: the user exists and is enabled, the identity is
  linked or resolved, roles reconcile. An application with rules of its own sets them
  with `auth.SetSignInPolicy(rule)` after `NewAuth` and before serving; the rule
  answers for every method, and for a session the application starts for a user it
  authenticated itself (`StartAuthenticatedSession`), once the user is known, with
  `signin.Allow()`, `signin.Deny(code)` or
  `signin.RequireMFA(factors...)`. The factors are the methods the step may record
  when it completes the sign-in: the library's own, `sessioninfo.MethodEmailCode`, or
  the application's (`"totp"`); the pending status and the `PendingHook` report them,
  and `CompletePending` refuses any other; none accepts any. The library's own steps
  are recorded by the library alone: `CompletePending` and `StartAuthenticatedSession`
  refuse an event under `email-code`, `trusted-device` or an attestation's method
  (`400`), whatever the factors, and an application reports its step under a name of
  its own. They refuse an event under the name of a sign-in the library runs too
  (`password`, `azure`, `google`, `workos`, `oidc`, and the method of every
  registration on the `Auth`): a session's first event says how the person signed in,
  and only a sign-in the library ran says it was a password's or a directory's. A `RequireMFA` is
  satisfied without a step by a second factor the sign-in already carries: the
  attestation its connection makes, or a trusted device of the user; see "The second
  factor". An application whose users belong to tenants sets the library's
  **tenant policy** (`signin.TenantPolicy`), with its own rule chained after it when it
  has one; see "The tenant policy".
- A **super administrator** signs in with a second factor whatever the sign-in policy
  asks of anyone else. No Auth is built without a word on who holds the Super
  Administrator role: it is `NewAuth`'s third argument, so an application that leaves it
  out does not compile. An application whose people hold roles passes
  `session.SuperAdministratorsBy(signin.SuperAdministrators(userManager))`, over the
  access library's user manager, which it opens before the Auth (on Spanner over
  `resource.SignInRoles(userManager)`, as the tenant policy is). The Auth then decides
  every sign-in by the application's policy and, after it, by a rule of its own
  (`signin.SuperAdministratorFactor`): a sign-in the policy allows is held for the code
  the library mails (`sessioninfo.MethodEmailCode`) when the person holds the role, with
  or without a policy of the application's. The directory's attestation and a remembered
  browser satisfy it as for any account, and a refusal, or a hold for a factor the policy
  names itself, stands as the policy decided it. The rule is for an Auth whose roles the
  application gives. A sign-in through a method that synchronizes roles is not held by
  it: there the directory is the authority, on who is a super administrator as on every
  role, and the application's own policy is asked of that person as of anyone (see
  "Role synchronization"). `NewAuth` refuses the argument's zero value and a nil check,
  each saying what to pass. An application with no permission engine, and so no
  role of any kind, passes `session.NoSuperAdministrators()`, and its Auth holds nobody
  to the rule. The auth packages `impulse` writes pass the first, and `impulse check`
  (`super-administrators`) fails an auth package with a role file that passes the
  second, or that builds its check over another auth's engine.
- The **account rules** decide an identity's first sign-in: an identity that is not
  linked to any user yet. A linked identity signs in by its link and never consults
  them. The library applies its **standard rule** inside the sign-in's first
  transaction:
  - **A person who has an account is linked to it.** A user the application created is
    found by the username first and then by the address: the user under the identity's
    **sign-in name** (the user principal name on Entra, the verified email on Google),
    matched without regard to case, or else the user who holds the identity's address
    verified, when the method vouches for the address (Google alone does, and only for
    a Gmail address or a Workspace account's; see "Users and their email address"),
    since a person is one user whichever way they sign in. The link is made after a
    password confirmation when the user has a password. When the user has none and has
    never signed in, it is the user's first identity, and is made when something vouches
    for it: at once where the method vouches for the sign-in name, and otherwise once
    the person enters the code mailed to the address the account holds (see "A first
    link on a name alone" below). A user with no password who
    already signs in through an identity is linked no second one on the name, on the
    same connection or another (see "Never a second identity on a name alone" below).
    An address a user holds unverified is a reservation, not a match.
    The rule asks what the Auth registers. On an Auth with no password method nobody
    can enter a password, so a password a user holds there (one the API set, or one
    kept from a release that registered the method) asks for no confirmation: the user
    is linked as one who has none, by the same checks, and keeps the password.
  - **A person who has none is decided by what an administrator chose for the method**,
    the registration's row of the sign-in methods table, read in the same transaction
    (see "Open to sign-up, or invite only"). A method with no row is **invite only**:
    the person is refused (`not_invited`) and nothing is written, unless the sign-in
    carries an invitation, which is then the account, with the invitation's roles. A
    method an administrator **opened to sign-up** gives the person an account under
    their sign-in name and the default role the row names, through the application's
    hook, in the transaction that makes the account. Nothing in code opens a method.
  - **A method whose directory writes the roles** (role synchronization) has no such
    choice: the directory says who holds a role, so a person whose token asserts one is
    given an account, and a person whose token asserts none is refused (`no_roles`).

  A new account holds no address until the person verifies one, a reserved address
  staying with the account it was entered for. A registration's own rule that
  provisions a new user for an identity whose verified address another user holds
  verified is refused (`identity_rejected`): never a second user for one person.

  In a tenanted application the **tenant's declaration decides a stranger first**.
  With `auth.SetDirectoryAdmission(directoryLookup, seatHook)` (see "The
  tenant policy"), a person no user was created for, from a tenant's directory, is
  decided by that tenant's declaration before the registration's rule is reached. A
  directory belongs to one tenant, so one declaration decides. A person an account
  exists for already, because the application gave them a role ahead of their first
  sign-in or another tenant invited them, is not such a person: the sign-in links to
  that account, under the sign-in name. That link is their first sign-in through the
  directory, so the tenant's default seat is given at it too, through the hook, in
  the transaction that makes the link, whether the link is made at once or after
  their password confirmed it.

  **A first link on a name alone needs something to vouch for it.** An account created
  ahead with no password, which has never signed in (one the application created
  with none), has neither a password to confirm a link with nor a link to compare a
  new one to: nothing but its name ties it to whoever signs in next under that name,
  and a directory asserts the names it likes (an organization's own OpenID Connect
  provider is held to no domain). The link hands whoever the directory signs in
  everything the account holds. So in a tenanted application the identity is linked to
  such an account only when the sign-in came through the directory of the one tenant
  the account belongs to (the hook's `OnlyIn`: the account holds a role, or a place in
  a group, in the tenant itself, and nothing in another tenant, in every tenant or
  outside the tenants, so the tenant's directory is given nothing the tenant's own
  administrators could not give, and a role a tenant's administrator gives an account
  that holds something elsewhere changes nothing), or when it carries the account's
  own invitation, the one mailed to the account's address, which must still be live;
  a disabled account accepts no invitation, so its own vouches for nothing. A person
  created ahead in two tenants, or holding a role in every tenant or outside them, is
  therefore linked at a first sign-in only with their invitation, or once they have a
  password to confirm with. The deployment's own first administrator, who holds roles
  for the whole application and belongs to no tenant, is no such account: the
  deployment creates none ahead of the person and invites them, and the invitation is
  what ties their first sign-in through a directory to the roles, however many
  organizations the method admits (see "The first administrator's invitation").
  Any other sign-in under the name is refused as a
  person no account was created for is, with that refusal's own code and words, which
  say nothing of the account (`invitation_only` through a directory some tenant has,
  `not_invited` through any other), and nothing is linked, seated or accepted. An
  account with a password keeps the confirmation above, and one that already signs in
  through an identity is linked no second one on the name (below). The rule is the standard
  rule's own: a registration's custom rule that answers `signin.Link` links on its own
  evidence, as before, and a registration of the generic OpenID Connect method that
  sets `Config.SeededUsers` declares its provider's names the application's own and
  links such an account on the name. It is for the provider a deployment runs for the
  accounts it seeds, never for an organization's directory, whose names are the
  organization's to assert. An account an application's seed creates with no password
  and with roles in every tenant or in none, as the cast of the persona sign-in is,
  signs in through such a registration; through any other it has no tenant whose
  directory could vouch for it and is linked only with its own invitation. An
  application whose
  tenants have no directories (no `SetDirectoryAdmission`) has only the directories its
  deployment registered for itself, and no tenant to vouch for a link. There the link
  is made at once only where the method vouches for the sign-in name: Google names an
  account by its address, and answers for a Gmail address and for a Workspace
  account's. Any other name is the provider's word alone (a Google account can be made
  under any address from outside Google, an identity provider's administrators give
  the names they like, and Microsoft Entra ID answers for no address), so the sign-in
  waits for the code mailed to the address the account holds, whether or not its
  person has confirmed it (`PendingMFA`, the email code its one factor, and no other
  address taken). Nothing is linked while it waits, so a sign-in that never finishes
  leaves the account as it was; the person who enters the code is linked and signed
  in, and the code confirms the address. An account that holds no address has nowhere
  to mail the code, and an address given at the sign-in would show only that the
  person reads their own mail: that sign-in is refused (`identity_rejected`,
  `signin.ErrLinkNeedsAddress`) and nothing is linked, so an account made ahead of
  its person is made with their address (`CreateUserRequest.Email`). The wait is for
  the account it began with, and its code is good only for the address that account
  holds when the code is entered. A code mailed to an address that has since been
  taken off the account, replaced by another (confirmed or not) or moved to another
  account is dead (`code_expired`, as a code that ran out is): nobody is linked on it,
  the address it was mailed to is not written back, and the step offers a new code at
  the address the account holds now. An account whose address is taken away while a
  sign-in waits takes none from the person who waits: the step's `EnrollEmail` is
  refused (`409`, saying that an administrator gives the account its address) and
  nothing is mailed, so the person signs in again once the account holds one. A
  sign-in name that moves to
  another account while a sign-in waits links nobody: the code answered for the first
  account's address and for no other's, so the sign-in that carries it is refused
  (`identity_rejected`) before anything is written, and neither account gains a link. A
  declaration naming a **default seat** admits a stranger, who is provisioned and
  seated with it in the tenant, through the hook, in the transaction
  that creates the account and links it. The hook is called once for a link, never
  for a sign-in that finds the link, and the application's hook gives the seat only
  where the person holds none in the tenant (the generated identity client's does),
  so a seat an administrator removed is not given back by signing in, and a link that
  is deleted and made again gives it again. A seat the invitation the sign-in consumes names too (the
  same role in the same tenant) is given once, by the invitation, and is left out of
  what the hook is handed. A declaration naming none admits strangers by invitation only, the
  invitation the sign-in carries being the account as through a method that is invite
  only, and without one the sign-in is refused
  (`invitation_only`) and nothing is written, whether the method is open or not. A
  directory no tenant declares falls to what was chosen for the method, as above.

  **Never a second identity on a name alone.** A name proves nothing about who holds an
  account. Where a username is not an address, a person may sign up under a colleague's
  sign-in name, sign in through the directory as themselves (the account holds their own
  address, so the directory's sign-in finds it and the password confirms the link) and
  remove the password. The account then has no password to confirm a link with and one
  identity on the directory's connection, and the colleague's first sign-in through
  that directory arrives under the account's name; with a method that writes one
  connection for every account (any Google account, Microsoft personal accounts) every
  account shares the connection. So the standard rule links an identity the application
  has not seen, on its sign-in name, to a user without a password only as that user's
  first identity. Where the user already signs in through one, the sign-in is refused
  (`identity_rejected`: `signin.ErrLinkSecondIdentity` on the same connection,
  `signin.ErrLinkCrossConnection` from another) and nothing is linked. A second identity
  is linked on proof instead: a user who holds, verified, the address the method
  verified for the identity is the person, and is linked on a connection they already
  sign in through, as before; a user with a password confirms the link with it, as
  above; a registration's own rule that answers
  `signin.Link` links on its own evidence, as before; and the application may link an
  identity itself (the store's `LinkIdentity`). A directory that issues a person a new
  subject under the same name (their directory account deleted and made again) is
  therefore followed only where the account holds the address verified and the method
  verifies it; elsewhere the sign-in is refused until the new identity is linked one
  of those ways.

  The standard rule writes nothing for a guest of the directory (an Entra B2B guest,
  refused with `guest_account`: a guest's preferred username is an address the
  directory did not verify), for a token asserting no application role when the method
  synchronizes roles (`no_roles`), or for an identity without a sign-in name
  (`no_email`). The tenant key on the link is the organization the registration names
  for the account (`Identity.Organization`): on Entra the tenant id, or the name
  `azure.NamedTenant` gives it; on Google the Workspace's name, the account's domain
  under any Workspace, `google.AnyAccountConnection` under any Google account.
- A registration's own **rule** (`azure.AccountRule`, `google.AccountRule`,
  `oidc.AccountRule`) is given to its `SignIn` function and runs
  first, for what the standard rule cannot know: it receives the method's own identity
  type (`*azure.Identity`, `*google.Identity`), decoded from the verified claims with
  the raw claims on it, and answers `signin.Reject(code)`, `signin.Link(userID,
  trusted)`, `signin.Provision(newUser, onProvisioned)`, `signin.Confirm(userID)` or
  `signin.Standard()`, which leaves the identity to the standard rule once the rule's
  own checks have passed; `signin.Tenant(key)` on any of the last four stores the
  application's tenant key on the link. A `Link` is the rule's own evidence, and the
  library asks for more when it can: a user who has a password must confirm with it
  (`Confirm`) unless the link is `trusted`; a user without a password cannot confirm,
  so an identity from another connection than the ones that user already signs in
  through is refused unless the link is `trusted`. A rule's link on a connection the
  user already signs in through is made on the rule's word, which the standard rule's
  match by name does not have. Never choose the user by an email or
  another claim the provider does not verify. On an Auth that registers no password
  method nobody can confirm, so a rule's `Link` to a user who holds a password is
  decided as a link to a user without one. A rule's own `Confirm` is refused there
  (`identity_rejected`, `signin.ErrConfirmNeedsPasswordMethod`): the sign-in would wait
  for a password nobody can enter, so it ends on the login page with the code, nothing
  is linked, and the server logs one error line that names the sign-in method and says
  what to change. Register the password method on such an Auth, or have the rule answer
  `Link` or `Reject`.

A sign-in runs in two transactions, the same on Spanner and PostgreSQL:

1. **User resolution** (external methods): a linked identity is its user's;
   otherwise the account rules run, the registration's own first and then the standard
   one, and the outcome is applied: the link, or the new user, its link and the rows
   `Provision`'s `onProvisioned` writes. A link decision
   holds the user against another link decision until the transaction ends
   (PostgreSQL: a transaction-scoped advisory lock keyed by the user, which holds
   neither session inserts nor a rule's own reads of the user; Spanner: the
   transaction is serializable), so two racing sign-ins from two connections cannot
   both link a password-less user. A transaction the database ends to break a deadlock
   or a serialization failure runs once more.
2. **Decision and session**: the user is read (a disabled one is refused), the
   sign-in policy runs, and the session row is inserted with its auth events and custom
   session data.

Each transaction **commits unless a hook or the storage returns an error**. A `Reject`,
`Confirm`, `Deny` or `RequireMFA` answer is not an error: it commits the hook's own
writes (a record of a refused sign-up, an audit row) and writes no session. Because the
resolution has committed by the time the policy runs:

- the policy (and the custom session data resolver) **reads the user a sign-in just
  provisioned** and the rows `onProvisioned` wrote, on both backends;
- `req.User` (`*sessioninfo.SignInUser`) tells them which user was resolved and
  how: `Source` is `UserNamed` (password), `UserExistingLink`, `UserNewLink` or
  `UserProvisioned` (`req.User.Provisioned()`), with the link's `Tenant`;
- **a user a rule provisioned stays, linked, when the policy then denies the
  sign-in or holds it for MFA**; the next sign-in goes through the link and the policy
  decides again. Return `signin.Reject` from a custom rule when the user must not be
  created at all. A hook error rolls back its own transaction only: an `onProvisioned`
  failure leaves no user, while a policy error leaves a provisioned user in place.

```go
// The application's rules, typed to the store's transaction.
func signInPolicy(ctx context.Context, tx *spanner.ReadWriteTransaction, req *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
    if req.User.Provisioned() { // a first sign-in through SSO: the MFA step enrolls the person
        return signin.RequireMFA("email-otp"), nil
    }

    return signin.Allow(), nil
}

// Optional: the application's own rule for an Entra identity's first sign-in. The
// library's standard rule handles the usual case; this one refuses what the standard
// rule cannot know and leaves the rest to it.
func entraAccountRule(ctx context.Context, tx *spanner.ReadWriteTransaction, identity *azure.Identity) (*signin.Resolution, error) {
    partner, ok, err := partnerOfTenant(ctx, tx, identity.TenantID) // the application's own rows
    if err != nil {
        return nil, err
    }
    if !ok {
        return signin.Reject("not_a_partner"), nil
    }

    return signin.Standard(signin.Tenant(partner)), nil
}

events, err := sessionstorage.NewAuthEventsTable("SessionAuthEvents")
store := sessionstorage.NewSpannerAuth(client, sessionstorage.WithAuthEvents(events))

// Who holds the Super Administrator role, over the access library's user manager: a
// holder signs in with a second factor whatever the policy asks of anyone else.
superAdministrators := session.SuperAdministratorsBy(signin.SuperAdministrators(resource.SignInRoles(userManager)))

// How the Auth sends mail: every Auth does, so the settings are an argument (see
// "Sending mail").
mail := session.MailSettings{Sender: sender, Application: "Harbor", LoginURL: "https://harbor.example.com/login"}

auth, err := session.NewAuth[MyData, session.NoCustomData](store, cookieKey, superAdministrators, mail,
    session.WithCookieName("partner_auth"),
    session.WithIdentityLinked(notifyLinked), // called after every identity link
    session.WithPendingHook(onPending),       // called when a sign-in becomes pending
    session.WithPendingTimeout(10*time.Minute),
    session.WithPendingCookieName("auth-pending"),
)
auth.SetSignInPolicy(signInPolicy) // optional: without it the library's checks decide
auth.SetSignUpSeated(giveDefaultRole) // gives a person who signs up through an open method its default role

// Register the sign-in methods before serving; each returns its own handlers.
password := session.PasswordSignIn(auth) // *session.PasswordMethod
sso := azure.SignIn(auth, "entra", entraAccountRule, &azure.Config{ // *azure.Method; nil leaves first sign-ins to the standard rule alone
    Tenants: azure.Tenant(cfg.EntraTenantID), ClientID: cfg.EntraClientID, ClientSecret: cfg.EntraClientSecret,
    RedirectURL: cfg.SSORedirectURL, RoleSync: signin.DisableRoleSync(), LoginURL: "/login",
})
```

### Open to sign-up, or invite only

Whether a person with no account and no invitation may sign in is not written in code.
It is one choice an administrator makes in the running application, for each sign-in
method: **invite only**, or **open to sign-up**. The choice is a row of the sign-in
methods table (`SessionSignInMethods`, under the group's prefix), keyed by the name the
method is registered under:

| Column | What it holds |
|---|---|
| `Name` | The registration's name (`password`, `google`, `entra`, ...). |
| `OpenSignUp` | Whether the method is open to sign-up. |
| `OpenedFor` | Whom the method admitted when it was opened, as its registration spells its audience (`signin.Audience.Key()`): what identifies each organization and each kind of account it admits, never a name of theirs. Null for a row that keeps none. The row opens the method for that audience and no other. |
| `DefaultRole` | The role a person who signs up through the method is given, or null. An application without tenants names one for every open method; an application with tenants names none, since a person who signs up there creates a tenant of their own. |
| `UpdatedAt` | When the row was written, by the database's clock. |

**A method with no row is invite only**, so a new application, and a method added to
one, admits nobody an administrator did not invite or create. The sign-in reads the row
in the transaction that would make the account, so the choice and the account are never
out of step: a method made invite only refuses the next person, and a link mailed to a
person who started a password sign-up while the method was open makes nothing once it
is not.

What open admits is how wide the registration is, which stays in code: a method limited
to the organizations it names (`azure.Tenant`, `google.Workspace`, an `oidc` provider)
admits anyone with an account there; `azure.AnyTenant`, `azure.PersonalAccounts`,
`google.AnyWorkspace`, `google.AnyGoogleAccount` and the password method admit anyone
who has such an account. `auth.API().SignInMethods()` lists the registrations with
that width in words (`signin.Audience.Words()`: "anyone with an account at
lakeside.edu", "anyone with a Google account"), for the page where an administrator
makes the choice and the warning they confirm. A method that synchronizes roles from
its directory has no choice to make (`RegisteredMethod.SyncsRoles`), and neither has a
registration that carries an account rule of its own (`RegisteredMethod.OwnRule`): the
application's code decides who signs in through it.

The administrator who opens a method confirms a sentence that names whom it admits, and
the row keeps that audience (`OpenedFor`). The row opens the method only while the
registration's audience is the one it was opened for: a registration that code widens
afterwards, from the organizations it named to any organization, say, or by one more
organization, is invite only again until an administrator opens it for the audience it
has now, and so is one that code narrows or changes in any other way.

Two audiences are compared by what identifies the people, never by what they are
called (`signin.Audience.Identities`): a Microsoft Entra tenant by its tenant id, a
Google Workspace by its domain, a generic OpenID Connect provider by its issuer, and
each kind of account a method admits outside its organizations by its kind. A name is
the application's to give, and a second organization can be given the first one's: a
tenant listed beside another under the same label is one organization more, and a
tenant put in another's place under its name is another audience, so each is invite
only until it is opened again. An organization that is renamed, or listed in another
order, is the same audience, and the method stays open. A row that keeps no audience
opens nothing, and neither does a row whose `OpenedFor` is not the key of an audience.
A sign-in method an application builds itself (`signin.Method`) gives its identities in
`Audience.Identities`; one that gives none is compared by the names it gives, which
then must be its organizations' alone.

| The person | The method is invite only | The method is open |
|---|---|---|
| has an account | signs in | signs in |
| carries a live invitation | gets an account with the invitation's roles | the same |
| has neither | refused (`not_invited`), nothing written | gets an account, and the default role the row names |

The default role is given by the application, through the hook it sets once at
startup, inside the transaction that makes the account, so the account and the role are
written together or not at all:

```go
auth.SetSignUpSeated(func(ctx context.Context, tx *spanner.ReadWriteTransaction, userID impulse.UUID, username, method, role string) (func(ctx context.Context), error) {
    // Write the role for userID through tx. What must happen once the transaction has
    // committed (telling a permission engine that keeps roles in memory) is returned.
    return nil, giveRole(ctx, tx, userID, role)
})
```

An open method whose row names a role on an Auth with no hook fails the sign-in in its
transaction, and no account is made holding nothing. The auth packages `impulse` writes
set the hook, write the table with the group's other tables, and give every application
the page where the choice is made; an application that wires its Auth by hand writes
the row with `sessionstorage.AuthStore.SetSignInMethod`, with `OpenedFor` the key of the
registration's audience (`auth.API().SignInMethods()` answers each registration's), and
lists them with `SignInMethods`. `session.WithSignInMethodsTableName` renames the table.

Open to sign-up or invite only is one of the choices that add up to an auth's sign-in
shape. The eight shapes that have a name, and every choice they are made of, are
described in [docs/sign-in-shapes.md](../docs/sign-in-shapes.md).

### The tenant policy

The tenant policy is the library's sign-in rule for an application whose users belong
to tenants: a person signs in when one of their tenants admits the way they signed in.
It is built from two things the application gives it and set like any other policy:

```go
auth.SetSignInPolicy(signin.TenantPolicy(userManager, tenantDeclaration))
```

- **The person's memberships**, read through `userManager`, the access library's user
  manager (`signin.UserRoleManager`, the same store role synchronization reconciles
  through). The store names a person by the id of their account, and the policy asks
  it for the user the sign-in resolved to. The person's tenants are the domains where they hold a role; a role held in
  the global partition or in every tenant domain makes the person the application's own
  staff.
- **Each tenant's declaration**, read through `tenantDeclaration`, a
  `signin.DeclarationReader[Tx]` the application writes over its own tenant record,
  typed to the store's transaction like the other hooks and run in the sign-in's:
  `func(ctx, tx, tenant accesstypes.Domain) (*signin.TenantDeclaration, error)`. A
  declaration names the **directories** the tenant admits its people through
  (`signin.Directory`: a method and the connection its identities carry, which is the
  Entra tenant id, the name the registration gives a Google Workspace, or a WorkOS
  connection id) and whether its people come through those **only**
  (`DirectoryOnly`). A declaration restricts that tenant's own door and nobody else's:
  a tenant that is not directory only, and a tenant that declares nothing (a nil
  declaration), admits everyone it seats, however they signed in.
- **A first sign-in through a directory by a person no user was created for** is
  decided before the policy, by the account rules, and in a tenanted application by
  the tenants' declarations first, through
  `auth.SetDirectoryAdmission(directoryLookup, seatHook)`.
  `directoryLookup`, a `signin.DirectoryReader[Tx]`, reads the declaration of the
  tenant whose directory the identity came through, with its **default seat**
  (`signin.DeclaredDirectory`: the tenant and the role, `""` for none; a directory
  belongs to one tenant, which the application's schema holds, so the answer carries
  one tenant at most), in the sign-in's first transaction; `seatHook`, a
  `session.DirectorySeatedHook[Tx]`, is the application's two functions over a
  person's roles. Its `Seat` seats the person with the seat the declaration
  names (`signin.DirectorySeat`), inside the transaction that links the identity to
  the user, which it is handed, so the link is made and the seat given together or
  neither is, under the contract the invitation hook has (see "Invitations"). Its
  `OnlyIn` answers whether a tenant is the only place a person holds anything, read in
  that transaction, which the library asks before it links an identity on its name
  alone to an account that has never signed in (see "A first link on a name alone"
  above). The generated identity client answers both.
  `Seat` is called at a person's first sign-in through the directory, the one that
  makes the link, whether the account is created in it or stood before: a person
  given a role ahead of their first sign-in, or invited by another tenant, has an
  account, which the sign-in links to after the pending confirmation
  (`Pending().Confirm()`) when it holds a password, and at once when it holds none
  and the link is vouched for, and the hook
  runs in the transaction of that link (the storage's `LinkConfirmedIdentity` for
  the confirmed one, which an `AuthStore` of the application's own must implement).
  It is called once for a link: a later sign-in finds the link and calls nothing.
  Whether the person is seated is the hook's to decide over what it is handed: the
  generated identity client's gives the seat where the person holds no role in the
  tenant, so a seat an administrator removed is not given back by signing in, and a
  link that is deleted and made again gives it again. A personal account, one of the
  two connections that stand for everyone who is in no organization
  (`signin.Directory.PersonalAccounts`: any Google account,
  `signin.GoogleAnyAccountConnection`, and Microsoft's personal accounts,
  `signin.MicrosoftPersonalAccountsConnection`), is no tenant's directory: no
  declaration is read for it, and it falls to the registration's account rule. A
  default seat admits and seats a stranger, so rule 5 below then finds the
  membership. A stranger who
  carries an invitation into the tenant that names the default seat's role is given
  it once, by the invitation hook: the two hooks write in one transaction and neither
  reads what the other wrote, so the seat hook is handed only the seats the
  invitation does not name, and is not called for none. A declaration naming
  none refuses a stranger by invitation only (`invitation_only`), with no account
  created, unless the sign-in carries an invitation. A directory no tenant declares
  falls to what an administrator chose for the method. Without the three, a stranger
  is that choice's alone, and a method opened to sign-up gives them an account holding
  what its row names: the default role, or nothing.

The rules, in order, once the user is known:

1. A super administrator, a person holding the Super Administrator role
   (`accesstypes.SuperAdministrator`, which the access library defines in every auth and
   which is held in the global partition alone), is admitted on the library's own
   checks, whatever any tenant declares: the role is the way back in, and no tenant's
   sign-in settings shut its holder out. The policy and
   the session's reach into a tenant ask the question through one function
   (`accesstypes.RoleCollection.HoldsSuperAdministrator`). A sign-in whose method
   synchronizes roles is admitted here too, since the directory names the person's
   roles and the synchronization, which runs once the session stands, refuses a person
   with none. The application's own directories are admitted by the registration's
   organization slot already.
2. A tenant's own administrators keep a way into their tenant. A person who signs in
   with a password and administers a tenant that is directory only is not refused, and
   the sign-in is held for the library's second factor, the code it mails
   (`RequireMFA` with `sessioninfo.MethodEmailCode`), whatever else would have admitted
   it and whatever the application's own rule asks of anyone; a browser the person
   asked to be remembered passes it for the usual period. A tenant whose directory is
   broken, or was set wrong, would otherwise be a tenant nobody in it could mend. See
   "A tenant's administrators and a directory-only tenant" below for who that is.
   Nobody but a super administrator and a tenant's own administrators, in their own
   tenant, enters a directory-only tenant another way than through its directory.
3. A person holding a role outside any one tenant is admitted: in the global partition,
   which is no tenant's and which no declaration governs, or in every tenant domain,
   whose tenants the policy has no list of. It is no exemption: the session then
   reaches a tenant only where the tenant's declaration admits it, as anyone's does.
4. A person who is a member of no tenant is refused (`no_tenant`), unless the policy
   was built with `signin.AdmitWithoutTenant()`, which the self-service shape of an
   application needs: a stranger signs in, then creates their organization and its
   first membership, and until then the session reaches no tenant.
5. The sign-in is admitted when one of the person's tenants admits it
   (`signin.TenantDeclaration.Admits`). A tenant that is not directory only admits
   every sign-in: a password, a personal account, a session the application starts for
   a user it authenticated itself, and a sign-in through any organization's directory,
   the tenant's own or another's. A tenant that is directory only admits a sign-in
   through its own directory and nothing else.
6. Otherwise every tenant of the person is directory only and none has the way they
   signed in as its directory: nothing would be reachable, and the sign-in is refused.
   A sign-in through a directory (Entra, Google, WorkOS, an OpenID Connect provider)
   is refused with `connection_not_in_tenant`. A sign-in with a personal account
   (through Google or Microsoft, with an account that is in no organization's
   directory, so no tenant's) is refused with `directory_only_personal_account`. Any
   other, a password or a session the application started itself, is refused with
   `directory_only`. The codes are apart so that a login page says what is true of how
   the person signed in: a person who used no password is not told to stop using one.

Otherwise the sign-in is allowed. `signin.Policy.Then` chains the application's own
rule after the tenant policy, for what the tenant policy cannot know:

```go
auth.SetSignInPolicy(signin.TenantPolicy(userManager, tenantDeclaration, signin.AdmitWithoutTenant()).Then(ownRule))
```

The application's rule is asked after an `Allow`, and its decision is the answer. It is
not asked after a `Deny`, which stands as it is. It is asked after a `RequireMFA` too,
so a person the tenant policy holds for a second factor (a tenant's administrator
signing in with a password) is still refused by the rule that refuses them: its
refusal stands, a hold of its own stands (the application names its factor, as it does
for a super administrator), and when it allows, the tenant policy's hold is the answer.

**A tenant's administrators and a directory-only tenant.** The exemption of rule 2 is
narrow on every side, and one function decides it for the sign-in and for the session's
reach alike (`signin.TenantDeclaration.AdmitsAdministrator(method, secondFactor,
administers)`):

- **Who.** The person holds, in that tenant itself and inside its period, one of the
  roles the application names as a tenant's administrator's
  (`access.WithTenantAdministratorRoles`, which the generated auth package sets from
  its `AdministratorRoles()`): by a membership of their own in the tenant's domain, or
  through a group kept in that tenant, whose people and roles are written in the tenant
  and so give nobody what a membership there would not. A role held in every tenant
  does not count, and neither does one held through a group kept for the whole
  application: those are how the application's own people are seated everywhere, and
  a tenant's switch shuts them out with everyone else. A role that inherits from an
  administrator's role administers nothing: the application names the roles
  themselves. The sign-in asks the role store
  (`signin.UserRoleManager.TenantsAdministered`), and every request asks the
  permission engine's snapshot (`access.Client.UserAdministersTenant`), both through
  one statement of where a role must be held to count.
- **How they signed in.** With a password, and nothing wider: an administrator who came
  through another organization's directory, or with a personal account, is kept out of
  the tenant as anyone is.
- **The second factor.** The session carries one, an auth event after the first that is
  not itself a way of signing in (`sessioninfo.SessionData.SecondFactor`): the mailed
  code, a remembered browser, an attestation, or a step of the application's own. A
  password session without one reaches no directory-only tenant, whoever holds it.
- **For how long.** While the role is held: it is asked again at every request, so a
  role taken away, or one whose period ends, closes the way at the next request and not
  at the next sign-in.

The same person's password session reaches their other tenants only by those tenants'
own declarations, unchanged.

The policy decides what the session may be and never writes. Which of the person's
tenants the session then reaches is decided again, tenant by tenant, where the
application computes the session's tenant list: `signin.TenantDeclaration.Admits(method,
connection)` is the same decision over one tenant, with no transaction and no reads,
and the session says how it was established through
`sessioninfo.DataFromCtx(ctx).EstablishedBy()`, its first auth event, whose method and
connection are the sign-in's (`sessioninfo.AuthMethod.ThroughDirectory` tells a sign-in
through a directory from the application's own). A session reaches every tenant that
seats the person, except a tenant that is directory only and whose directory is not the
one the person signed in through; such a tenant is still reached by its own
administrator's password session that carries a second factor
(`TenantDeclaration.AdmitsAdministrator`, above). So a person seated in a directory-only tenant and in
a tenant that is not signs in by password and reaches the second; signed in through the
first one's directory they reach both. A person invited into one tenant who signs in
through another tenant's directory reaches both, unless the tenant that invited them is
directory only. A member of two directory-only tenants reaches the one whose directory
they came through.
A refusal is a `sessioninfo.LoginRefusal` with its code, and `errors.Is` matches
`signin.ErrSignInDenied` and the rule's own cause (`signin.ErrNoTenant`,
`signin.ErrConnectionNotInTenant`, `signin.ErrDirectoryOnly`); a role store or reader
error fails the sign-in (`internal_error`).

A tenant's directory is an organization's: `signin.Directory.Organization` reports a
directory method with a connection that names one organization, so neither a password
nor a personal account (`signin.Directory.PersonalAccounts`, the two connections every
account outside an organization carries). `Admits` and the policy treat a personal
account as they treat a password: no tenant can have it as its directory, so it reaches
a tenant unless the tenant is directory only. A tenant's directory can be any directory
method the application registers: Entra, Google Workspace, WorkOS, or an organization's
own OpenID Connect provider (`sessioninfo.AuthMethod.ThroughDirectory`). The session read (`Authenticated()`)
answers `directory`, the method and the connection of the organization's directory the
session was established through, and leaves it out for every other session: it is what
an application offers a tenant's administrator to make the tenant's directory, which is
set from the sign-in and never typed (the resource package's reference, "One directory,
set from a sign-in").

### Registering sign-in methods

A sign-in method is registered on the `Auth`, and its registration returns the method's
own handlers; routes are wired from that value. The handler is the proof of
registration: a method that was not registered has no handlers to wire, and each method
is its own type, so a handler exists only on the method it belongs to
(`FrontChannelLogout` only on `*azure.Method`). A registered method with no route is
harmless.

Registration is refused, with a panic, in one place, the way `http.ServeMux` refuses a
duplicate pattern: these are wiring mistakes, found when the application starts.

- **A registration name used twice** on one `Auth`, by the same method or another, or a
  registration without a name.
- **A registration name that cannot name its state cookie.** Each registration keeps its
  own state cookie, `OIDC-<session cookie name>-<name>`, so the name is letters, digits
  and hyphens (`lakeside-entra`): two registrations of one method, logins with different
  providers in one browser, and two `Auth`s on one host with a registration of the same
  name never read each other's state, and a callback completes only the login its own
  registration began.
- **A method named after a step the library runs**: a registration whose `Method` is
  `email-code`, `trusted-device`, `attested-by-evidence` or `attested-by-declaration`.
  A sign-in's first auth event carries its method's name, and the library's rules read
  those events as its own proof, so a method of the application's own takes a name of
  its own (`"partner"`). A registration also has one method name: one whose `Method` is
  not the `Name` of the method it builds is refused. The identities a method verifies
  are held to that name at every sign-in: one under another method's name, or under
  one of those four, is refused as the server's error (`internal_error`), and nothing
  is linked or held.
- **A method registered once the `Auth` is in use**: after it has served a request
  through any of its handlers or middleware, or read its methods to check or hash a
  password through its API (`CreateSessionUser` before `PasswordSignIn(auth,
  HashAlgorithm(...))` would have hashed with the default). Register every method, then
  build the router and serve.
- **A registration that synchronizes roles beside any other method**, in either order
  of registration. Role authority is the `Auth`'s, not a registration's: the `Auth` is
  one users table, one role file and one administration surface, so when a directory
  writes the roles it writes them for everyone in the table, and a second way in would
  be a second authority over the same people (a person it provisions holds nothing at
  their first sign-in, and the administration surface refuses to seat them because the
  directory will not). A registration with `RoleSync` stands alone on its `Auth`; two
  populations with two authorities are two `Auth`s. The refusal names the two ways out:
  an `Auth` of its own for the registration, or `DisableRoleSync` so the application
  assigns the roles.
- **A misconfigured method**: Azure or Google without its role sync slot, or with a slot
  missing its manager (or, for Google, its group prefix or lookup); an Azure
  registration without its `Tenants`, a tenant id that is not a GUID, Microsoft's
  consumer tenant listed as a tenant, a tenant listed twice or a name used twice, or an
  `Authority` that is not a base URL; a Google registration without its `Workspaces`, a
  Workspace without a name or a domain, a value that is not a domain name, a name used
  twice or a domain in two Workspaces; role synchronization with other than one tenant
  or one Workspace; a generic OpenID Connect registration without an `Issuer`, with
  one that is not an absolute URL, or without its role sync slot; cookie or session
  options passed to `PasswordSignIn` instead of `NewAuth`.

### Routes

```go
// A machine outlet and the device flow's public endpoints sit outside StartSession:
// a bearer token is the credential there, no cookie is read or written.
droids := session.RegisterMachineOutlet(auth, "droids")
r.Route("/api/droids", func(r chi.Router) {
    r.Use(droids.ValidateToken)
    // …the outlet's routes
})
r.Post("/api/oauth/device/authorize", auth.Device().Authorize()) // client_id, outlet
r.Post("/api/oauth/token", auth.Device().Token())                // grant_type device_code | refresh_token

r.Use(auth.StartSession, auth.SetXSRFToken)
r.Get("/api/user/authenticated", auth.Authenticated())
r.Get("/api/user/sso/login", sso.Login())       // ?organization=…&returnUrl=…
r.Get("/api/user/sso/callback", sso.Callback())
r.Get("/api/user/pending", auth.Pending().Status())
r.Get("/api/user/methods", auth.Methods(&session.MethodList{ // what the login page offers
    Methods: []session.AdvertisedMethod{
        {Name: "password", Label: "Password", Path: "/api/user/session"},
        {Name: "workos", Label: "Single sign-on", Path: "/api/user/sso/login"},
    },
    ChangePassword:   "/api/user/password",
    PasswordStrength: "/api/user/password/strength",
    Pending: session.PendingRoutes{
        Status: "/api/user/pending", Confirm: "/api/user/pending/confirm", Cancel: "/api/user/pending/cancel",
        SendCode: "/api/user/pending/code", VerifyCode: "/api/user/pending/code/verify", EnrollEmail: "/api/user/pending/email",
    },
    Invitation: session.InvitationRoutes{Open: "/api/user/invitation", Accept: "/api/user/invitation/accept", SignUp: "/api/user/invitation/sign-up"},
    Email:      session.EmailRoutes{Add: "/api/user/email", Verify: "/api/user/email/verify"},
    SignUp:     session.SignUpRoutes{Start: "/api/user/sign-up", Open: "/api/user/sign-up/open", Complete: "/api/user/sign-up/complete"},
    FirstPassword: session.FirstPasswordRoutes{SendCode: "/api/user/first-password/code", Set: "/api/user/first-password"},
}))
r.Group(func(r chi.Router) {
    r.Use(auth.ValidateXSRFToken)
    r.Post("/api/user/session", password.Login())
    r.Post("/api/user/pending/confirm", auth.Pending().ConfirmWithPassword())
    r.Post("/api/user/pending/cancel", auth.Pending().Cancel())
    r.Post("/api/user/pending/code", auth.Pending().SendCode())          // mails the sign-in code
    r.Post("/api/user/pending/code/verify", auth.Pending().VerifyCode()) // {"code", "rememberDevice"}
    r.Post("/api/user/pending/email", auth.Pending().EnrollEmail())      // {"email"}: a user without an address gives one
    r.Post("/api/user/mfa", app.CompleteMFA) // an MFA step of the application's own: checks its code, then auth.API().CompletePending
    r.Post("/api/user/logout", auth.Logout())
    r.Post("/api/user/password", password.ChangeUserPassword()) // validates the session itself
    r.Post("/api/user/password/remove", password.RemoveUserPassword()) // {"password"}: the person's own password, removed; validates the session itself
    r.Post("/api/user/password/strength", password.Strength())          // {"password", "username", "name", "email"}: how strong it is, asked while the person types; no session
    r.Get("/api/user/sign-in-means", auth.SignInMeans())        // what the person signs in with: a password or none, the directory sign-ins linked; validates the session itself
    r.Post("/api/user/name", auth.ChangeName())                 // {"name"}: the person's own name; validates the session itself
    r.Post("/api/user/look", auth.ChangeLook())                 // {"scheme", "palette"}: the look the person chose; validates the session itself
    r.Post("/api/user/invitation", auth.Invitation().Open())          // {"token"}: sets the invitation cookie
    r.Post("/api/user/invitation/sign-up", auth.Invitation().SignUp()) // {"password", "username"}: the invited person's account
    r.Post("/api/user/sign-up", auth.SignUp().Start())                 // {"email"}: mails the sign-up link; 404 while the password method is invite only
    r.Post("/api/user/sign-up/open", auth.SignUp().Open())             // {"token"}
    r.Post("/api/user/sign-up/complete", auth.SignUp().Complete())     // {"token", "password", "username"}
    r.Group(func(r chi.Router) {
        r.Use(auth.ValidateSession)
        r.Post("/api/user/invitation/accept", auth.Invitation().Accept()) // the signed-in person accepts
        r.Post("/api/user/email", auth.Email().Add())                     // {"email"}: mails the verification link
        r.Post("/api/user/email/verify", auth.Email().Verify())           // {"token"}
        r.Post("/api/user/first-password/code", auth.FirstPassword().SendCode()) // mails a person who has no password the code to choose one
        r.Post("/api/user/first-password", auth.FirstPassword().Set())           // {"code", "password"}
        r.Get("/api/user/tokens", auth.ApplicationTokens().List())        // the person's tokens
        r.Post("/api/user/tokens", auth.ApplicationTokens().Create())     // {"name", "outlet", "expiresIn"}: the secret, once
        r.Post("/api/user/tokens/revoke", auth.ApplicationTokens().Revoke()) // {"id"}
        r.Get("/api/user/device", auth.Device().Lookup())                 // ?userCode=: which client asks, for which outlet
        r.Post("/api/user/device/approve", auth.Device().Approve())       // {"userCode"}
        r.Post("/api/user/device/deny", auth.Device().Deny())             // {"userCode"}
        // …protected routes
    })
})
```

The invitation, email and sign-up handlers are described under "Invitations", "Users
and their email address" and "Sign-up" below, the sign-in code's under "The second
factor", and the first password's under "A person's first password".
The machine outlet, the token page and the device flow are described under "Machine
outlets and bearer tokens" below.

The pending handlers and the API's pending methods read the pending identity
`StartSession` found, so they must run behind it; the POSTs belong behind
`ValidateXSRFToken` like the password login. The session read (`Authenticated`), the
logout and the password change sit outside `ValidateSession`: they validate the session
on their own, and they are what a user who must change their password reaches (see
"Users and the forced password change"). The methods list names the password change
route (`ChangePassword`) whenever it lists the password method. The removal of a
person's own password (`RemoveUserPassword`) validates the session itself too and is
routed beside the change, though a user who must change their password is refused
there as everywhere else; the methods list names it where the application serves it
(`RemovePassword`).

### What the login page is told

The login page renders from the server, so the browser bundle is the same in every
environment and a method that is not registered has no button. `auth.Methods(list)` is
the public route that tells it: for each method the application lists, its name, the
method behind it, how it starts, its label and its path, in the list's order, the
pending sign-in routes when the list names them, and the password change route when
the password method is listed.

```json
{
  "methods": [
    {"name": "password", "method": "password", "kind": "password", "label": "Password", "path": "/api/user/session"},
    {"name": "workos", "method": "workos", "kind": "redirect", "label": "Single sign-on", "path": "/api/user/sso/login"}
  ],
  "pending": {"status": "/api/user/pending", "confirm": "/api/user/pending/confirm", "cancel": "/api/user/pending/cancel",
              "sendCode": "/api/user/pending/code", "verifyCode": "/api/user/pending/code/verify", "enrollEmail": "/api/user/pending/email"},
  "changePassword": "/api/user/password",
  "passwordStrength": "/api/user/password/strength",
  "removePassword": "/api/user/password/remove",
  "usernameIsEmail": true,
  "invitation": {"open": "/api/user/invitation", "accept": "/api/user/invitation/accept", "signUp": "/api/user/invitation/sign-up",
                 "standing": {"email": "pat@lakeside.edu", "newTenant": false, "directoryOnly": false, "expiresAt": "2026-10-31T20:00:00Z",
                              "usernameIsEmail": false, "methods": ["workos"], "accountExists": false, "hasPassword": false}},
  "email": {"add": "/api/user/email", "verify": "/api/user/email/verify"},
  "signUp": {"start": "/api/user/sign-up", "open": "/api/user/sign-up/open", "complete": "/api/user/sign-up/complete"}
}
```

- `kind` is how the method starts: `password` is a form that posts `{"username",
  "password"}` to `path` and reads the JSON answer of the password login; `redirect`
  sends the browser to `path` (a GET, with `?returnUrl=` naming the page to come back
  to) and the method's callback brings it back. The kinds are
  `signin.KindPassword` and `signin.KindRedirect`.
- `name` is the registration's name on the `Auth`, `method` the method behind it
  (`password`, `azure`, `google`, `workos`, `oidc`), for a page that shows a provider's
  mark.
- `label` is the application's wording for the method.
- `path`, the pending paths and `changePassword` are the application's: the list is
  written where the routes are wired, so the paths the page is told are the paths the
  router serves. A registered method the list leaves out is not offered. The pending
  block's `sendCode`, `verifyCode` and `enrollEmail` are the sign-in code's routes,
  named together or not at all (`PendingRoutes`); a page told them runs the library's
  second factor (see "The second factor") when a sign-in waits for it.
- The pending block's `confirm` is where a person whose sign-in matched an account with
  a password enters it. Every list of an Auth that registers the password method names
  it, a list that does not offer the method among them: the person may come through any
  of the Auth's methods, and `Methods` refuses a list that leaves it out. An Auth that
  registers no password method asks nobody for a password, so its lists may leave it
  out (`PendingRoutes.Confirm`): the application mounts no `ConfirmWithPassword()`
  handler, the answer carries no `confirm`, and the page offers no password
  confirmation.
- `changePassword` is the path of the password method's `ChangeUserPassword` handler,
  which the page posts `{"oldPassword", "newPassword"}` to when a sign-in finds the
  user must change their password (the session read's `passwordChangeRequired`). It is
  present whenever the password method is listed.
- `passwordStrength` is the path of the password method's `Strength` handler, where a
  page that sets a password asks how strong it is.
- `removePassword` is the path of the password method's `RemoveUserPassword` handler,
  present when the list names it (`MethodList.RemovePassword`, which needs the password
  method listed): where a signed-in person's account page posts `{"password"}` to remove
  their own password (see "Users and the forced password change").
- `firstPassword` are the paths of the first password handlers, `{"sendCode", "set"}`,
  present when the list names them (`MethodList.FirstPassword`, both or neither, which
  needs the password method listed): where the account page of a signed-in person who
  holds no password asks for the code and posts `{"code", "password"}` (see "A person's
  first password").
- `signInMeans` is the path of the `SignInMeans` handler, present when the list names it
  (`MethodList.SignInMeans`): where a signed-in person's account page reads what they
  sign in with, `{"password": true, "directories": [{"name": "google", "label": "Fleet
  Google"}]}`. The page hides the change and the removal of a password from a person
  who holds none, offers the removal only where a directory sign-in can sign them in,
  and says what the removal leaves by the label of the button the person signs in
  through, as this list words it.
- `forgetBrowser`, `changeName` and `changeLook` are the paths of the `ForgetBrowser`,
  `ChangeName` and `ChangeLook` handlers, each present when the list names it
  (`MethodList.ForgetBrowser`, `MethodList.ChangeName`, `MethodList.ChangeLook`): where
  a signed-in person's account page forgets this browser and writes the person's own
  name, and where their pages keep the look they chose (see "The second factor", "A
  person's name" and "The look a person chose").
- `usernameIsEmail` is present, and true, when the listed password method declares
  the username as the email address (`UsernameIsEmail`): the page then asks for the
  address where it would ask for a username, and a sign-up takes none.
- `invitation`, `email` and `signUp` are the paths of the invitation, email address and
  sign-up handlers, each block present when the list names it (`MethodList.Invitation`,
  `Email`, `SignUp`). The invitation block's `signUp` is named exactly when the
  password method is listed; the sign-up block needs the password method listed, and
  is in the answer only while an administrator has the password method open to sign-up,
  read as each request arrives. The page opens a link it was sent to with
  `#invitation=`, `#verify=` or `#signUp=` by taking the token out of the address and
  posting it to the block's `open` (or `verify`) path; see "Invitations", "Users and
  their email address", "Sign-up" and "Mail".
- `invitation.standing` is the live invitation the browser carries, present when the
  invitation cookie that `open` set still names an invitation that stands: the address
  it was sent to, its tenant (`""` for the application's global surface), when it stops
  being accepted, whether an account created from it takes the address as its
  username, and the names of the listed external registrations that could admit the
  address (`methods`, empty when none could), as `Invitation().Open()` answered them;
  the page hides the methods not named. For an invitation that makes a tenant,
  `newTenant` is true, `tenantName` is the name the invitation carries for it, and
  `directoryOnly` says it is accepted with an organization's account alone, so the page
  offers no account with a password for it (see "Invitations"). `accountExists` says an account already holds
  the invited address and `hasPassword` that it signs in with a password, so the page
  offers that account's password form, and offers to create an account only where
  none holds the address. Whoever holds the link is told this much, so the page can
  offer the right form; the link proves nothing of who holds it, and nothing is
  accepted by it. A page that arrives without the
  mailed link's token, as it does when an external sign-in was refused and sent the
  browser back with `?code=`, reads the invitation from here and keeps showing it. The
  block is absent when the browser carries no cookie, or its invitation is dead (used,
  revoked or expired).

`Methods` is called after every method is registered and panics on a wiring mistake,
as a registration does: an empty list, a method that is not registered on the `Auth`,
a name listed twice, a method without a label or with a path that is not a canonical
path in the application, a pending block with some of its paths, the password method
without its change route or that route without the method, an invitation, email or
sign-up block with some of its paths, or a sign-up block with no password method
listed. Route it beside the login routes: behind `StartSession`,
before `ValidateSession`, no XSRF token.

### The redirect and response contract

A sign-in ends in one of three ways. External methods answer with a redirect; the
password login and the pending handlers answer JSON. `<LoginURL>` is the method's
`Config.LoginURL` (default `/login`).

| Outcome | External `Callback()` | `password.Login()` |
| --- | --- | --- |
| Session established (new session ID) | `302 <returnUrl>` (default `/`) | `200 {"mfaIsRequired": false}` |
| Waits for the user's password | `302 <LoginURL>?pending=confirmation[&returnUrl=<path>]`, or the pending hook's URL | — |
| Waits for the application's MFA | `302 <LoginURL>?pending=mfa[&returnUrl=<path>]`, or the pending hook's URL | `200 {"mfaIsRequired": true[, "redirectUrl"]}` |
| Refused | `302 <LoginURL>?code=<code>` | `401 {"message", "code"}` (403 for a forbidden cause) |
| Refused: the name's failed attempts reached the limit | — | `429 {"message", "code": "too_many_attempts", "retryAfter": <seconds>}` with the `Retry-After` header (see "Attempt limiting and the after-sign-in hook") |

- `returnUrl` is only ever a path in the application, in canonical form: one leading
  `/` (not `//` or `/\`), printable ASCII, no backslash anywhere (raw or `%5C`), no
  empty, `.` or `..` path segment (raw or percent-encoded), and a path made of RFC 3986
  path characters; its query and fragment pass through (`/settings/sso?test=done`,
  `/reports#x`). An external `Login()` whose `returnUrl` breaks any of these is refused
  with **400**, and the callback sanitizes it again, falling back to `/`. The callback
  writes the `Location` header with exactly that value. Percent-encode a non-ASCII path
  before passing it. A WorkOS `Login()` without `organization` is also 400, and its callback refuses a profile from another organization than the login requested (`organization_mismatch`).
- `prompt=select_account` on a Google or Entra `Login()` asks the provider for its
  account chooser (its `prompt=select_account`), so a provider holding one signed-in
  account does not sign that account in again without asking. The login page sends it
  after a refusal, so a person refused with the wrong account picks another instead
  of being sent round the same loop, and under "Use a different account"; it sends
  nothing on an ordinary visit, where the single-account person is signed in without a
  click. Any other `prompt` value is refused with **400**. A provider that reports an
  error on the callback instead of a code (`error=access_denied` when the person
  canceled at its chooser or consent screen) is refused `token_exchange_failed`
  without an exchange, after the state check, so the page is always reached with a
  code and a login page that forwards to the provider on its own can never loop.
- `pending` is the login page's routing marker. The page reads the pending identity from
  `Pending().Status()`: `200 {"reason", "email", "expiresAt", "returnUrl"[, "factors"][, "emailCode"]}`, `404` when
  there is none, `401 {"code": "pending_expired"}` once it has expired or been used.
  `emailCode` is the sign-in code's state, present for an MFA wait that may take it
  (see "The second factor").
- **Confirmation** (`reason` `confirmation`): the page asks for the existing user's
  password and posts `{"password"}` to `Pending().ConfirmWithPassword()`: `200
  {"mfaIsRequired": false}` (linked, the hook called, signed in; go to `returnUrl`),
  `200 {"mfaIsRequired": true}` (linked, but the policy wants MFA: the pending identity
  remains with reason `mfa`), `401` for a wrong password (the pending identity remains),
  `409` when the pending identity waits for MFA instead.
- **MFA** (`reason` `mfa`): the page runs the library's step, the sign-in code
  (`Pending().SendCode()`, `VerifyCode()`; see "The second factor"), or the
  application runs its own and calls
  `auth.API().CompletePending(ctx, w, sessioninfo.AuthEvent{Method: "totp"})`, which
  starts the session (the user is resolved afresh and must be the one it waited on, and
  the policy decides again with the step's events on the request, so a tenant disabled
  or switched to require SSO during the wait is honored: a `RequireMFA` the events
  satisfy proceeds, a `Deny` refuses, a `RequireMFA` for another factor holds again)
  and records the sign-in's events followed by the step's.
  The pending identity always names its user (`UserID`, `Username`), including one the
  sign-in has just provisioned, which exists by then.
- `Pending().Cancel()` discards the pending identity.
- **The pending hook** (`WithPendingHook`) runs when `password.Login()`, an external
  `Callback()` or `Pending().ConfirmWithPassword()` has just held a sign-in, after the
  pending identity is stored and its cookie set. It receives the
  `*sessioninfo.PendingIdentity` (reason, user, the identity with its email, expiry,
  return path), so the application can send its MFA code and choose the next page:

  ```go
  func onPending(ctx context.Context, w http.ResponseWriter, r *http.Request, p *sessioninfo.PendingIdentity) (string, error) {
      if p.Reason != sessioninfo.PendingMFA {
          return "", nil // the default: <LoginURL>?pending=confirmation
      }
      if err := mfa.SendCode(ctx, p.UserID.UUID); err != nil {
          return "", err // the pending identity is discarded and the sign-in fails
      }

      return "/user/mfa", nil
  }
  ```

  The callback redirects to the URL it returns, and the JSON handlers answer it as
  `"redirectUrl"`; an empty URL keeps the default. The hook may instead write the response
  itself, and nothing more is written. An error discards the pending identity and fails
  the sign-in (`?code=internal_error` on a redirect). The hook changes nothing about the
  pending identity (its cookie, its timeout; the session ID is regenerated when it
  completes). The URL is the application's own: never build it from request input. The
  `AuthAPI` methods never call the hook: `CompletePending` and
  `ConfirmPendingWithPassword` return a `*signin.PendingSignInError` instead.
- The codes are the `sessioninfo.LoginRefusalCode`s of the "Login refusal codes"
  section, including the resolver's and policy's own; text never travels.

### What a pending identity is

A pending identity is a verified identity waiting for its confirmation or MFA. It is a
row of the `SessionPendingSignIns` table (migration `000002` of the auth schema) holding
everything the sign-in carried: the identity and its claims, the registration it came
through, the user it waits on, the steps so far, the role names the method asserted, the
factors the policy accepts, the return path and when it expires. The browser holds only
the `auth-pending` cookie, encrypted and authenticated with the cookie key, carrying an
opaque 32-byte token; the row keeps the token's SHA-256. A pending identity is accepted
only while its row is live: not expired and not consumed. Completing, canceling or
replacing it consumes the row by a conditional update, before the completion signs in,
so the token is single-use: of two completions racing on a copied cookie, one proceeds
and the other answers `pending_expired`. No session row exists until the sign-in
completes, so nothing of a pending identity ever validates as a session. Expired and
consumed rows stay for audit; delete them by `ExpiresAt` on your own schedule.

### The second factor

Every session carries a second factor when the sign-in policy asks for one: the policy
answers `signin.RequireMFA(sessioninfo.MethodEmailCode)` for every sign-in (or
`RequireMFA()`, which accepts any factor), and the library satisfies it one of three
ways, in this order, before it holds the sign-in. The requirement is the policy's, so
an application that wants no second factor sets none, and one whose rule varies by
person or tenant writes the rule; what a connection can vouch for is the
registration's, below, since it is a property of the sign-in method, not of the tenant
the person reaches.

1. **The connection attests.** A sign-in's connection says what it can about a second
   factor (`sessioninfo.Identity.Attestation`), and the Auth records it as the
   session's second auth event, right after the sign-in's, under the same connection:
   - **By evidence**: the token's `amr` claim names `mfa` (RFC 8176), which Microsoft
     Entra asserts after its own multifactor authentication. The event is
     `attested-by-evidence`, with the `amr` values. Google puts no such evidence in its
     tokens.
   - **By declaration**: the registration declares the directory as one that enforces
     a second factor, because the application administers it and knows it does (a
     Workspace's 2-Step Verification enforcement, a tenant's Conditional Access policy):
     `google.Workspace("example", "example.com").Attesting()`,
     `azure.Tenant(id).Attesting()`. The event is `attested-by-declaration`. The
     declaration is for named Workspaces and listed tenants only; `AnyWorkspace`,
     `AnyGoogleAccount`, `AnyTenant` and `PersonalAccounts` admit organizations the
     application does not administer and are refused with it, so name the
     administered one beside the open entry:
     `google.Workspaces(google.Workspace("example", "example.com").Attesting(), google.AnyGoogleAccount())`.
   An attested sign-in satisfies any `RequireMFA`, the application's own factors
   included, and is never held. A password never attests.
2. **The browser is a trusted device.** A person who entered the code may ask for the
   browser to be remembered (`rememberDevice`): the Auth keeps a row in the trusted
   devices table with the hash of an opaque token, and the browser the encrypted
   `auth-trusted` cookie with the token, for `WithTrustedDevicePeriod` (30 days). A
   sign-in from that browser by that user satisfies a `RequireMFA` that accepts the
   email code (no factor named, or `MethodEmailCode` among them) and records a
   `trusted-device` event in the step's place; a `RequireMFA` for the application's
   own factors alone is the application's step to run. The cookie holds one entry per
   user, keyed by the user's id inside the encrypted value, so two people who share a
   browser, or one person with two accounts, are each remembered: a sign-in reads the
   signing-in user's entry alone, and a sign-in held for the code drops that user's
   entry alone (it named a dead device, or none), leaving the others' as they are. A
   browser holds at most five entries; remembering one more drops the oldest.
3. **The sign-in code.** Otherwise the sign-in waits as a pending identity with reason
   `mfa` and the policy's factors, and the login page runs the step when the factors
   include the email code: `Pending().Status()` reports `emailCode` with the masked
   address the code goes to (`codeEmail`, `p•••@example.com`), whether a code stands
   (`codeSent`, `codeExpiresAt`) and whether the user must first give an address
   (`enrollEmail`); the page posts to `Pending().SendCode()`, which mails the user a
   six-digit code good for `WithEmailCodeTimeout` (10 minutes), then
   `{"code", "rememberDevice"}` to `Pending().VerifyCode()`, which completes the
   sign-in in a new session and records the `email-code` event itself. Only the
   library records that event: an application's `CompletePending` with an `email-code`
   event is refused, since the event is the library's proof that the code it mailed
   was entered. A
   wrong code is `401 {"code": "invalid_code"}` and the code stands with one fewer
   attempt; the fifth wrong code kills it, as its expiry does, and a code entered while
   none stands (none sent, or the one sent died) is `401 {"code": "code_expired"}`, so
   the page asks for a new one, which replaces the old. A guess takes its attempt
   before it is judged: the store counts it on the code's row and answers which
   attempt it was in one step (`AuthStore.RecordFactorCodeAttempt`), and a guess past
   the fifth is answered `code_expired` without being compared with the code, so of
   guesses entered at once no more than five are ever judged. A code whose five
   attempts are taken stands no longer: the pending sign-in's state reports none, so
   the page offers a new one. An address is sent five codes
   within fifteen minutes that go unused, and the sixth is refused with
   `too_many_emails` and the wait, as a link is; a code that is used resets the count,
   so a person who signs in with each code sent never meets the limit. The codes sent to
   the address an account holds are counted by themselves (`CODE:`). The codes that
   accept an invitation are counted under that invitation (`INVITATION:` and its id) and
   under no address: whoever holds the invitation's link has them sent, and a mailed
   link can be forwarded, so its holder uses up that invitation's codes and nothing
   else. An invitation also takes twenty-five codes and no more. Its code is the only
   thing between the holder of its link and what it gives, so a code records the
   invitation it was mailed for, and every such code entered takes a place under that
   invitation (`INVITATION-GUESS:` and its id) before it is judged, in every sign-in
   and whatever the browser that enters it carries, and that count never starts
   again: it is reset by nothing, and it is kept for longer than any invitation
   stands. Such a code is judged only in a browser that carries that same invitation.
   Entered in one that carries none or another invitation
   (`401 {"code": "invitation_not_open"}`), or in one whose invitation has ended
   (`401 {"code": "invitation_expired"}`), it is refused without being
   compared, its place taken and nothing written: it is the acceptance of that
   invitation and no account's second factor. The wrong code that takes the twenty-fifth place, and every code asked for
   or entered after it, is `401 {"code": "invitation_spent"}`; nothing more is mailed
   for the invitation, and an administrator invites the person again. The right code
   among the twenty-five accepts. The links, and a code asked for an address a person types at this step, are
   counted together under another key (`MAIL:`), which anybody who knows an address can
   run up. So nothing a stranger asks for in a person's name, or with a link that was
   mailed to them, holds up the codes that person signs in with. A request takes
   its place under its limit before its code is mailed, counted in the
   store's one transaction (`AuthStore.RecordSignInFailure`, `SignInAttempts.Counted`),
   so of codes asked for at once no more than five are mailed; and of codes recorded at
   once for one pending sign-in one stands, so there is never a second code to guess
   at with attempts of its own. The code goes to the address the user holds, verified or not:
   an address an operator entered is proven by the code as a link would prove it, and
   the user holds it verified once the code is entered. A code is good only for the
   address the user holds when it is entered: one mailed to an address the account no
   longer holds is dead (`code_expired`), nothing is written, and the next code goes to
   the address the account holds now. A user who holds no address
   gives one at the step (`Pending().EnrollEmail()`, `{"email"}`), which is written on
   the user verified when its code is entered; an address another account holds is
   answered as sent and told so by mail to that address, as the add-address step does,
   so the screen reveals nothing. An address that an invitation which stands was mailed
   to is treated the same way: no code is made, the mailbox is sent a message that says
   to open the invitation's link, and the screen is answered as sent. The step takes an
   address only from a sign-in that is already the account's: a password's, or an
   identity linked to the user. A
   sign-in that waits to be linked on the account's code (see "A first link on a name
   alone") is given none, since nothing but the name ties it to the account, and is
   answered `409` with the reason. The `AuthAPI` offers the same as `SendEmailCode`,
   `EnrollEmail` and `VerifyEmailCode`.

A code mailed to the mailbox of the account that just signed in adds little against a
stolen password that opens that mailbox too: for a directory, the attestation is the
answer, and the code is for the password method and for directories the application
neither administers nor gets evidence from.

**The administrator's reset.** `AuthAPI.ResetFactors(ctx, userID)` forgets every
browser the person asked to be remembered, so their next sign-in from any of them asks
for the factor; a signed-in person forgets their own browser with
`ForgetTrustedDevice`, which revokes the session user's device and takes their entry out
of the cookie, leaving the browser remembered for anyone else it was. `Auth.ForgetBrowser()`
is the handler over it that a person's account page posts to: it validates the session
itself, as the password change does, so it is routed beside it outside `ValidateSession`,
and it refuses an impersonated session; the generated router mounts it at
`/user/browser/forget` and names it in the methods list (`MethodList.ForgetBrowser`). The
session read (`Authenticated`) says `browserRemembered: true` while the browser carries the
person's remembered entry, so the page offers the action only then. The email code itself has nothing to reset: a person who lost
their mailbox has an administrator ask for a new address (`RequestAddressChange`), and
the code keeps going to the address the account holds until the person confirms the new
one from its mailbox. Recovery codes are not offered. Live sessions
are not touched by the reset; `DestroyUserSessions` ends them. The person is told of an
administrator's reset by a notice to the address they confirmed (`NoticeFactorReset`
sends it for an application that resets the factor in rows of its own).

**What the tables hold.** A code is a row of `SessionFactorCodes`: the pending sign-in
it was sent for, the address, the invitation it was mailed for where it accepts one
(`InvitationId`, null for every other code: an account's own sign-in code, and a code
mailed to an address a person gave), the code's hash salted by the
pending sign-in, the wrong
attempts, and when it was created, expires and was consumed; one code stands for a
pending sign-in at a time, however many are recorded at once (the PostgreSQL driver
takes the pending sign-in's row for the insert, and Spanner's transaction is
serializable), and the codes go with the pending sign-in. A trusted device
is a row of `SessionTrustedDevices`: the user, the token's hash, and when it was
created, expires, was last used and revoked; the devices go with the user. Expired and
consumed rows stay for audit; delete them by `ExpiresAt` on your own schedule.

### Every session

- **The session ID is regenerated** at every sign-in, confirmation and MFA completion:
  the cookies are written only once the session row, its auth events and role
  synchronization have succeeded.
- **The user is loaded by `UserId` on every request.** `ValidateSession` refuses a
  session with no user or whose user is missing or disabled; the user is
  `sessioninfo.UserFromCtx(ctx)`. The session's user, authentication time and auth
  events are on the context's `*sessioninfo.SessionData`
  (`sessioninfo.DataFromCtx(ctx)`): `UserID`, `AuthenticatedAt`, `AuthEvents`.
- **Custom session data** is resolved in the transaction that inserts the session, after
  the user resolution has committed: the resolver reads the user (one just
  provisioned included) and the application's rows, and `req.User` says how the
  user was found. A pending identity has no session row, so it has no custom data.
- **Each session records how it was authenticated** in `SessionAuthEvents`: the sign-in
  method (with its connection and the IdP's `amr`), the attestation its connection
  makes (`attested-by-evidence`, `attested-by-declaration`), then any
  `link-confirmation`, `trusted-device` and MFA steps (`email-code`, or the
  application's own). `AuthAPI.StartAuthenticatedSession(ctx, w, userID, events)` starts a session the
  application authenticated itself, recording its events, which name steps of its own
  (an event under `email-code`, `trusted-device` or an attestation's method, which the
  library records itself, is refused, and so is one under the name of a sign-in the
  library runs: a password's, a directory method's, a registration's); it is decided like a login:
  the sign-in policy answers, a Deny refuses it and a RequireMFA holds it as a pending
  identity. No session start skips the policy; a support session is an impersonation.
- **Role synchronization** (Azure, Google) reconciles the roles of the user the
  identity resolved to, from the roles the provider asserted at sign-in, whenever such a
  sign-in establishes a session (including after a confirmation or MFA). A sign-in
  whose asserted roles the application recognizes none of is refused (`no_roles`)
  before anything is resolved or written, so an invitation it carried stands; a role
  store that fails once the session stands expires the session.
- **Impersonation** is established through `auth.API().StartImpersonatedSession`, from
  a request the application's declaration made (see "Impersonated sessions"). A user
  principal's session belongs to the impersonated
  user; a role principal's belongs to none. The session records an `impersonation`
  event whose connection is the actor.
- **A request on a machine outlet** is bound to the person whose bearer token it
  carries the same way: the user is loaded on every request and refused when missing
  or disabled, the session data's `ID` is the token's, its one auth event is `token`
  with the token's kind as its connection, and `sessioninfo.TokenFromCtx(ctx)` names
  the token. See "Machine outlets and bearer tokens".
- **User management is by ID**: `ChangeSessionUserPassword`,
  `DeactivateSessionUser` and `DeleteSessionUser` destroy the user's sessions by
  `UserId`; `DeleteSessionUser` deletes its identity links with
  it; `UnlinkIdentity` refuses the last identity that can sign in a user with no
  password, and `RemoveSessionUserPassword` the password of a user no identity can sign
  in. See
  "Users and the forced password change".
- **Failed password attempts are limited** per name, known or not, and an attempt past
  the limit is refused with `too_many_attempts` and the wait before anything is read
  or hashed; **the after-sign-in hook** (`SetSignedInHook`) is told of every session a
  sign-in establishes. See "Attempt limiting and the after-sign-in hook".
- **Provider-initiated (front-channel) logout** is keyed by the issuer of the token the
  sign-in verified and the provider's session id, since a provider's session id is
  unique only within its issuer: an Azure sign-in records the ID token's `sid` claim on
  its session row with that issuer (`ProviderSessionId`, `ProviderSessionIssuer`), a
  pending sign-in carries them to the session it completes, and a regenerated session
  keeps them. A registration declares the issuers its tokens may come from
  (`signin.Registration.Issuers`; an Entra registration declares its one tenant's
  issuer, or the issuer of every tenant its `Tenants` slot admits), and a round trip from any other issuer is refused
  (`internal_error`). `azure.Method.FrontChannelLogout()` ends every live session under
  a declared issuer and the `sid` the request names, impersonations started from it
  included (their records end `Logout`), and answers 200. Entra sends the `sid` alone:
  a registration with one issuer scopes the request to it, and one serving several to
  whichever of its issuers live sessions carry the `sid` under (a provider's session id
  is unique within the provider, and every issuer a registration declares is the
  provider's). A request naming an `iss` is scoped to it, and refused with 400 when the
  registration does not declare it; a request without a `sid` is 400. The handler reads
  nothing from the browser, since Entra calls it in a frame, and a known `sid` under a
  declared issuer is its only credential. A session recorded with an id but no issuer
  is out of its reach until the person signs in again. Register its route as the
  application's front-channel logout URL in
  Entra, outside `ValidateSession`. An app registration takes one such URL, and the
  handler reaches the sessions of its own `Auth`: an application whose several `Auth`s
  should all end at Microsoft's sign-out registers one Entra application per `Auth`.
  Sessions of the same user under another `sid`, or signed in by a method that reports
  no session id (password, Google, WorkOS), are not touched.

### Users and the forced password change

The `Auth` has no user-management handlers of its own: an application, or a library
over it, manages users through `auth.API()` and guards the callers. The API reads
users as the store holds them (`sessionstorage.SessionUser`: the username, `Disabled`,
the password hash, nil for a password-less user, `PasswordChangeRequired`, `CreatedAt`
and `LastSignInAt`, nil before the first sign-in) and writes them by ID:

| Call | What it does |
| --- | --- |
| `Users(ctx)` | Every user, by username. |
| `User(ctx, id)`, `UserByUsername(ctx, username)` | One user; the username is matched without regard to case. |
| `CreateSessionUser(ctx, &CreateUserRequest{Username, Password, Disabled, Email})` | A user, password-less when `Password` is nil. `Password` is the seam for a seed or an import, which creates an account that already holds a password: a person's own account is made by an invitation or a sign-up, where the person chooses theirs. A password the judgment refuses (400) creates nothing: it is judged against the request's username, name and address (see "Password strength"). The user is created under no password change mark. The library generates the user's id; a seed that must know the id before the user exists sets `ID` (Go callers only, never read from a request body; the nil id is refused): one that names its people in rows of its own, so a development database reads the same on every machine, and one that gives a user its roles (`SeedUserID`, below). |
| `ChangeSessionUserUsername(ctx, id, username)` | Renames the user and its live sessions. |
| `ChangeSessionUserPassword(ctx, w, id, &ChangeSessionUserPasswordRequest{OldPassword, NewPassword})` | The user's own change: the old password must match and the new one differ. Writes the new password over the one that was proved and no other (Conflict, `sessionstorage.ErrPasswordChanged`, when the password changed meanwhile: nothing is written and no session ends), clears the mark and destroys the sessions in the same transaction (`AuthStore.ChangeUserPassword`: the password changed and the sessions ended, or neither), then continues in a new one. The new session is the continuation of the one that made the change: it records that session's auth events, how it was established and the factors it holds, read from the session and never from the request, so a person who signed in through a directory still holds the directory's session and one who entered with a password and the mailed code keeps the code. Where there is none to continue (a call outside a session of the user, a request a bearer token authenticated, or a session that views the application as them) it records the password the change proved, alone. `PasswordMethod.ChangeUserPassword()` is this over HTTP. |
| `DeactivateSessionUser`, `ActivateSessionUser`, `DeleteSessionUser`, `DestroyUserSessions` | As named; the first and the third destroy the sessions first. The first three read the session off the context they are called with: in a session that acts as someone else each refuses the account of the person who started the session (403, "you are acting as someone else, so you cannot disable your own account: …"), so a route an application wrote that calls one is held to the rule the generated user management has. The store's own `DeactivateUser`, `ActivateUser` and `DeleteUser` beneath them are the bare writes and are not held to it: they are handed no session to read, so a route calls the session API's three, never the store's. In a session of the person's own, the caller guards against the person disabling or deleting themselves. |

| `Identities(ctx, id)`, `UnlinkIdentity(ctx, identityID)` | The user's identity links; unlinking the last link that can sign in a user with no password is refused (Conflict, `sessionstorage.ErrLastSignInMethod`): another link must be one a sign-in is still possible through (`SignsInThrough`), whatever links that sign nobody in remain. A user who holds a password may lose every link. |
| `SignInMeans(ctx, id)` | What the user signs in with (`SignInMeans`): whether they hold a password, never its hash, and the sign-in methods the login page offers that an identity linked to them signs in through, each by the registration's name and the label its button carries, in the page's order. A link answers the registrations that can still sign the person in through it: of the link's method, offered on the login page, and taking the link's connection, so of two listed Entra registrations a link names the one whose tenants hold its tenant; a method that cannot tell one connection from another answers every registration of it. A link of a method the page no longer offers, or from a tenant or a Workspace no registration takes any more, names nothing, and is no means of sign-in to the removal either. For an administrator's page; the caller guards who may ask about whom. `Auth.SignInMeans()` is the person's own read over HTTP, answered in the words of the methods list that names its path. `SignsInThrough(method, connection)` is the judgment itself, for one link. |
| `DirectoryName(method, connection)` | What the Auth's sign-in registrations call the directory an identity of this method and connection came through, for a sentence that names it to a person: the name a registration that takes the connection gives it (`signin.Method.ConnectionName`: a tenant listed with `azure.NamedTenant`, a Google Workspace's name or the domain of one admitted as any Workspace, an OpenID Connect provider's name or its issuer's host), and the label of the button of the first registration of the login page that takes the connection; each is empty where the registrations have none. A tenant listed by its id alone (`azure.Tenant`), one admitted as any tenant, and a method that names no connection have no name, so a sentence says such a directory by the label ("your organization's Microsoft directory") and never by the connection, which for an Entra tenant is an id. It reads nothing. |
| `RemoveSessionUserPassword(ctx, id)` | Removes the user's password, an operator's act: the password then signs nobody in, and the user signs in through an identity linked to them. A user keeps it, their last means of sign-in (Conflict, `sessionstorage.ErrLastSignInMethod`), unless a link can sign them in: one of a method the login page offers whose registration still takes the link's connection (`SignsInThrough`), which rules out a user with no link and one whose links are all left over. A user with no password has none to remove (BadRequest). Clears the mark and the name's count of failed sign-ins; the user's sessions stand. `PasswordMethod.RemoveUserPassword()` is the person's own removal over HTTP, which asks for the password. |
| `UserByEmail(ctx, email)` | The user holding an address, verified or not. See "Users and their email address". |
| `RequestAddressChange(ctx, id, email, by)` | An administrator's change of a person's address: mails the link that confirms the new address to it and changes nothing until the link is followed. Refused for a person who signs in through an organization's directory and holds a confirmed address (Conflict). See "Users and their email address". |
| `SendPasswordReset(ctx, id, by)` | Mails the user a link that resets their password, to the address they confirmed; Conflict, in one sentence, for an account with no password or no confirmed address. See "A password reset". |
| `NoticeFactorReset(ctx, id)`, `NoticeRemovedFromTenant(ctx, to, tenant, accountDeleted)` | The notices of an administrator's reset of the second factor and of a removal from a tenant, for an application that makes either change in rows of its own; each goes to a confirmed address and to no other. |
| `WaitForMail()` | Waits for the mail a person's own request for a reset link sends after its answer; for a test, and for a shutdown that lets mail finish: the auth package `impulse` writes calls it in its `Close`, which an application calls once it has stopped serving and before it closes the database. |
| `SetSessionUserName(ctx, id, name)` | An operator's entry of the person's name, or its removal (nil). See "A person's name". |
| `Invite(ctx, &InvitationRequest{Email, Name, TenantID, EveryTenant, RoleNames, InvitedByUserID})`, `Invitations(ctx)`, `RevokeInvitation(ctx, id)` | An invitation mailed to an address; the invitations that stand; one withdrawn. See "Invitations". |
| `PrepareInvitation(ctx, &InvitationRequest{…})` | An invitation checked and made, and neither stored nor mailed: `Row()` is the mail links row for a caller that stores it in a transaction of its own (`InvitationRow`, whose comment says how each column is written), and `Send(ctx)` mails the link once that transaction has committed; a message that could not be sent ends the invitation and marks its row as not mailed (`MailFailedAt`), so a list of the links tells it from one that was used or withdrawn. The token, the message and the sender stay in the library. `Invite` is the two in the Auth's own storage. |

**A seed that gives a user its roles.** A role is held by the id of an account, so a
seed needs the id before the user exists. `session.SeedUserID(auth, username)` is that
id, the same on every run: the seed writes the roles under it first and creates the user
under it last (`CreateUserRequest.ID`). The user's row then says the seed is done. A run
that stopped before it is repeated whole by the next one, which writes the same
memberships again and creates the user. A seed that created the user first would have to
give the roles, after a stopped run, to a user it found under a name, and a name is
something anyone may come to hold. The persona cast's generated seed works this way:
at every run it gives the account under that id the roles its entry names that it does
not hold, and gives nothing to a user of another id who holds the persona's login. A deployed Auth's first
administrator is not seeded: no account is made ahead of the person, who is invited (see
"The first administrator's invitation").

**The forced password change.** An administrator may require a new password of
any person who holds one (the generated user management's method, which sets
the mark `PasswordChangeRequired` on the user's row): the user changes it before anything else is served. While the mark is set, `ValidateSession` (and
`AuthAPI.ValidateSession`) refuses every request with 403 and `{"message", "code":
"password_change_required"}`; the session read answers, with `"passwordChangeRequired":
true`, and the logout and the password change go through, since they validate the
session on their own, which is why the three sit outside `ValidateSession` in the
wiring above. A login page that reads the mark from the session read posts the change
to the methods list's `changePassword` path before it opens the application. The change
clears the mark and goes on in a new session that carries the auth events of the one
that made it (`ChangeSessionUserPassword`, below); an
impersonated session is the actor's and is not held to the user's mark. No call of the
library sets the mark: a user is created under none, with a password or without, and the
application writes it when an administrator requires the change. The password a user chose through
`ChangeSessionUserPassword` or at a reset link clears it. A password-less user has nothing to change, so the
generated user management refuses to require a change of one.

**Removing a password.** A person who signs in through a directory does not need the
password they started with. Its removal follows the rule of an identity's unlinking,
from the other side: an identity is not unlinked when it is the last one that can sign
in a user with no password, and a password is not removed from a user no identity can
sign in (409, `sessionstorage.ErrLastSignInMethod`).

**"Linked" means a link that can sign the person in.** A row of the identities table
is not a way in by being there. A link counts when a sign-in through it is still
possible: its method is a registration the login page offers (the methods list, or
every registration where the application wrote none), and that registration still
takes the link's connection, where the method can tell: an Entra registration asks its
tenants, a Google one whether it would write that connection for the account today (a
Workspace it still names, an account's own domain while any Workspace is admitted and
no named one has taken the domain), a generic OpenID Connect one whether the connection
is its own. A method that cannot tell one connection from another counts every link of
it. So a link left from a registration the Auth no longer has, or from a tenant taken
off the registration, does not hold the password in place of a way to sign in: the
person keeps the password until a link that works stands beside it. One judgment says
this everywhere (`AuthAPI.SignsInThrough`): the operator's removal and the person's
own are refused by it, the unlink of an identity is refused by it, `SignInMeans`
answers by it, the generated user management refuses and offers by it, and the pages
offer the removal by those reads. The library names the links that count to the store,
and the store removes the password only while one of them is still linked, in the
transaction that removes it, so an unlink asked at once cannot leave the person with
neither. An unlink is held the same way from its side: where the person holds no
password, `UnlinkIdentity` hands the store the judgment, and the store asks it of the
person's other links in the transaction that unlinks, so the last link that counts is
not unlinked while only links that sign nobody in would remain (409, told so and what
to do), a link that signs nobody in goes from beside one that counts, and two unlinks
asked at once cannot take the person's last two between them. The tenant sign-in policy is not
asked: it decides a sign-in from a request no link can stand in for, and it may write.
A person whose link passes this judgment and whom the policy would refuse (no tenant
of theirs declares the link's directory) is therefore still counted as having a way in.

An operator removes a password with
`RemoveSessionUserPassword(ctx, id)`; the person removes their own by posting
`{"password"}`, the password as proof, to the password method's `RemoveUserPassword`
handler, routed beside the change and named to the page as the methods list's
`removePassword`. A wrong password answers 400, and a person no link can sign in is
told what to do first: sign in through their directory, which links it. The handler
validates the session itself and holds it to what every own-account route but the
change holds it to: a person whose password must change is refused (403,
`password_change_required`), since the removal would clear the mark with the password
and no change would have been made. It refuses an impersonated session.
Either way the removal takes the
password and the mark with it, clears the count of failed sign-ins kept under the
user's name, and ends no session: the sessions the user holds stand, the ones the
password established among them, as they do when an identity is unlinked. From then on
a sign-in with the password is refused exactly as a wrong password is, and counted the
same way.

**What the pages offer.** The server's refusals are the rule: no password to remove is
a 400, and a last means of sign-in a 409. A page is told enough not to offer what would
be refused. The person's account page reads `SignInMeans()` (the methods list's
`signInMeans`): it shows the change and the removal only to a person who holds a
password, the removal only where a directory sign-in can sign them in, and its question
names what is left ("You will sign in through Fleet Google only."). A page whose read
fails offers no removal: it does not fall back to naming every method the login page
lists. The generated router mounts the read at `/user/sign-in-means` and names it in
the methods list (`MethodList.SignInMeans`). The read answers in the words of the list
that names the path it is served at, so an Auth served on two outlets, each with its
own list and its own labels, tells each outlet's page what that outlet's login page
calls the method; a read at a path no list names, or one two lists name, answers each
method under the label it was first listed under. A page that hides a button is a
courtesy and decides nothing.

**A write that follows a proof writes over the password that was proved, and no
other.** Three writes are made on the strength of a password the person just entered:
the hash upgrade at a sign-in (a matching password stored under an older algorithm or
older parameters is hashed again, unless `AutoUpgradeHashes(false)`), the person's own
change and the person's own removal. Each reads the user, compares the password and
then writes, and another write can commit in between: the person changes the password
in another window, sets a new one through a reset link, or an operator removes it. So each hands the store the
hash it compared against (`UpgradeUserPasswordHash`, `ChangeUserPassword`,
`RemoveProvenUserPassword`), and the store writes only while the row still holds that
hash: on PostgreSQL the write's `WHERE` names it, or the removal compares it under the
lock it holds on the user row; on Spanner the hash is read in the transaction that
writes. A write that finds another hash, or none, writes nothing
(`sessionstorage.ErrPasswordChanged`, a Conflict), and the other write stands whole,
hash and mark. An upgrade that finds the password changed fails its sign-in as a wrong
password does, since the password entered is no longer the person's; a change or a
removal answers 409 and says the password has changed since it was entered and to try
again, with no session ended. Without this an upgrade could put back a password that
had just been changed or removed, and a change or a removal could undo a password
written a moment before, at a reset link or in another window, on proof of the one it replaced.

**What a password proves is inserted only while the person still holds that
password.** A sign-in compares the password and inserts later: the session, or the
pending sign-in the login is held as while a second step waits, up to the pending
sign-in's ten minutes. A change, a password set through a reset link or a removal can
commit in between, and it ends the person's sessions before the sign-in's own is there
to be ended. So the sign-in carries a proof of the password it compared against
(`sessionstorage.PasswordProof`: the SHA-256 of the stored hash, never the hash), and
the store inserts only while the user's row still answers to it, compared in the
transaction that inserts: the session (`SignInRules.Proven`), the pending sign-in
(`PendingSignIn.PasswordProof`, kept in the row's `PasswordProof` column) and the link
a password confirmation makes (`LinkConfirmedIdentity`). On PostgreSQL the user row is
read under a share lock a password write waits for, on Spanner in the read-write
transaction, so of a sign-in and a password write asked at once either the session is
there for the write's ending of the sessions to find, or the sign-in finds the new
password and inserts nothing. A pending sign-in completes on the proof its row
carries, whichever step completes it (`CompletePending`, the email code), and a
sign-in held again is held with it: once the password was changed, reset or removed,
the old password completes nothing, however long the row had left and whatever address
the holder gives the code for. A sign-in refused this way is answered as a wrong
password is, 401 in the same words ("The username or password is wrong. Check what
you entered and try again") in the same body, and counts as a failed
attempt where a password was entered (the login, the confirmation); a completion
refused this way leaves its pending sign-in spent, and the person signs in again. A
sign-in no password proved (a directory sign-in waiting for its confirmation or its
factor, a session the application starts) carries no proof and is held to none. A
pending sign-ins table made before the proof takes the column from the auth set's
`SessionPasswordProofs` migration.

One write follows no proof and overwrites on purpose, an operator's removal:
`RemoveSessionUserPassword` writes whatever the row holds (the store's
`RemoveUserPassword`), and so does the generated user
management's removal, which writes the column in the caller's transaction. Neither
claims to know the password being removed, so there is nothing to compare. No call of
the library puts one password over another without a proof of the one held. A reset
link writes on proof too: the password a person chooses there is written over the
password the account held when the link was followed and over no other (the store's
`ChangeUserPassword`, on the hash read with the link), so a password changed or removed
in that moment stands and the link answers 409 ("A password reset", below).

### Password strength

One judgment holds a password's strength on every path that sets one: a sign-up, the account
an invitation's acceptance makes, a first password, a person's own change, the password a
reset link ends in, and `CreateSessionUser` with a password. A password is refused (400, with
the sentence that says why) when it is too easy to guess, when it is built on the person's
own name, username or email address, or when it is among the 100,000 passwords most often
found in data breaches.

How hard a password is to guess is an estimate in bits. The password method's
`MinimumPasswordStrength` option sets the bits a password must reach (default: 30); an
application that wants longer passwords raises it. The estimate reads a password's first 64
characters. It counts a character outside ASCII once, whatever its bytes, and reads an accented
letter as the letter. A password is judged when it is set: one a person already holds still
signs them in.

A page asks the same judgment while the person types. `PasswordMethod.Strength()` takes
`{"password"}` and, when the page has them, the person's `"username"`, `"name"` and
`"email"`, and answers `{"strongEnough", "strength", "minimum"}`: `strength` is where the
password stands from 0 to 100 and `minimum` is where the application's minimum sits on the
same scale, so a page draws a bar and a mark and calculates nothing. `strongEnough` is true
exactly when `strength` has reached `minimum`. While it is false, `"reason"` is one sentence
that says why. Route it behind `ValidateXSRFToken`, outside `ValidateSession` (a person at an
invitation, a sign-up or a reset link has no session), and name its path to `Methods`
(`MethodList.PasswordStrength`).

The route reads no account: it judges against the details the page sends. The path that sets
the password judges against the account's own and has the last word. The password is in a
request body and in no log. A client that asks 3,000 times within five minutes is answered
429 with the wait.

### A person's first password

A person who has only ever signed in through a directory holds no password, and the
change of a password asks for the old one, so they could never give themselves one.
`auth.FirstPassword()` is how they do, signed in, in two steps:

- `SendCode()` takes no body and mails a six-digit code to the address the account holds
  **confirmed**, in place of any code that stood, and answers `{"codeEmail",
  "codeExpiresAt"}`: the address masked, and when the code stops being accepted (the
  sign-in code's lifetime, `WithEmailCodeTimeout`, ten minutes by default).
- `Set()` accepts `{"code", "password"}` and writes the password.

The code ties the password to the mailbox the account holds confirmed: whoever asks must
read that mailbox to finish, and a code counts only while the address it was mailed to is
still the one the account holds confirmed. It is as strong as that address is: a
signed-in person replaces their address with a link mailed to the new one (`Email()`,
above), and a code asked for afterwards goes there. The mail says a code was asked for
from a browser where the person is signed in, and tells them to sign out of the browsers
they do not use.

What is refused, and how:

| Case | Answer |
| --- | --- |
| The account holds a password | 409: it is changed with the old one (`PasswordMethod.ChangeUserPassword`). The store writes a first password only while the row holds none, decided by the write itself (`AuthStore.SetFirstUserPassword`, `sessionstorage.ErrPasswordHeld` otherwise), so of two asked at once one lands. |
| The `Auth` has no password method | 409: nobody signs in to the application with a password, so there is none to choose. |
| The account has no confirmed address | 409 from `SendCode()`, saying to add an address and confirm it from the link first (`Email()`, "Users and their email address"). An address nobody confirmed is not mailed the code: the person has not shown it is theirs, and the code would go wherever whoever typed the address pointed it. A confirmed address no message can be sent to is answered the same way: the simulated build's Google directory vouches for a person under their login name, so a person signed in through it adds their address first. |
| The session views as someone else | 403, on both routes: an impersonated session changes nothing of the account it looks at, as on every own-account route. |
| A wrong code | 400. The fifth within fifteen minutes ends the code and holds the account's first password for fifteen minutes, whatever codes are asked for meanwhile: 429 with `too_many_attempts`, the wait, and words that say wrong codes were what was held. A guess takes its attempt before it is judged, counted against the account in the store's one transaction, which says whether it was counted (`SignInAttempts.Counted`); a guess that was not, the limit reached already, is answered 429 without being compared with the code, so of guesses entered at once no more than five are ever judged, on both databases (the PostgreSQL driver takes the account's attempts row and its lock in one statement, so a failure recorded while the count is cleared is the first after the clear). The right code gives the attempts back. |
| A code entered when none stands | 409: none was sent, or the one sent expired, was used, was replaced by a newer one or was ended by wrong codes. |
| A password the judgment refuses | 400, with the sentence that says why: the password is held to the judgment a sign-up's is (see "Password strength"). The code still stands. |
| A sixth code asked for a mailbox that used none of five | 429 with `too_many_emails` and the wait, the limit the links share (the codes of a sign-in are counted by themselves). |

A first password ends no session: the person added a way to sign in, and nothing
they held is in doubt. Where the `Auth` records auth events, the session the person
chose it in records a password event beside the ones it had, since the person proved
the password is theirs there; the name's count of failed sign-ins is cleared, as a
change clears it. The code is
kept in the mail links table as a row of the kind `first-password` that names the
account (its hash where a link's row keeps its token's), so it goes with the user, and
the guesses are counted in the sign-in attempts table under the account.

The generated router mounts the two routes with the other own-account routes, inside
the validated group, where the auth has the password method
(`/user/first-password/code` and `/user/first-password`), and names them in the methods
list. The account page offers "Choose a password" to a person the server says holds
none.

### A password reset

A person gets back in by a link mailed to the address they confirmed
(`Auth.PasswordReset()`, which needs mail). Three parties send one: an administrator of
the application or of a tenant (`AuthAPI.SendPasswordReset(ctx, id, by)`, where `by` is
`AskedByApplication()` or `AskedByTenant(key)`), and the person, from the sign-in page
(`PasswordReset().Request()`). The message says who asked: "You asked to reset the
password", or "An administrator of <the tenant, or the application> asked us to send
you a link". There is no second mail.

- **Sending changes nothing.** The password the account has signs the person in, and
  their sessions stand, until the link is used.
- **The link works once**, for `MailSettings.LinkTimeout`, and for the account whose
  confirmed address it was mailed to: it is dead once that address or the password
  changed. Every write and removal of a password in the library withdraws the
  account's standing reset links, and an application that writes one in rows of its
  own says so with `AuthAPI.EndPasswordResetLinks(ctx, id)`. `PasswordReset().Open()` answers the username of a live link and
  `PasswordReset().Complete()` takes the new password; the login page reaches both with
  `#reset=<token>`. The new password is held to the same judgment as any other (see
  "Password strength"): a password the judgment refuses (400) spends nothing, and the
  link still opens. Once one is set, the old one is refused from then on, every session
  of the person ends, the other reset links of the account are withdrawn, and the person
  signs in with the new password: completing a reset starts no session.
- **The link writes over the password it was followed for, and no other.** Completing
  it spends the link and then writes the new password only while the account still
  holds the password it held when the link was read (`AuthStore.ChangeUserPassword`,
  on that hash). A password changed or removed in that moment stands: nothing is
  written, no session ends, and the answer is 409, "Your password was changed while you
  were choosing a new one, so this link did not change it. Sign in with your password,
  or ask for a new link". The link stays spent.
- **It goes to a confirmed address and to an account that has a password.** An
  administrator is told in one sentence when no link was sent (Conflict). A person's
  own request answers the same whether or not the address has an account, mails
  nothing where there is none, and does its work after the answer, so neither the
  answer nor its timing says which. `WaitForMail` waits for that work, so a shutdown
  loses no link a person was promised: the auth package `impulse` writes calls it in
  its `Close`.
- **A limit of their own.** Reset links are counted per address, five in fifteen
  minutes, then fifteen minutes closed, whoever asked: an administrator's link and the
  person's own request share the one count, and a link that is used resets it. The
  count is kept apart from the one for sign-in codes and the other links: a request
  anyone can make for an address never uses up the codes a person who is past their
  password needs, and a code that is used does not reopen the address to reset links.
- **Self-service is a setting, read on each request.** The person's own request is
  served only while the password method's row of the sign-in methods table says so
  (`SelfServiceReset`, false until an administrator turns it on, and false with no
  row): turning it on or off takes effect in the running application with no restart.
  `AuthAPI.SetSelfServiceReset(ctx, on)` writes that one field and leaves what else
  the row says as it is.
  While it is off the request is NotFound, in words that say to ask an administrator,
  and the methods listing leaves the request route out, so the page draws no "Forgot
  your password?". A link a person asked for while it was on still works once it is
  turned off, until it is used or runs out: turning it off stops new requests, and
  takes back nothing already mailed.

The methods listing names the routes (`MethodList.PasswordReset`, and
`MethodList.Email.Confirm` for the confirmation of an address an administrator set).

### Users and their email address

A user may hold one email address, in two states. **Verified before trusted**: an
address is trusted for nothing until the person has proven they read it by following
a link sent to it, and the user record carries the time that happened
(`SessionUser.EmailVerifiedAt`, `UserInfo.EmailVerified()`). Code that sends a link
may use an unverified address; code that relies on one (linking a social identity, a
recovery) requires the time. No address is required of any user: an account without a
verified address has no mail-based recovery, and multi-factor authentication can serve
instead. The address is unique among every address held, verified or not, matched
without regard to the case of its ASCII letters (`NormalizedEmail`), so an address is
held by one account at a time. Nothing else is folded: an address written with a letter
from outside ASCII is the same address only when it is written with that same letter, in
the stores' lookups by address and the address's unique index as in every comparison the
library makes. A username is matched more widely (`NormalizedUsername`: the database's
Unicode normalization and case folding), so on a store whose usernames are the addresses
two addresses that differ only by such a letter cannot both be accounts, and a sign-in
name written with one names the account written with the other. A password sign-in
under such a name still needs that account's password. A first sign-in that is linked
on the provider's word for the name (see "A first link on a name alone") is linked to
the account the wider match finds.

How an address gets onto a user:

- **The person adds and verifies their own**, signed in: `Email().Add()` takes
  `{"email"}` and mails the address a link; nothing is written until the person
  follows it and the page posts the token to `Email().Verify()`, which writes the
  address verified, replacing whatever the user held. The link counts only for the
  account that asked for it, so following a link never attaches an address to an
  account the follower does not control. A directory's claim (Entra's user principal
  name, Google's address) is offered as a prefill of that step through the session
  read's `emailHint`, and never stored by it. `Add` answers `{}` whatever the address's
  state, so the screen reveals nothing: an address another account holds is told so
  by mail to that address, and an address the user already holds verified gets no
  mail. A mailbox is sent at most five links within fifteen minutes that go unused; the
  sixth is refused with `too_many_emails` and the wait. A request takes its place under
  the limit before anything is mailed, so of links asked for at once no more than five
  are mailed. A link that is followed resets the count, as a sign-in code that is used
  does.
- **An operator enters one at creation**, unverified: `CreateUserRequest.Email`. The
  entry reserves the address within the auth: a later sign-up with it is pointed at
  this account by mail, and the first code mailed to it at a sign-in confirms it.
- **An administrator asks for a change** (`RequestAddressChange`): a link goes to the
  new address, good for `MailSettings.LinkTimeout` and once, and the account keeps the
  address it has, where its codes and links still go, until the link is followed. The
  link is followed without a session (`Email().Confirm()`, a public route the login
  page reaches with `#address=<token>`), since the person may have lost the mailbox
  their sign-in code went to. Nothing is mailed to the old address to confirm the
  change; once it is made, the old address is told (a notice with the new address
  masked, and no link). An address another account holds is told so by mail and gets
  no link, and the answer is the same. One change stands at a time: asking again
  withdraws the links asked for before, and a link is dead once the account confirmed
  any address after it was mailed. A sign-in code never replaces an address the
  account holds, confirmed or not: a code mailed to an address the account no longer
  holds (replaced, taken off the account or moved to another account) is dead
  (`code_expired`), and nothing is written. Taking an address off an account
  (`AuthStore.SetUserEmail` with no address) ends, in the same transaction, the
  sign-in codes mailed for the account's sign-ins before it, so a code mailed before
  an administrator's change and entered after it is dead and writes nothing back; the
  code for an address the person then gives at the step writes it. A change that lands
  while a code is being entered ends that code as well: the address a code confirms is
  written only while the account still holds what it held before the code was used,
  and a completion that finds it changed, or finds that another account took the
  address, is refused (`code_expired`) with nothing written and nobody linked or
  signed in. Nothing writes an address onto an account unconfirmed after its creation.
- **A directory keeps its people's**: the address of a person with an identity linked
  through an organization's directory (`signin.Directory.Organization()`) is changed in
  the directory. Their own add and verify, an administrator's request and the
  confirmation of a link sent before the identity was linked are each refused
  (Conflict); a directory person who holds no confirmed address may still confirm
  their first, and so may one whose directory vouched for a value no message can be
  sent to, as the simulated build's Google directory does under a login name.
- **Only Google vouches for an address, and every other sign-in leaves it to a
  code**: a sign-in through an external method that does not vouch for the address
  (Entra, WorkOS, an OpenID Connect provider whatever its `email_verified` says, a
  method of the application's own, and a Google account whose address is neither
  Gmail's nor a Workspace account's) is held for the email code while the account
  holds no confirmed address, whatever the token attests and
  whatever the sign-in policy says about a second factor (`PendingMFA`, the email code
  its one factor). The person gives the address where the account has none
  (`Pending().EnrollEmail()`), and the code confirms it; their next sign-in is not held
  for it. A registration of seeded users holds nobody: the deployment wrote those
  accounts itself.
- **Google writes its own**: a Google sign-in writes the address Google verified
  onto the user, verified at the time of the sign-in, when the user holds no verified
  address yet and no other user holds it. Google's word counts only where Google is
  the address's authority: a Gmail address (`gmail.com`, `googlemail.com`) and the
  address of a Workspace account (the token carries `hd`). A Google account made with
  an address from anywhere else is treated as another provider's. Where another user
  holds the address verified and the user the sign-in reaches holds no verified
  address, the address would be written on nobody and the person signed in to an
  account with no confirmed address, so the sign-in is refused (`identity_rejected`;
  the cause in the log is `signin.ErrAddressHeldByAnother`, which names both users)
  and links nothing. An account made ahead under the address as its sign-in name,
  beside the account that holds the address, is reached so, and an administrator
  settles which is the person's by removing the duplicate account or its sign-in name.
  An address another user holds unconfirmed refuses nobody, and neither does a
  registration that holds no sign-in for an address (the point above). No other method's
  word is kept, whatever it puts in `Identity.EmailVerified` and whatever it is
  named: the people who run a provider can mark any address verified, so an Entra
  user's address, or one an OpenID Connect provider calls verified, stays a prefill
  until the person verifies it. Google's method is the one `session/google` registers,
  which the library knows by a mark only its own packages can set
  (`signin.Method.AddressAuthority`): a method of the application's own that names
  itself `google` is not Google's, and its word is not kept. A sign-in that waits is
  held to the same answer when it is read and when it is finished, whatever its row
  holds: one stored with another provider's word, by a release of the library that
  kept every provider's word, confirms no address when it is finished. The other way
  round, a first Google sign-in by a person whose address a user already holds verified
  is that user's, where Google vouches for the address: the standard account rule
  matches the user by the address once no user is under the sign-in name (see "Storage
  and schema" above).
- **An invitation's code or a sign-up proves it**: the account an invited person
  creates, or an existing account that accepts an invitation while holding no verified
  address, takes the invitation's address verified once the person has entered the code
  mailed to it (or signed in through a directory that vouches for it); the invitation's
  link alone writes nothing. A sign-up's account is created with its address verified,
  by the link the person asked to be mailed.

**Two shapes.** An application declares on the password method whether the username
is the email address (`session.UsernameIsEmail()`), and the users table takes the
matching shape:

- **Apart** (the default): the username is chosen, the address is added and verified
  separately, and sign-in is by username. The auth set's `SessionUserEmail` migration
  creates the columns as plain columns.
- **One value** (`UsernameIsEmail()`): the address is the username, derived from it by
  the database so the two cannot diverge. The login page asks for the address, a
  sign-up's username is the address, renaming a user is refused
  (`ChangeSessionUserUsername`: Conflict) as is an operator's entry of an address, and
  a person who verifies a new address is renamed to it with their live sessions, as
  the rename does; an application that keys anything of its own by the username is
  told of that rename through `auth.SetUserRenamed(func(ctx, userID, from, to) error)`
  and moves what it holds. Role memberships are not such a thing: the permission store
  names a person by the id of their account, which a rename leaves alone. The store is told the shape when
  the method registers. The users
  table is created with the address columns generated from the username, in place of
  the migration's first two statements:

  ```sql
  -- Spanner
  ALTER TABLE SessionUsers ADD COLUMN Email STRING(MAX) AS (Username) STORED;
  ALTER TABLE SessionUsers ADD COLUMN NormalizedEmail STRING(MAX) AS (CAST(LOWER(CAST(Username AS BYTES)) AS STRING)) STORED;
  -- PostgreSQL
  ALTER TABLE "SessionUsers" ADD COLUMN "Email" character varying GENERATED ALWAYS AS ("Username") STORED;
  ALTER TABLE "SessionUsers" ADD COLUMN "NormalizedEmail" character varying COLLATE "C" GENERATED ALWAYS AS (lower("Username" COLLATE "C")) STORED;
  ```

  `EmailVerifiedAt`, the unique index on `NormalizedEmail` and the index by username,
  which carries the address, are as the migration has them. Sign-in by address when the
  two are apart is not offered.

The session read (`Authenticated`) reports `email`, `emailVerified` and, for a user
without an address, `emailHint`.

### A person's name

A user may hold a name: the person's name as people read it ("Gale Reyes"), which a
page shows in place of the username. It identifies nobody. The username stays what a
person signs in under, and a role membership names them by the id of their account, so
writing a name renames nobody, ends no session and moves no role. No name is required
of any user,
and two people may hold the same one. A name is kept without the space around it, a
name of nothing but space is no name, and one longer than 200 characters, or one that
is not text on one line (a line break, a tab or another control character in it), is
refused as a bad request (`sessioninfo.CleanName`, `sessioninfo.MaxNameLength`):
everything that writes a name holds it to that one rule.

How a name gets onto a user:

- **A directory asserts it at a sign-in.** The Google, Microsoft Entra and generic
  OpenID Connect methods read the `name` claim and WorkOS the profile's first and last
  name (`sessioninfo.Identity.Name`). A sign-in that creates the account writes the
  name with it. A sign-in to an account that exists fills the name **only where the
  user holds none**, after the session stands, so a name the person or an operator
  wrote is never replaced by the directory's. A name the rule would refuse is dropped
  before anything of the sign-in is written, and the sign-in stands
  (`sessioninfo.KeptName`).
- **An invitation carries it.** `InvitationRequest.Name` is the name the sender gives
  the person. An account the acceptance creates takes it unless the sign-in asserted a
  name of its own, and an existing account that accepts takes it only where it holds
  none.
- **The person writes their own**, signed in: `Auth.ChangeName()` takes `{"name"}` and
  answers `{"name"}`, the name as it is kept, absent when the person now holds none
  (an empty name takes it off; the next directory sign-in then fills it again). The
  handler validates the session itself, as the password change does, so it is routed
  beside it outside `ValidateSession`, and it refuses an impersonated session: the name
  is the person's own to write. The generated router mounts it at `/user/name` and
  names it in the methods list (`MethodList.ChangeName`).
- **An operator writes it**: `CreateSessionUser` takes `CreateUserRequest.Name`,
  `SetSessionUserName(ctx, id, name)` replaces the name whatever the user held (nil
  takes it off), and the management an application generates for an auth's people
  creates and invites with a name and changes one (`Set<Auth>UserName`).

The session
read (`Authenticated`) reports `name` when the user holds one, and `UserInfo.Name`
carries it to the application's handlers.

A users table made before the name existed takes the column from the auth set's
`SessionNames` migration, which also adds the name an invitation carries to the mail
links and the name a waiting sign-in's identity asserted to the pending sign-ins, and
stores the name in the two indexes a list reads without a row of the table.

### The look a person chose

A user may hold a look: light, dark or whichever their device is set to, and one of the
palettes the application offers (`sessioninfo.Look{Scheme, Palette}`). It is the person's own choice for how the
application's pages are drawn for them, kept with the account so that it follows them to
every browser they sign in from. Nobody else sets it: there is no operator's entry for
it, and the management an application generates for an auth's people neither lists nor
changes it.

- **The rule** (`sessioninfo.CleanLook`): the scheme is `light`, `dark`, `device` (a page
  then follows the setting of the device it is drawn on) or none; the palette is an id as
  the application names its palettes, kept without the space around it and at most 64
  characters (`sessioninfo.MaxPaletteLength`), or none. The session library does not know
  an application's palettes, so a page drops an id its application no longer offers. Any
  other scheme, a longer id, and an id with a control character in it are a bad request.
- **The person's pages write it**, signed in: `Auth.ChangeLook()` takes `{"scheme",
  "palette"}` and answers the look as it is kept, each half absent when the person now
  holds none. The look is replaced as a whole, so a half left out is taken off. The
  handler validates the session itself, as the password change does, so it is routed
  beside it outside `ValidateSession`, and it refuses an impersonated session: the look
  is the person's own to choose. The generated router mounts it at `/user/look` and
  names it in the methods list (`MethodList.ChangeLook`).
- **The session read** (`Authenticated`) reports `look` when the user holds one, and
  `UserInfo.Look` carries it to the application's handlers. A browser applies it once
  the session is checked and keeps a copy of its own for the login page.

A users table made before the look existed takes the two columns, `LookScheme` and
`LookPalette`, from the auth set's `SessionLooks` migration.

### Invitations

An invitation is a mailed link that brings a person into the application, or into
one of its tenants, with the roles it carries. It reaches anyone by address, and the
actor's request is the same whether an account already holds the address or not: only
the invitee's path differs, and the actor does not learn which. It is accepted only by
the account it was sent to: a directory sign-in as another address, a password sign-in
by an account that holds another address, and the acceptance by hand of a person signed
in as such an account are refused (`invitation_address`), nothing consumed and the
invitation left standing for the invited account. The link only opens the invitation:
a mailed link can be forwarded, so holding one proves no address. Wherever the sign-in
does not itself prove the invited address (a directory that vouches for it does, and so
does an account that already holds it confirmed), the acceptance waits for a code mailed
to the invited address: a sign-up with a password, a password account that has not
confirmed the address, and a directory that sends no address or does not vouch for the
one it sends.

- **Sending**: `auth.API().Invite(ctx, &session.InvitationRequest{Email, Name, TenantID,
  EveryTenant, RoleNames, InvitedByUserID})` stores the invitation and mails the link; `Name`, optional, is
  the name the sender gives the person (see "A person's name"), and `NewTenant`, in
  place of a tenant and roles, makes the invitation one that makes a tenant (below).
  `TenantID` and `EveryTenant` are the place the roles are held in: the tenant
  `TenantID` names, every tenant when `EveryTenant` is set, and the application's
  global surface when neither is. The invitation stores the place, and the acceptance
  hook is handed it (`sessioninfo.Invitation.TenantID` and `EveryTenant`), so the
  application gives each role there and nowhere else, whatever another place holds
  under the same name by the time the person accepts. A request that names a tenant
  and every tenant, or makes a tenant and says every tenant, is a bad request, in
  words.
  The invitation stands for
  `MailSettings.InvitationTimeout` (seven days). `Invitations(ctx)` lists those that
  stand; `RevokeInvitation(ctx, id)` withdraws one. The tenant and the roles are the
  application's, opaque to the library.
- **Opening**: the login page, sent to with `#invitation=<token>`, posts the token to
  `Invitation().Open()`, which answers `{"email", "tenantId", "tenantName", "newTenant",
  "directoryOnly", "expiresAt", "signUp", "usernameIsEmail", "methods", "accountExists",
  "hasPassword"}` and sets an encrypted cookie holding the token, so the
  sign-in that follows carries the invitation, an external round trip included. The
  page then offers the sign-in methods, and, when `signUp` is true (the password method
  is registered, and the invitation is not one accepted with an organization's account
  alone), an account with a password. `methods` names the listed external
  registrations the server will accept the invitation through for the invited address,
  by the names the methods listing advertises them under (`[]` when there is none; the
  password method is never among them), so the page offers no button that would refuse
  the invitee. For a new person, for an account that has a password and for an account
  nobody can sign in to yet, they are the registrations that could admit the address.
  For an account that has no password and signs in through a provider, they are the
  registrations that account signs in through and no other: the acceptance never gives
  such an account a second sign-in. `accountExists` and `hasPassword` say whether an
  account already holds the address and signs in with a password, so the page offers
  that account's password form, and offers to create an account, beside the other
  methods, only where none holds the address. `tenantId` is the tenant the invitation
  seats the person in, and `tenantName` what people call it: the person reading the
  screen is not signed in and can read no tenant record, and a tenant's id says nothing
  to them, so the application hands each Auth its tenant roster
  (`SetTenantNames(roster)`, after NewAuth and before serving), which names the tenant
  from its copy with no read. Without one, or for a tenant it does not know, the name is
  absent and the screen names no tenant. While the cookie names an invitation that
  stands, the methods listing (`auth.Methods`) reports it as `invitation.standing`, so
  the page shows the invitation again when it comes back without the token, as after a
  refused external sign-in; see "What the login page is told".
- **Accepting**: a sign-in that carries an invitation accepts it once the user is
  known, before the sign-in policy decides, so the policy sees the membership. The
  sign-in is first held to the address: the one it answers for, the address the method
  verified for an external identity (Google's) and the account's own for a password
  one, must be the invited one,
  compared without regard to case, or the sign-in is refused before anything is read
  or written (`invitation_address`, the login page naming the invited address), the
  invitation standing for the account it was sent to; a password account with no
  address is not refused here, and is held to the code as said below. An external
  identity that asserts no address (a Microsoft Entra
  sign-in, unless the registration is set up to send one), or asserts one its method
  did not verify (the one a Microsoft Entra sign-in then carries, which is whatever the
  directory's administrator wrote, and anybody can run a directory), has nothing to
  hold to the invited one, whether the address it asserts is the invited one or
  another, so the sign-in waits for a code instead
  (`pending=mfa`, the one factor `email-code`): the code is mailed to the invited
  address, masked on the page, with no address asked for (`Pending().EnrollEmail()`
  answers 409 there), and the invitation is accepted in the transaction of the sign-in
  the code completes, the new account made with the invited address verified. The code
  proves the address and gives no way in to an account somebody can already sign in to.
  It comes in a message of its own, never the sign-in code's: the message says that the
  code accepts an invitation to the application sent to this address, that only the
  person invited should enter it, and that somebody else has the invitation's link if
  they did not open the invitation themselves, so they should tell the person who
  invited them. It speaks of no password, since the person may have no account yet.
  The account that holds the invited address, verified or not (an address an
  administrator entered is kept for its account), is the one invited: with a password
  it is held for the password as any link is, without one and signing in through an
  identity it is refused (`identity_rejected`), and only where nobody signs in to it yet
  is it linked; no second account is made beside it. A person the identity is already
  linked to accepts at once where their account holds the invited address verified, and
  through the code, which then verifies the address onto the account, where it has
  verified none; an account that holds another verified address, and any account while
  another one holds the invited address, is refused (`invitation_address`), as is an
  account under another name on a store whose usernames are the addresses, which could
  not take the invited one. The code is the proof of that invitation's acceptance and of
  nothing else: entered once the invitation was used, withdrawn or ran out, it signs
  nobody in and writes nothing (`invitation_expired`), though the method would have
  admitted the person without an invitation. Through a registration whose accounts
  the deployment seeds itself, no code is given: the address the identity
  asserts is compared with the invited one, verified or not, and a sign-in that asserts
  none is refused. An account that names itself, with its password or in a session
  the application starts for it, proves nothing of the invited address by holding the
  link, so it accepts at once only where it holds that address confirmed. Where it
  holds no address, or holds the invited one and has not confirmed it, the sign-in
  waits for the code the same way (`pending=mfa`, the one factor `email-code`), the
  wait naming the account: an account with no address is mailed the invitation's code
  at the invited address, with no address asked for, and one that holds the invited
  address unconfirmed is mailed its own sign-in code there. Until the code is entered
  the invitation stands, nothing is granted and the account takes no address; entering
  it accepts the invitation and writes the address on the account, confirmed. The
  invitation's code is held to its invitation here as well: it puts the invited address
  on the account in the acceptance's transaction and nowhere else, so the code entered
  in a browser that does not carry that invitation, or once the invitation was used or
  withdrawn, signs nobody in, takes its place in the invitation's count and writes
  nothing else, and the code of an invitation that has taken its twenty-five codes is
  not counted, signs nobody in and writes nothing. The
  acceptance by hand of a person signed in
  (`Invitation().Accept()`) is held to the address the same way, answered 403 with the
  sentence. A session cannot wait for a code, so a person whose account has not
  confirmed the invited address is answered 403 too (`invitation_address`), with a
  sentence that says to sign in again from the invitation's link, where
  the code is mailed. Then the
  invitation is consumed, the user takes its address when they hold no verified one,
  and the application's hook grants what it names. Only a sign-in the resolution
  admitted consumes it: one the account rules refuse or hold for a confirmation, one
  the directory's roles refuse (`no_roles`), or a disabled user's leaves the
  invitation standing with nothing granted, and the person may sign in another way;
  once accepted, a sign-in the policy then denies leaves what the acceptance granted
  in place. The hook is set with
  `auth.SetInvitationAccepted(func(ctx, tx, userID, username, inv) (committed func(ctx), err error))`
  and runs inside the transaction that consumes the invitation, which it is handed
  (for an external identity, the resolution's own, the one that provisions a user the
  invitation admits): the invitation is spent and its roles given together, or
  neither. The hook reads and writes through `tx` alone, since the Spanner emulator
  allows one read-write transaction at a time and a second one, or a read through
  another client, inside the hook waits on the transaction it runs in; a user the
  transaction provisioned is buffered and not readable in it, so the hook writes under
  the id it is given. `committed`, nil for none, is what the library runs once
  the transaction has committed and before the policy decides (the word to a
  permission engine that its rows changed), once, for the run of the transaction that
  committed. A hook that fails fails the transaction and ends the sign-in with its
  error: the invitation stands, and a user the sign-in would have provisioned is not
  created. An account a sign-up through the invitation made stays: it is created
  once the code is entered, before the sign-in that accepts the invitation, and holds
  the invited address confirmed and nothing else. Through a method that is invite only
  the invitation is the account: a
  stranger who carries one is provisioned under their sign-in name, and so is a
  stranger from a directory a tenant declares with no default seat
  (`SetDirectoryAdmission`). A person already
  signed in accepts with `Invitation().Accept()` (behind `ValidateSession`); an
  invited person creates an account with a password through `Invitation().SignUp()`
  (`{"password", "username"}`), which makes nobody yet: it answers
  `{"mfaIsRequired": true}` and holds the sign-up as a pending sign-in that names no
  user, for the code mailed to the invited address (`Pending().SendCode()`), the
  username and the password's hash kept in the browser's encrypted pending cookie and
  bound to that invitation. A username or an address another account holds is answered
  409 before anything is held or mailed. `Pending().VerifyCode()` with the code then
  creates the user with the invitation's address verified and signs them in, the
  invitation accepted on the way. Five wrong codes end the code with nothing written,
  and the application's own step (`CompletePending`) refuses such a wait (409).
  `NewSessionRequest.Invitation`
  carries it to the policy, with `Accepted` saying whether this sign-in accepted it.
  The hook is told how the person signed in: the invitation it is handed carries
  `SignInMethod` and `SignInConnection`, the method and connection of the sign-in that
  carries the invitation, and of the session's first auth event for an acceptance by
  hand (empty for a session that records no events).
- **Where the application opens afterwards**: a person who accepts an invitation into a
  tenant came for that tenant, so the application opens in it, whatever other tenants
  the person holds a seat in and wherever they last worked. A person whose invitation
  makes a tenant came for the tenant it made, and the application opens there. The
  library says which tenant, once: the first session read (`Authenticated`) after the
  acceptance answers `invitedTenant`, the invitation's tenant or the one its acceptance
  made, and no later read does. The acceptance (a
  sign-in that accepts the invitation, one that waits for a step first, or
  `Invitation().Accept()`) sets an encrypted, HttpOnly cookie named after the
  invitation cookie with `-seated` after it (`auth-invitation-seated`), holding the
  tenant and the user; it stands for `WithPendingTimeout`. The first session read of
  that user's own session answers the tenant and deletes the cookie. A read of another
  user's session deletes it and is told nothing, and an impersonated session reads
  none. The browser library lands the person there (`PlaceService`); an application
  with pages of its own reads the field. An invitation that makes a tenant names none,
  since the tenant does not exist until the acceptance: the acceptance hook tells the
  library the key of the tenant it made (`Invitation.MadeTenantID`, which the generated
  user management sets), and a hook of the application's own that leaves it empty
  lands the person as any sign-in does. An invitation into the application names no
  tenant, and lands the same way. `NewAuth` refuses another cookie of the Auth
  configured under the name.
- **An invitation that makes a tenant**: staff bring an organization into a tenanted
  application by inviting one person, and that person's first sign-in makes the tenant.
  The request carries `NewTenant` (`sessioninfo.NewTenant{Name, DirectoryOnly,
  Details}`) in place of a tenant and roles: such an invitation names no tenant and
  carries no roles, since the tenant does not exist yet and whoever accepts it becomes
  its first administrator, and the tenant's name is required. `Invite` and
  `PrepareInvitation` answer a bad request, in words, for a request that says
  otherwise. `Name` is what the invitation page shows the person before they sign in:
  `Invitation().Open()` and the methods listing answer `newTenant` true and the name as
  `tenantName`, with no `tenantId`, since the tenant has none yet. `Details` is the
  application's own JSON document, whatever else staff entered for the tenant: the
  library stores it with the invitation, hands it to the acceptance hook and reads none
  of it. No handler of the library answers it, and the mailed message is the message of
  any invitation: it carries nothing of the tenant, its name included, since a message
  is read by whoever holds the mailbox before anyone has signed in.

  The acceptance hook makes the tenant. It is handed the invitation with `NewTenant`
  and how the person signed in, in the transaction that consumes the invitation, so the
  tenant, the person's seat in it and the spent invitation are written together or not
  at all. `signin.Directory{Method: inv.SignInMethod, Connection:
  inv.SignInConnection}.Organization()` says whether the person came through an
  organization's directory (a Google Workspace, a Microsoft Entra tenant), which the
  hook may then write as the tenant's own. A hook that will not make the tenant answers
  a `sessioninfo.LoginRefusal` with a code of its own: the sign-in ends on that code,
  nothing is written and the invitation stands. `sessioninfo.RefusedOrganizationHasTenant`
  (`organization_has_tenant`) is the code for an organization whose directory another
  tenant already has; `Invitation().Accept()` answers it 403 with a sentence that says
  what to do and nothing of that tenant, the browser still carrying the invitation.
  An account a sign-up through the invitation made stays where the hook then refuses,
  as for any invitation: it is created once the code is entered, before the sign-in that
  carries the invitation, holds the invited address confirmed and nothing else, and
  accepts the invitation by signing in once it can be accepted. An Auth
  with no acceptance hook (`SetInvitationAccepted`) never consumes an
  invitation that makes a tenant, since nothing would make it: the sign-in that carries
  one and the acceptance by hand fail, and the invitation stands.

  `DirectoryOnly` says the tenant is entered through its organization's directory
  alone, and the library holds the acceptance to it. A sign-in that carries such an
  invitation with a password, with a personal account (a Google account in no
  Workspace, a Microsoft personal account) or through anything else that is no
  organization's directory is refused (`organization_account_required`) before
  anything is read or written: no account is made, and the invitation stands for the
  sign-in with the organization's account. `Invitation().Open()` answers
  `directoryOnly` true and `signUp` false for it, so the page offers the directory
  buttons and no account with a password; `Invitation().SignUp()` refuses it with the
  same code before any account is created; and `Invitation().Accept()` answers a
  session that was not established through an organization's directory 403 with the
  sentence, the invitation standing.
- **A dead invitation** (used, revoked or expired) is skipped by a sign-in the person
  was admitted to anyway, which goes on without a grant, and refuses one the invitation
  alone admitted (`invitation_expired`), provisioning nothing. A hook that fails ends
  the sign-in with its error and writes no session; the invitation stays consumed and a
  user it provisioned stays, so the person's next attempt at the link finds it dead and
  an administrator invites again.

An invitation is refused before anything is stored or mailed when no sign-in method the
login page offers could admit the address, so the sender learns it now rather than the
invitee at the sign-in: `Invite` answers a bad request reading `No sign-in method of
this application accepts an address at <domain>.` The password method, when it is
listed to `Methods`, admits any address, since the invited person creates an account
with a password; a Google registration admits an address at a domain of its named
Workspaces, any domain but `gmail.com` and `googlemail.com` under `AnyWorkspace`, and
any under `AnyGoogleAccount`; a Microsoft Entra registration refuses only a personal
account's address (`outlook.com`, `hotmail.com`, `live.com`, `msn.com`) unless it admits
`PersonalAccounts`, since a tenant is not a domain; WorkOS and a method of your own that
sets no `signin.Method.AdmitsAddress` cannot tell, which admits. Google puts the
Workspace's primary domain in the `hd` claim, so an invitation to an address at a
secondary domain of a Workspace whose primary domain alone is named is refused here
although Google would admit the sign-in: name the secondary domain on the Workspace too.

### The first administrator's invitation

The first administrator of a deployed application comes in as everyone after them does:
by an invitation mailed to their own address, which they accept by signing in through a
method of the Auth. The deployment names the address, and its release asks for the
invitation while nobody of the Auth holds the Super Administrator role
(`accesstypes.SuperAdministrator`). No account is created ahead of the person and no
password is generated for them, so nothing stands that somebody other than the mailbox's
owner could sign in with, and no secret exists that a person would have to read. With
the password method the person chooses a password from the link and enters the code
mailed to the address, which proves it theirs; through a directory the invitation is what ties the sign-in to the roles, whoever
else the directory admits and however many organizations the method is open to, and the
sign-in is held to the invited address, as for every invitation.

An Auth whose only sign-in method answers for no address accepts the invitation by a
mailed code. The Microsoft Entra method asks the directory for the `openid` and `profile`
scopes alone, so its sign-ins carry no address unless the registration itself is set up
to send one, and the one they then carry is the directory's word, which the method does
not verify. Either way the sign-in has nothing to hold to the invited one: the person
who opened the link and signed in is held at the code, which goes to the invited address
and to no other, and entering it makes the account, with the address verified, and gives
the role (see "Invitations", Accepting). Somebody who has the link and not the mailbox
gets no further than the code.

The role is what the release asks about, and not whether the Auth has an account. The
invitation exists so that somebody administers the Auth, and an account that holds
nothing (a person who signed up, one a directory seated, a persona a test environment
seeds) administers nothing and stops no invitation. A holder counts when the id the
role store lists is an account of this Auth; the account may be disabled, since the
last holder who can sign in keeps the role and the account whoever asks, so the
question is who holds the role, not who can sign in. A persona a test environment
seeds holding the role is a holder: such an environment seeds its cast before this
step, invites nobody and needs no mailbox.

`AuthAPI.InviteFirstAdministrator(ctx, roles, &session.FirstAdministratorRequest{Email, RoleNames})`
is the step a release runs. `roles` is the role store it asks who holds the Super
Administrator role (`session.RoleHolders`, which the access library's user manager is:
pass the manager of the Auth's own permission engine, the one the acceptance hook gives
the roles through); a call handed none is refused. It answers what it did
(`session.FirstAdministrator`: the outcome, the address, when the invitation runs out
and the addresses of the invitations it withdrew), and never the link or its token,
which are in the mailed message alone:

- Somebody holds the Super Administrator role: nobody is invited
  (`FirstAdministratorRoleHeld`), nothing is mailed, and neither an address nor a
  mailbox is asked for; every person after the first is invited from the application.
  One thing is still done: an invitation of the deployment's that still stands for an
  address the deployment no longer names is withdrawn, so no link stays open for an
  address it took back. One to the address still named is left to be accepted, with the
  roles it was sent with.
- An invitation to the address with those roles stands: nothing is mailed
  (`FirstAdministratorStanding`), so a release while the person has not yet accepted
  sends no second message.
- None stands, because none was sent or the one sent has run out
  (`MailSettings.InvitationTimeout`, seven days unless set): one is mailed
  (`FirstAdministratorInvited`).
- One stands that the deployment no longer names: it is withdrawn, so its link no longer
  opens, and the address named now is mailed. That is so for another address, and for
  other roles: an invitation carries the roles it was sent with, so a release that
  names a changed set of roles replaces the standing invitation with one that gives
  the set named now, and the person is mailed again.

`AuthAPI.ReinviteFirstAdministrator` is the same on demand, for a message that was lost:
it withdraws the standing invitation and mails a fresh one. It is refused once somebody
holds the Super Administrator role (`ErrFirstAdministratorRoleHeld`), and a refusal
changes nothing; an account that holds no such role does not refuse it.

While nobody holds the role, both refuse, with nothing mailed, an empty address
(`ErrFirstAdministratorUnnamed`) and an Auth whose sender delivers nothing
(`ErrFirstAdministratorNoMailbox`). The second is
the rule that the release never writes an invitation's link to a log: a sender says
itself whether it hands its messages to a mail system (`mail.Deliverer`, which the
Workspace relay declares), `mail.Delivers` takes a sender that says nothing, as
`mail.Log` and anything that wraps it, for one that does not, and whoever reads the link
of the first administrator's invitation becomes the administrator. An application's own
driver declares `Delivers() bool` before it can carry this invitation. An
invitation the deployment no longer names is withdrawn before either refusal, and the
error says so. The checks every invitation is held to apply too: an address no sign-in
method of the Auth could admit is refused, and a message that could not be sent leaves no
invitation standing.

The first administrator is the deployment's first super administrator, the one person
who can make another, so the roles name the Super Administrator role
(`accesstypes.SuperAdministrator`), and a request without it is refused. That role is
also how the deployment's own invitation is told from every other: it is an invitation
into the application that gives the Super Administrator role and that no user of the
Auth sent, which only the application acting for itself writes. Those are the
invitations the two calls read and withdraw, and they touch no invitation a user sent,
none into a tenant, none that makes a tenant and none that gives other roles alone. The
roles are given by the application's acceptance hook
(`SetInvitationAccepted`), as any invitation's are. The invitation is for the whole
application and stores no tenant; an application whose first administrator holds roles
in every tenant as well tells its hook which roles those are, and the hook gives each
role where the application declared it (the generated management's
`identity.WithFirstAdministratorRoles`). An Auth with no such hook gives an
accepted invitation nothing: nobody would come to hold the role and every release would
mail the address again, so both calls refuse such an Auth, whoever holds the role, as
they refuse a call handed no role store.
Nothing here locks: a deployment runs one release at a time, and the step is its.

### Sign-up

While an administrator has the password method open to sign-up (its row of the sign-in
methods table; see "Open to sign-up, or invite only"), anyone may create
an account: the person gives their address to `SignUp().Start()` (`{"email"}`), which
mails a link standing `MailSettings.LinkTimeout` (a day); the login page, sent to with
`#signUp=<token>`, posts the token to `SignUp().Open()` for `{"email",
"usernameIsEmail"}` and then, with the password the person chose (a password the
judgment refuses is 400) and the username when the two are apart, to `SignUp().Complete()`, which
creates the account with the address verified and the method's default role, and signs
the person in, answering as the password login does. `Start` answers `{}` whatever the
address's state: an address an account already holds is pointed at that account by
mail, and the screen says nothing.

Every application with the password method routes the three
handlers; nothing in code offers or withholds sign-up. While the method is invite only,
which it is until an administrator opens it, the handlers answer 404 and `Methods`
leaves the block out of its answer, so the login page offers nothing. `Complete` makes
the account in one transaction that reads the method's row, spends the link, writes the
user and gives the default role through the application's hook (`SetSignUpSeated`): a
link mailed while the method was open creates nothing once it is invite only again, and
is not spent by the refusal.

### Attempt limiting and the after-sign-in hook

The password method limits failed attempts itself. Five wrong passwords on a name
within fifteen minutes lock the name for fifteen minutes, whether a user exists under
the name or not; an attempt during the lock is refused with `too_many_attempts` and the
wait, before the user is looked up or the password hashed; the right password resets
the count. An attempt takes its place under the limit before its password is compared,
so the limit holds however many attempts arrive together (see "Attempts made at once"
below). The thresholds are an option of `PasswordSignIn`:

```go
password := session.PasswordSignIn(auth, session.AttemptLimit(5, 15*time.Minute, 15*time.Minute))
```

`AttemptLimit(failures, window, lock)` reads: `failures` failed attempts on a name within
`window` lock the name for `lock`. The default is the line above. The limit is at least
one failure within a positive window and lock; any other panics at registration, as a
misregistered method does.

**What counts, and against what.** Every password check the library makes is held to
the limit: the login (`PasswordMethod.Login()`), the API's `ValidateCredentials`, and
a pending identity's password confirmation (`Pending().ConfirmWithPassword()`,
`ConfirmPendingWithPassword`), under the name the attempt names, as the users table
normalizes usernames (`Pat@Lakeside.edu` and `pat@lakeside.edu` are one name). A wrong
password, an unknown name and a password-less user all count, and all answer the same
401 in the same words (`{"message": "The username or password is wrong. Check what you
entered and try again"}`) in the same time: a name with no user or no
password is compared against a dummy hash the method's hasher made, so timing no
longer tells an existing name from an unknown one. The password a signed-in person
proves is held to the same limit, under their own name: the old password of a change
(`ChangeSessionUserPassword`, the method's `ChangeUserPassword`) and the password a
removal asks for (`RemoveUserPassword`). A wrong one answers 400 as before and counts;
a name at the limit answers 429 with `too_many_attempts` and the wait before anything
is hashed, so a session of the person guesses their password no faster than the login
lets a stranger. A matching password resets the
name, as do an MFA completion (`CompletePending`) and a password change of any kind
(`ChangeSessionUserPassword`, the password chosen at a reset link).
Nothing locks a user an operator must unlock: the lock passes on its own, and a
password change lifts it. An operator who will not wait clears it: the name's row
deleted from the sign-in attempts table (the key is `SignInAttemptName(username)`), as
the generated user management's unlock method does in a transaction of the
application's, lifts the lock on every instance within five seconds, since no instance
trusts what it remembers of a name for longer.

The same table keeps the library's own counts: the mail an address is sent, the sign-in
codes an address is sent, the codes sent to accept one invitation, the reset links an
address is sent, and the guesses at the code of an account's first password, under
`MAIL:`, `CODE:`, `INVITATION:`, `RESET:` and `FIRST-PASSWORD:` and what each counts. A
name is counted
under its case-folded form, which holds no capital, so nothing a person types into the
username field is ever one of those keys: failing to sign in under the text of one
counts against a name nobody holds, and closes no address to its mail. A caller that
clears a row a list shows, whichever kind it is, clears the row of
`SignInAttemptKey(listed)`.

**Attempts made at once.** An attempt takes its place under the limit before its
password is compared: the store counts it against the name in one transaction and says
whether it was counted (`AuthStore.RecordSignInFailure`, `SignInAttempts.Counted`), and
an attempt that was not, because others made at once with it reached the limit first,
is refused with `too_many_attempts` and the wait without being compared. Of any number
of attempts on a name made together, no more than the limit are ever compared with a
password, on both databases; the one that reaches the limit is compared itself. A wrong
password leaves its place taken, which is its count. A right password gives the place
back and resets the name: the row is deleted, whatever lock the attempts in flight
beside it earned included, so a person's own sign-ins leave nothing held, and the place
is given back though the request was canceled once the password was compared. The
client's address and cookie are counted the same way, in memory, in the step that finds
them free, and a right password gives that count back too, so the sign-ins of people
who share an address leave nothing held against it. The limit so bounds the attempts on
a name that are compared at one time, right ones among them: when more sign-ins with
the right password than the limit arrive together for one name, the ones past the limit
are refused with `too_many_attempts` until one of the others finishes and resets the
name, which an instance that cached the lock learns within five seconds. An application
that checks one account's password many times at once (`ValidateCredentials` for an
account its services share) meets those refusals; a higher `AttemptLimit` avoids them
at the price of as many more guesses at every name.

**The three layers.** The name's count is durable: a row of the sign-in attempts table
(`SessionSignInAttempts`, keyed by the name, never a column on the user row), written
by each attempt that takes its place, deleted by a right password, and kept per
instance in a cache that trusts a lock for five seconds, so every instance refuses a
locked name and a lock cleared in the table is gone from every instance within that
time. Over it, each instance holds three kinds of key in
memory, in a bounded table: the name, the client's address and the login's XSRF cookie
token, the cookie the XSRF guard reads (a POST without it never reaches the handler).
During a name's lock every attempt on it is refused the moment it arrives, from memory,
with the same answer and the remaining wait: nothing is counted or hashed, and the
store is asked about the name at most once in five seconds per instance. An
early retry during a wait counts against the client's address and cookie, never against
the name, so a script that ignores the wait extends its own lock while the name's stays
as it was, and a person who waits is not locked out by someone hammering their name.
The client's keys are each allowed ten times the name limit's failures, across names,
within the window; past that the client is locked for the lock period and every retry
extends it. The address is the one a proxy reports (`True-Client-IP`, `X-Real-IP`, the
first `X-Forwarded-For` entry) or the connection's; it is a throttle, not a security
boundary, since the headers are the client's to forge, and a client cycling cookies is
held by the name and the address, one cycling addresses by the edge's own throttle. The
cookie carries no state and nothing new is written to it. In front of it all sits the
edge: a per-address throttle on the sign-in route, outside the library.

**The answer.** A locked name, and a held client, get `429 {"message", "code":
"too_many_attempts", "retryAfter": <seconds>}` with the standard `Retry-After` header,
and nothing about the name; `ValidateCredentials` and `ConfirmPendingWithPassword`
return a `*sessioninfo.LoginRefusal` carrying `RefusedTooManyAttempts`, whose wait
`sessioninfo.RetryAfterOf(err)` reads. A login page that reads `code` from a refusal's
body shows its sentence for `too_many_attempts` (and may show the wait); the external
methods are not limited, since the provider checks their passwords.

**The after-sign-in hook.** One function per `Auth`, set once like the sign-in policy,
is called once for every session a sign-in establishes, after the transaction that
inserted it has committed: a password or external sign-in, a password confirmation, an
MFA completion, a session the application started (`StartAuthenticatedSession`). It
receives the session, its user and the auth events the session recorded, and never
hears of a refused sign-in or one held for confirmation or MFA. An error from the hook
is logged; the session stands. A regenerated session (a password change) and an
impersonation are not sign-ins and are not reported.

```go
auth.SetSignedInHook(func(ctx context.Context, s *session.SignedIn) error {
    return audit.SignedIn(ctx, s.UserID, s.Username, s.SessionID, s.Reason, s.Events)
})
```

A nil hook, a second hook, or a hook set once the `Auth` is in use panics, as
`SetSignInPolicy` does: set it after `NewAuth` and before serving.

The generated management of an Auth's people answers this hook to remove a person's
role assignments whose period has ended (`<auth>identity.Client.SignedIn`, the resource
package's README, section 21): because the hook runs after the commit and its error is
logged, the removal can neither fail a sign-in nor undo one. An application with a hook
of its own calls the client's from it.

### Machine outlets and bearer tokens

A machine outlet is a URL space of the application whose requests carry a bearer token
the Auth issued, never a session cookie: the surface a command-line tool or an
integration calls. The Auth issues the tokens two ways, and one middleware binds each
request on the outlet to the token's person the way `ValidateSession` binds a browser
request to its user. The outlet names the application chooses, the pages that issue and
approve tokens and what each outlet serves are the application's; the library issues,
stores, validates and revokes.

**Registering an outlet.** `session.RegisterMachineOutlet(auth, name)` registers the
outlet and returns it; tokens are issued only for registered outlets, and a token is
accepted only on the outlet it was issued for. Register every outlet after `NewAuth` and
before serving, as sign-in methods are registered: a name that is empty or not an
identifier (letters, digits, hyphens, underscores), a name registered twice, or an
outlet registered once the Auth is in use panics. Route the outlet outside
`StartSession` and wire `outlet.ValidateToken` on it (see "Routes").

**What `ValidateToken` does.** It reads `Authorization: Bearer <token>`, finds the token
by its hash, requires it live (not expired, not revoked), of a bearer kind (an
application or access token, never a refresh token) and issued for this outlet, loads
its person as `ValidateSession` loads a session's user (missing or disabled: refused;
under the password change mark: 403 with `password_change_required`, as everywhere),
and binds the request: `sessioninfo.UserFromCtx`, `sessioninfo.DataFromCtx` (the
session data's `ID` is the token's id, `AuthenticatedAt` its issue, `AuthEvents` one
`token` event whose connection is the token's kind, `Token` the
`*sessioninfo.TokenInfo`), `sessioninfo.PrincipalFromCtx` through the Auth's principal
resolver as for a session, and `sessioninfo.TokenFromCtx` for the token itself. The
request's log entry, every line logged within it and its span carry `token.id`,
`token.kind` and `token.name` (an application token) or `token.client` (an access
token), beside `username` and `enduser.id`. A refusal is 401 with
`WWW-Authenticate: Bearer realm="<outlet>"`, and `error="invalid_token"` when a token
was presented (RFC 6750). No cookie is read or written and no XSRF token is checked: a
bearer token is nothing a browser sends on its own. The token's `LastUsedAt` moves at
most once a minute.

**Application tokens** are what a signed-in person issues for a program of their own.
`auth.ApplicationTokens()` has the page's handlers, behind `ValidateSession` and
`ValidateXSRFToken`: `Create()` takes `{"name", "outlet", "expiresIn"}` (`expiresIn`
in seconds, optional, at most the Auth's application token lifetime) and answers
`{"id", "token", "name", "outlet", "expiresAt"}` where `token` is the secret, shown
this once and stored only as its hash; `List()` answers the person's live tokens,
newest first, as `[{"id", "kind", "name", "outlet", "clientId", "createdAt",
"expiresAt", "lastUsedAt"}]` (the application tokens and the device grants' tokens,
never a secret); `Revoke()` takes `{"id"}` and revokes that token of theirs, a device
grant's with its family (404 when the token is not theirs or not live). The API has
`CreateApplicationToken`, `Tokens`, `RevokeToken` and `RevokeUserTokens` (every live
token of a person, for an operator taking their access away; a disabled person's
tokens are refused on their next request anyway, since the person is loaded on every
request, and a deleted person's are deleted with them).

**The device flow** (RFC 8628) is how a command-line client obtains tokens with a
person's approval and no password of its own. The client posts `client_id` and
`outlet` to `auth.Device().Authorize()` (form-encoded or JSON; public, outside
`StartSession`) and is told `{"device_code", "user_code", "verification_uri",
"verification_uri_complete", "expires_in", "interval"}`; it shows the person the user
code (`XXXX-XXXX`, letters only) and the verification URI, and polls
`auth.Device().Token()` with `grant_type=urn:ietf:params:oauth:grant-type:device_code`
and the `device_code` every `interval` seconds. The person, signed in to the
application as always, opens the verification page (`WithDeviceVerificationURL`,
default `/device`; the application's page, which reads `?userCode=`), which asks
`auth.Device().Lookup()` (`?userCode=`, behind `ValidateSession`) which client asks for
which outlet and posts `{"userCode"}` to `Approve()` or `Deny()` (behind
`ValidateSession` and `ValidateXSRFToken`); one decision stands. The next poll answers
`{"access_token", "token_type": "Bearer", "expires_in", "refresh_token"}`, once; until
then, and otherwise, it answers 400 with the RFC's codes: `authorization_pending`,
`slow_down` (polled before the interval passed), `access_denied`, `expired_token`,
`invalid_grant` (unknown, redeemed, rotated or revoked), `invalid_request`,
`unsupported_grant_type`. The two public endpoints speak the RFC's wire format
(snake_case, `Cache-Control: no-store`), so a standard client library drives them.

The access token is what the client presents on the outlet; when it expires the client
posts `grant_type=refresh_token` with the `refresh_token` to the token endpoint and
receives the next pair. A refresh rotates: the pair presented is revoked and the next
is issued in the same family, for the same person, outlet and client. A refresh token
presented again after its exchange is a reuse (a stolen copy, or a client that lost
the answer): the whole family is revoked, so the holder of the current pair loses it
too, and the answer is `invalid_grant`; the client signs in again. The API has
`StartDeviceAuthorization`, `RedeemDeviceCode`, `RefreshTokens`, `PendingDevice`,
`ApproveDevice` and `DenyDevice`.

**Lifetimes and storage.** `WithTokenLifetimes(access, refresh, applicationMax)`
(default an hour, thirty days, a year), `WithDeviceCodeLifetime(lifetime,
pollInterval)` (default ten minutes, five seconds) and `WithDeviceVerificationURL(path)`
(default `/device`, a path in the application). Tokens and device authorizations live
in `SessionTokens` and `SessionDeviceCodes` (the shipped migrations `000007` on Spanner
and `000006` on PostgreSQL; `WithTokensTableName`, `WithDeviceCodesTableName`), keyed
by the hash of the secret, with the person, the outlet, the kind, the family, the
name or client, the issue, expiry, last use and revocation (`revoked`, `rotated`,
`reuse`, `user`); a person's rows go with the person.

#### From a command line

A command-line program signs in to an outlet with the `session/deviceflow` package and
calls the outlet as the person who approved it: `Login` runs the device flow once,
`Client` calls the outlet, `Logout` forgets the sign-in.

```go
cfg := &deviceflow.Config{
    Server: "https://app.example.com", // the application, as a browser reaches it
    Outlet: "droids",                  // the machine outlet
    Prefix: "/api",                    // where its public endpoints sit: /api/oauth/device/authorize, /api/oauth/token (default "/" + Outlet)
    Client: "corral",                  // the program's name, shown to the person who approves it
}
if err := deviceflow.Login(ctx, cfg); err != nil { // "Open https://app.example.com/device?userCode=BCDF-GHJK and approve the code BCDF-GHJK."
    …                                              // errors.Is: deviceflow.ErrDenied, deviceflow.ErrExpired; a *deviceflow.Error otherwise
}
client, err := deviceflow.Client(cfg) // an *http.Client: Authorization: Bearer … on every request
resp, err := client.Do(req)           // errors.Is(err, deviceflow.ErrSignedOut): nothing stored, or the refresh refused; Login again
_ = deviceflow.Logout(cfg)
```

`Login` asks the application for a device authorization, tells the person which
address to open and which code to approve (`Config.Prompt`; by default two sentences
on standard error, the address with the code filled in and when the code expires),
polls the token endpoint at the interval the application names, five seconds longer
after each `slow_down`, until the person decides, and stores the pair: `ErrDenied` when
they deny it, `ErrExpired` when the code expires first, a `*deviceflow.Error` with the
code and description for any other refusal. The pair is kept in `tokens.json` under
`Config.Dir`, by default the program's own directory in the person's configuration
directory (`os.UserConfigDir()` joined with `Client`: `~/.config/corral` on Linux), the
directory `0700` and the file `0600`, keyed by server and outlet, so one program keeps
its sign-ins to several applications in one place. `Client` returns an `http.Client`
that puts the stored access token on every request, refreshes the pair first when the
token expires within a minute, and, when the outlet refuses the token with
`invalid_token`, refreshes and sends the request once more if its body can be sent
again (none, or `GetBody` set). Every refresh holds `tokens.lock` beside the tokens
file, and a process that finds the pair already rotated by another uses what it finds:
a refresh token is presented once, never twice, since a second presentation revokes the
grant. A request with no stored pair, or whose refresh the application refuses, fails
with `ErrSignedOut`; the program calls `Login` again. `Logout` forgets the stored pair
and calls nothing on the application: the person revokes the grant on their token page
when they want it gone there too.

## Sending mail

The library sends mail for the flows that need a link or a code in someone's mailbox:
the invitation a tenant's administrator sends to an address, the link that verifies an
address a person adds or changes, the link a password sign-up starts from, the link
that resets a password, the link that confirms an address an administrator set, the
sign-in code of the second factor, and the code that accepts an invitation, which comes
in a message of its own. It also sends notices, which carry no link and are
not counted against the limit on mail to an address: that an account's address changed
(to the old address), that an administrator reset a person's second factor, and that an
administrator removed a person from a tenant, which says so when the account went with
it. The removal notice is for an administrator's act that leaves the person with no role
in the tenant, of their own or through a group: a role taken, the person taken out of a
group, or a role taken from a group; where that was the last thing the person held
anywhere and the account went with it, the notice says so. It is not sent when an
administrator deletes the account outright or a tenant is cleared, for a role that ends
by itself, or when the person leaves themselves. A link, a code or a notice goes to a confirmed address or, where it is what
confirms one, to the address being confirmed. It writes
to one interface, `mail.Sender` (`session/mail`), and knows nothing of the mail system
behind it; an application configures a driver and hands it to the `Auth`. A message is
plain text with one link in it (`mail.Message`: the recipient, the subject and the body),
and every message is from the one address the driver is configured to send as. A message
reads as instructions: what the person did, the link to open and how long it is good for
(`LinkTimeout` or `InvitationTimeout`, said as a duration such as "6 hours", since the
person knows when the message reached them), then, under headings of their own, what to
do if it was not them and a word to keep the link to themselves. Tests use
`mail.Recorder`, a sender that keeps what it was asked to send.

The first driver is the Google Workspace SMTP relay, `session/mail/workspace`. Google
Cloud has no outbound mail service of its own and blocks port 25 from every compute
product, so a mail server of our own is not an option there; the relay is Google's own
route. The driver submits each message to `smtp-relay.gmail.com` on port 587 over TLS,
which Cloud Run allows, logged in as one mailbox of the organization's Workspace domain
with an app password, and Google carries delivery, reputation and signing from there.
The mailbox is the From address of every message, a no-reply address as a rule; where
the no-reply address is an alias of a real account rather than a mailbox of its own, the
account logs in and `From` names the alias, since Google issues no login to an alias
while the relay accepts any From address in the organization's domains once the login is
a real account.

```go
sender, err := workspace.New(&workspace.Settings{
	Mailbox:     "ops@example.com",        // the Workspace account the relay is logged in to
	From:        "no-reply@example.com",   // the address every message is from; empty is the mailbox
	AppPassword: settings.MailAppPassword, // the account's app password, from the secret store
	Name:        "Harbor",                 // the display name beside the address
})
```

What the Workspace side needs, once per organization:

1. **The mailbox**, a Workspace user such as `no-reply@example.com`, or a real account
   with `no-reply@example.com` as one of its alias addresses, with two-step verification
   turned on and an app password generated for it (the account's security settings,
   under two-step verification). Google issues app passwords only to accounts
   with two-step verification; the password stands in for the account's at the relay's
   login and carries no second step, which is what a service needs. Two-step verification
   itself protects interactive sign-ins to the mailbox and the driver never meets it.
2. **The relay service**, in the admin console under Gmail routing: the SMTP relay
   service accepting mail from addresses in the organization's domains, with SMTP
   authentication required and TLS required.
3. **The secret**: the app password goes into the deployment's secret store as a pinned
   version (bedrock refuses a release where the pin is missing). Rotating it is
   generating a new app password and pinning the new version; nothing in the application
   changes.

Every `Auth` sends mail, so `NewAuth` takes how as an argument (`MailSettings`): the
sender, the application's name and the absolute URL of its login page, where every
mailed link leads
(`#invitation=<token>`, `#verify=<token>`, `#signUp=<token>`, `#reset=<token>`,
`#address=<token>`). The token is in the URL's fragment, which a browser sends to no
server: it is in no request line, so no request log, proxy or `Referer` header holds it.
The login page reads it there and nowhere else, takes it out of the address at once, and
posts it in the body of the request that uses it; a parameter of a link's name in the
address's query is not a link, and the page sends its value nowhere. The browser's own
history of visited pages keeps the address the mail opened, token and all, which no page
can clear: a link opened and not used can be opened again from that browser until it is
used or expires. The login URL itself carries no fragment (NewAuth refuses one).

```go
auth, err := session.NewAuth[session.NoCustomData, session.NoCustomData](store, cookieKey, superAdministrators,
	session.MailSettings{Sender: sender, Application: "Harbor", LoginURL: "https://harbor.example.com/login"})
```

In development `mail.Log{}` stands in for a driver: it writes each message to the log,
link included, and sends nothing, so the link is followed from the log. It is never
configured for a deployed environment, where the links are secrets.

The relay allows a sending address ten thousand messages a day, counted apart from the
mailbox's own sending. A refused login comes back in the relay's words after the driver's
sentence saying what the login takes; a refused recipient and a refused message likewise.
A send is given thirty seconds (`Settings.Timeout`), dialing included. An organization
that forbids app passwords has the relay's IP allowlist (a fixed outbound address per
environment) or the Gmail API with domain-wide delegation as its alternatives; neither is
built.

## OIDC role synchronization

Role synchronization reconciles a user's application roles to the identity provider's
authorization signal on every OIDC login: role names sourced from the IdP are assigned
(where a role of that name exists), roles the user holds that are NOT among them are
removed, and the login is rejected unless at least one recognized role results. It is
designed for organizations that manage roles centrally in the directory. The role
names come from the provider's native mechanism — Azure delivers them in the token's
`roles` claim (App Role ↔ group assignments); Google has no such claim, so the Google
sign-in derives them from Google Groups membership at login (see the "Google Workspace
OIDC" section). See the `azure.SignIn` and `google.SignIn` godoc for the full semantics.

A membership is written where the role is held. A global role is held in the global
partition, and a domain role is held in every tenant domain, so one membership reaches
every tenant, the ones that exist today and the ones created later; the sync keeps no
list of tenants. A membership the directory does not name is removed wherever it is
held, a membership in one tenant domain included, so an application that writes its own
memberships (a tenant-specific role, say) runs with role synchronization disabled.

On an `Auth` session the slot is in the method's configuration, required for the two
methods that synchronize: `azure.Config.RoleSync` takes `signin.RoleSync(manager)` or
`signin.DisableRoleSync()`, and `google.Config.RoleSync` takes
`google.RoleSync(manager, groupPrefix, lookup)` or `google.DisableRoleSync()`.
`session.RoleSync` and `session.DisableRoleSync` are the same functions under the root
package's name. A registration without its slot is refused (see "Registering sign-in
methods").

A registration that synchronizes roles stands alone on its `Auth`: no password method
and no other registration beside it, in either order of registration, or the `Auth`
refuses the registration. The directory is then the authority for everyone the `Auth`
holds, and the first sign-in through it seats the person in the roles the directory
names, with nothing to seed. An application whose people come in two ways with two
authorities (staff on the directory's groups, members the application seats) keeps two
`Auth`s, one per population.

The directory decides who is a super administrator too. A name it asserts that
resolves to the Super Administrator role is written like any other, in the global
partition, the one place the role is held and the one place it resolves; a person the
directory names for that role alone holds a role and signs in; and the role goes at the
sign-in where the directory no longer names it, the last holder's included. So such an
`Auth` has the super administrators its directory names and no other, and none is
seeded. The library's own second factor for a super administrator
(`signin.SuperAdministratorFactor`) does not hold a sign-in through a method that
synchronizes roles: how the directory's people prove who they are is the directory's
to say, and the application's sign-in policy still asks what it asks of anyone.

```go
entra := azure.SignIn(auth, "azure", nil, &azure.Config{
    Tenants: azure.Tenant(tenantID), ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL,
    RoleSync: signin.RoleSync(userRoleManager), // or signin.DisableRoleSync(); one tenant is the shape it runs with
})
```

`session.UserRoleManager` is the role store surface the sync needs, and the access
package's `UserManager` satisfies it. Each method names where a membership is held with
an `accesstypes.PolicyScope`: `accesstypes.GlobalPolicyScope()` is the global partition
and `accesstypes.EveryDomainPolicyScope()` is every tenant domain, the two places the
sync writes. `UserRoles` called with no scopes lists every membership the user holds,
keyed by where each is held, which is how the sync finds a stale membership in one
tenant domain. `ResolveRole` finds the role a place holds under a name, matched without
regard to case, and answers it in the spelling the store holds: a Google group is an
email address and the groups lookup lowercases what it reads, so the directory names
`pilot` where the role file spelled `Pilot`, and the sync compares, adds and removes in
the store's spelling. A store that holds two roles differing only in case cannot answer
such a name, and the access library refuses to create the pair. `ResolveRole` errors must
be returned, never flattened to "role missing": the sync removes what the directory does
not name, so a swallowed store error would delete a valid membership on a transient
fault.

## Generic OpenID Connect

A provider with nothing vendor-specific about it, an identity gateway, Okta or
Keycloak, is a method of the `Auth` session too, registered with
`oidc.SignIn(auth, name, rule, &oidc.Config{...})` from the `session/oidc` package. The
Entra and Google methods exist because their tokens carry a tenant or a Workspace the
method must check against the registration's slot; this one checks the issuer alone,
which discovery reads the provider's configuration from and every ID token must carry.
The round trip is the same authorization code flow with PKCE the other methods share.

```go
gateway := oidc.SignIn(auth, "gateway", nil, &oidc.Config{
    Issuer:       "https://id.example.com", // discovery at Issuer/.well-known/openid-configuration; every token carries it
    ClientID:     "lanterns",
    ClientSecret: "",                       // a public client: PKCE alone, the client ID in the request body
    RedirectURL:  "https://lanterns.example.com/api/user/gateway/callback",
    Connection:   "gateway",                // the connection every link carries; the issuer URL when empty
    RoleSync:     signin.DisableRoleSync(), // or signin.RoleSync(manager), reconciled to the token's roles claim
})
r.Get("/api/user/gateway/login", gateway.Login())
r.Get("/api/user/gateway/callback", gateway.Callback())
```

The identity is keyed by the registration's `Connection` and the token's `sub`. Its
sign-in name is the `preferred_username` claim, or the `email` claim when the provider
sends none, so the standard account rule provisions and matches users under it. The
provider is the one organization the registration is limited to: until an administrator
opens the registration to sign-up it is invite only, and opening it admits anyone the
provider authenticates (see "Open to sign-up, or invite only"). The login route forwards
`?login_hint=` to the provider as its `login_hint`, which a gateway reads as the
account to sign in, and `?prompt=select_account` as its `prompt`. A provider that
enforces a second factor at every sign-in is declared so with `Attesting: true`, and a
sign-in through it carries the attestation by declaration; a token whose `amr` claim
names `mfa` attests by evidence whatever the declaration (see "The second factor").
There is no provider-initiated logout: `Logout` destroys the local session only. Under
the `skipAuth` build tag the method is simulated like the others: the callback signs in
`APP_<NAME>_USERNAME` with the roles `APP_<NAME>_ROLES` names, `APP_USERNAME` and
`APP_ROLES` when those are unset, under a `sub` derived from the username.

## Microsoft Entra ID OIDC

Microsoft Entra ID sign-in is a method of the `Auth` session, registered with
`azure.SignIn(auth, name, rule, &azure.Config{...})` from the `session/azure` package
(see "Registering sign-in methods" above). The identity is linked to its user by the
tenant and the object id, the `tid` and `oid` claims, under every shape below. Its
sign-in name is `preferred_username`, the user principal name, so the standard account
rule provisions and matches users under it. A guest of the tenant (a
B2B guest, whose `idp` claim names another provider than the issuer or whose name
carries `#EXT#`) is marked on the identity (`Identity.Guest`) and refused by the
standard rule (`guest_account`); a rule of the application's own decides for itself.

`Login` accepts `?returnUrl=` (a path in the application) and `?prompt=select_account`,
passed to Microsoft as its `prompt` so it shows its account chooser even when it holds
one signed-in account it would otherwise take without asking; the login page sends it
after a refusal and under "Use a different account". Any other `prompt` value is
refused with 400.

### Which accounts may sign in

`Config.Tenants` is required and says which Microsoft Entra tenants' accounts a
registration admits. The verified ID token's `tid` claim selects the entry at the
callback, before the account rules run, so an account the slot does not admit never
reaches a row (`unlisted_tenant`, or `personal_account` for a Microsoft personal
account under a work or school shape). The entry also decides the organization the
standard rule stores as the tenant key of the links it makes
(`Identity.Organization`); the link's connection is the tenant id under every shape.

- **One tenant**, `azure.Tenant("<tenant id>")`: the application that belongs to one
  organization, as a single-tenant app registration serves it. The registration reads
  its provider configuration at the tenant's own endpoint, whose document names the
  tenant's issuer, so every token is held to it. `azure.NamedTenant(id, "lakeside")`
  gives the tenant the application's own name, stored as the tenant key in place of
  the id. This is the only shape role synchronization runs with, since a token's
  application roles are asserted by the tenant that issued it.
- **Listed tenants**, `azure.Tenants(azure.Tenant(...), azure.NamedTenant(...))`: the
  `tid` claim selects the entry, and a tenant outside the list is refused. A tenant id
  is listed once and a name is used once.
- **Any tenant**, `azure.AnyTenant()`: the work or school account of any Microsoft
  Entra tenant, and no personal account. The tenant key is the tenant id; name the
  tenants you know beside it (`azure.Tenants(azure.NamedTenant(...), azure.AnyTenant())`)
  and theirs is the name.
- **Personal accounts**, `azure.PersonalAccounts()`: Microsoft personal accounts
  (outlook.com, hotmail.com, Xbox), which Microsoft issues from one consumer tenant
  (`azure.ConsumerTenantID`), alone or beside any tenant or listed tenants.
- **A tenant that attests**, `azure.Tenant(id).Attesting()`: the application
  administers the tenant and declares that it enforces a second factor at every
  sign-in, so a sign-in through it carries the attestation by declaration and is never
  held for the library's code; a token whose `amr` names `mfa` attests by evidence
  under every shape. The declaration is for listed tenants; see "The second factor".

Several tenants, any tenant and personal accounts are served through Microsoft's shared
endpoints (`organizations`, `consumers` or `common`, as the shape calls for), whose
discovery documents name their issuer with a placeholder; the app registration must
then admit accounts in any organizational directory, and personal accounts when the
slot does. A shared document cannot hold a token to an issuer, so this library checks
the token's issuer against the token's tenant itself, `<Authority>/<tid>/v2.0`, and
the tenant against the slot, in the verifier and again in the method, before any user
is resolved; the signature, audience and expiry are checked as always.

The entry is also how wide the registration is, which is whom an administrator admits
by opening it to sign-up (see "Open to sign-up, or invite only"): the people of the
tenants it lists, each by the name `azure.NamedTenant` gives it; anyone with a
Microsoft work or school account under any tenant; anyone with a personal Microsoft
account under personal accounts. Every registration is invite only until it is opened.

The name `azure.NamedTenant` gives a tenant is also what a person reads wherever the
application speaks of the tenant's directory: a link's connection is the tenant id,
which nobody knows, so `AuthAPI.DirectoryName` answers the name for it and a sentence
says "Lakeside". A tenant listed by its id alone, and every tenant admitted under
`azure.AnyTenant()`, has no name: a sentence then says the directory by the label of
the registration's button on the login page, "your organization's Microsoft directory"
for a button labeled Microsoft. Name the tenants you list.

`Config.Authority` is the Microsoft identity platform the tenants live on,
`https://login.microsoftonline.com` when empty; Azure Government tenants live at
`https://login.microsoftonline.us`. There is no issuer setting: a tenant's issuer is
`<Authority>/<tenant id>/v2.0`.

### Provider-initiated logout

Entra calls the application's front-channel logout URL (`FrontChannelLogout`) with
the `sid` when the person signs out of Microsoft. The handler ends every session whose
sign-in carried that `sid` under an issuer the registration declares, impersonations
started from them included. A registration serving one tenant scopes the request to
that tenant's issuer; one serving several, to whichever of its issuers live sessions
carry the `sid` under. A request that names an `iss` is scoped to it, and refused when
no tenant the registration admits has it. See "Every session" for the design.

## Google Workspace OIDC

Google Workspace sign-in is a method of the `Auth` session, registered with
`google.SignIn(auth, name, rule, &google.Config{...})` from the `session/google`
package (see "Registering sign-in methods" above). It is the counterpart of the Azure method: the
same session machinery, custom data axes and role-reconciliation semantics, built on
Google's identity model instead of Entra's. The mental-model difference that drives
every API difference: **an Entra app registration is an authorization surface** (App
Roles are declared on the app, assigned to groups, and delivered in the token's `roles`
claim), while **a Google OAuth client is authentication only** — the ID token says who
the user is (`email`, `sub`) and which Workspace org they belong to (`hd`), and nothing
about what they may do. Authorization signals therefore come from the directory itself
(Google Groups), fetched at login.

There is no issuer or authority setting — Google operates a single issuer for all orgs;
tenancy is the `hd` claim, not the issuer. The identity is linked to its user by its
connection and the `sub` claim: `sub` is globally unique among all Google identities
and never reused, and the connection is what the registration's `Workspaces` slot
selects for the account (below). The sign-in events carry it too, so a policy tells
Workspaces apart by connection and, within one, domains apart by the identity's
`HostedDomain`.

The identity's sign-in name is the verified `email` claim (`email_verified` is
enforced), so the standard account rule provisions and matches users
under it. The claim confirms the address itself (`Identity.EmailVerified`) only where
Google is the address's authority: a Gmail address, or a Workspace account's (a token
with `hd`). A registration's own rule reads the same answer on the identity it is given
(`google.Identity.EmailVouched`), and chooses no user by an address Google does not
vouch for. A Google account made with an address from anywhere else signs in under
that name all the same, and Google's word for the name counts no more than another
provider's: its first sign-in to an account created ahead under that name waits for
the code mailed to the address the account holds (see "A first link on a name alone"),
and its first sign-in to any account with no confirmed address waits for the mailed
code. Google has no `end_session_endpoint` and issues no `sid` claim, so there is no
Google-initiated logout: `Logout` destroys the local session only. The `RoleSync` slot
is `google.RoleSync(manager, groupPrefix, lookup)` or `google.DisableRoleSync()` (see
"The group naming convention").

### Which accounts may sign in

`Config.Workspaces` is required and says which Google accounts a registration admits.
The verified ID token's `hd` claim, the domain of the account's Workspace, selects the
entry at the callback, before the account rules run, so an account the slot does not
admit never reaches a row (`not_workspace_member`). The entry also decides the
connection written on the identity link:

- **One Workspace**, `google.Workspace("lakeside", "lakeside.edu", "lakeside.org")`:
  the application that belongs to one organization. The name is the application's own
  name for the Workspace, written as the connection of every link made through it,
  since Google puts no stable organization id in the token and one Workspace answers
  for every domain it owns; the domains are checked against the `hd` claim. This is
  the only shape role synchronization runs with, since the group lookup reads one
  Workspace's directory.
- **Listed Workspaces**, `google.Workspaces(google.Workspace(...), google.Workspace(...))`:
  the `hd` claim selects the Workspace whose domains contain it, and that Workspace's
  name is the connection. A domain belongs to one Workspace, and a name is used once.
- **Any Workspace**, `google.AnyWorkspace()`: any `hd` claim is admitted and none is
  refused. The connection is the account's domain, lowercased, since the registration
  names no Workspace; name the Workspaces you know beside it
  (`google.Workspaces(google.Workspace(...), google.AnyWorkspace())`) and their members
  share one connection across their domains.
- **Any Google account**, `google.AnyGoogleAccount()`: a token with or without an `hd`
  claim. An account outside the named Workspaces carries the connection
  `google.AnyAccountConnection`. The OAuth client's consent screen must be *External*
  and published for accounts outside your own Workspace to reach the callback;
  *Internal* restricts it to your Workspace.
- **A Workspace that attests**, `google.Workspace("example", "example.com").Attesting()`:
  the application administers the Workspace and declares that it enforces a second
  factor at every sign-in (2-Step Verification enforcement), so a sign-in through it
  carries the attestation by declaration and is never held for the library's code.
  Google puts no evidence of a second factor in its tokens, so the declaration is the
  only way a Google sign-in attests; it is for named Workspaces, and the one to name
  beside an open entry. See "The second factor".

The entry is also how wide the registration is, which is whom an administrator admits
by opening it to sign-up (see "Open to sign-up, or invite only"): anyone with an
account at the domains of the Workspaces it names; anyone with a Google Workspace
account under any Workspace; anyone with a Google account under any Google account.
Every registration is invite only until it is opened.

**The connection is a key.** Renaming a Workspace, or moving from any Workspace (where
the domain is the connection) to a named one, makes every sign-in from it arrive as an
unknown identity until the links are re-keyed:

```sql
UPDATE SessionIdentities SET "Connection" = 'new' WHERE "Method" = 'google' AND "Connection" = 'old'
```

Two layers restrict an application to your own organization: the OAuth client's
*Internal* consent screen (Google refuses users outside the org with an `org_internal`
error before they reach the callback) and the one-Workspace slot, the trusted backstop
even with the consent screen in place.

### Skipping Google's account chooser

With one Workspace on one domain the authorization request hints that domain, and
Google skips its account chooser when exactly one signed-in account is in it; with
several domains, several Workspaces or any Workspace it hints any Workspace account,
and the chooser appears whenever more than one Workspace account is signed in; with any
Google account it hints nothing. `Login` accepts `?login_hint=<email>` beside
`returnUrl` and passes it to Google as `login_hint`: Google picks that account when it
is signed in and pre-fills the sign-in form otherwise, so an identifier-first login
page (the person types their email, the page picks the registration) skips the chooser
whatever the registration's shape. It is a hint and is never read back; the
identity comes from the verified token. A hint longer than 254 characters, or carrying
spaces or control characters, is refused with 400.

The opposite is wanted after a refusal: Google holding one signed-in account of the
hinted domain signs it in again without asking, so a person refused with the wrong
account (`not_invited`, say) would be sent round the same loop on every press of the
button. `Login` accepts `?prompt=select_account` and passes it to Google as its
`prompt`, which makes Google show its account chooser whatever it holds; the login
page sends it after a refusal and under "Use a different account", and nothing on an
ordinary visit. Any other `prompt` value is refused with 400. A person who cancels at
the chooser comes back to the callback with `error=access_denied`, which is refused
`token_exchange_failed` before any exchange (see "The redirect and response
contract").

### The group naming convention

`google.RoleSync` derives candidate role names from group emails: a group whose local
part is the configured prefix followed by a role name maps to that role, everything
else is ignored. With prefix `app-myapp-`:

| Group email                       | Candidate role |
| --------------------------------- | -------------- |
| `app-myapp-admin@example.com`     | `admin`        |
| `app-myapp-viewer@example.com`    | `viewer`       |
| `team-eng@example.com`            | — (no prefix)  |
| `app-otherapp-admin@example.com`  | — (other app)  |

The candidates then flow through the same reconcile logic as Azure's token roles: names
for which a role exists are assigned, held roles not among them are removed, and the
login is rejected unless at least one recognized role results. Group emails are
lowercase, so the candidate names are lowercase; the store resolves each without regard
to case (`ResolveRole`) and the sync writes the role in the store's spelling, so the
application's roles keep whatever spelling they have.

Membership is read at login through Google's Cloud Identity Groups API, with the
signing-in person's own access token. An access token is the credential that lets the
application call a Google API as that person. The library gets it by asking for one
extra OAuth scope, in addition to `openid`, `email` and `profile`:
`https://www.googleapis.com/auth/cloud-identity.groups.readonly`. A scope names one
permission the application asks the person for. The token is used for the lookup once
and never stored. No administrator credential is involved: no service account (a
Google user for software rather than a person), no domain-wide delegation (a
setting that lets such a user act as anyone in the organization), and no admin
role.

The last argument of `google.RoleSync` is a `google.GroupLookup`, which sets how far
the lookup reaches. It has two values:

- `google.DirectGroups()` (the default): one call. Only the groups the person is
  directly assigned to count, however the directory is nested (role groups should then
  hold people, not other groups — matching Entra's direct-only App Role resolution).
- `google.NestedGroups()`: the groups those groups are in count too, level by level,
  up to 10 levels. Each level is one round of calls to Google, so a deeply nested
  directory makes login slower. `DirectGroups` restores the one call.

`google.ParseGroupLookup(value)` reads the setting from configuration: `direct` or
`nested`, in any letter case, with surrounding space ignored. An empty value means
`DirectGroups`. Any other value is an error, so a misspelled setting stops the
application when it constructs its auth.

Google returns only the groups whose member list the person may view. A group whose
member list the person may not view is hidden: Google leaves it out, so that one
membership does not count. A hidden group never fails the lookup; the login goes ahead
with the groups Google returned. Two rules follow for your organization's groups:

- A group grants a role only if its *who can view members* setting includes the
  group's members.
- With `NestedGroups`, a hidden group hides everything above it, because the lookup
  cannot pass through it to the groups it is in.

A groups-lookup failure fails the login — the same posture as a role-store error.

## The simulated sign-in (`skipAuth`)

Built with the `skipAuth` build tag, the Azure and Google methods contact no provider:
the login route sends the browser straight to the callback, and the callback signs in
the person the environment names, with the roles it names. `APP_USERNAME` is the
person (the Azure `preferred_username`, the Google verified `email`) and `APP_ROLES`
the roles, comma-separated (the Azure `roles` claim, the Google groups the simulated
lookup answers). A registration reads its own pair first, `APP_<NAME>_USERNAME` and
`APP_<NAME>_ROLES`, with the registration's name upper-cased and every character
outside letters and digits written as an underscore (`lakeside-entra` reads
`APP_LAKESIDE_ENTRA_USERNAME`), and the shared pair when its own is unset. An
application with one registration sets the shared pair alone; one with several in one
process (a console's `google` beside a portal's `directory`) gives each its own person.
The tag is for development: a `skipAuth` binary admits whoever the environment names.

## Login refusal codes

A refused redirect-based sign-in (an `Auth` session's Azure, Google or WorkOS method)
returns the browser to the login page with the reason as a code,
never as text: `<LoginURL>?code=<code>`; the password login and the pending handlers
answer the same codes in JSON (see "The redirect and response contract"). The page maps
the code to a sentence it holds itself and shows nothing for a code it does not know, so
nothing that arrives in the URL is ever displayed. `LoginURL` is the method's
`Config.LoginURL`; its `Login()` and `Callback()`
redirect there, and `code` is the only query key a refusal writes.

The codes are typed constants in `sessioninfo` (`sessioninfo.LoginRefusalCode`), and the
table is the wire contract:

| Code | Constant | Produced when |
| --- | --- | --- |
| `internal_error` | `RefusedInternalError` | A fault the module does not classify: the authorization URL could not be built, the claims payload would not decode, the role store or session store failed, or a resolver error is neither a `LoginRefusal` nor a client message. |
| `login_refused` | `RefusedByApplication` | The application's custom session data resolver refused the login with an httpio client message and no code. The message goes to the log. |
| `no_oidc_cookie` | `RefusedNoOIDCCookie` | The callback arrived without the cookie the login route set. |
| `invalid_state` | `RefusedInvalidState` | The callback's `state` does not match the login this browser started. |
| `invalid_pkce` | `RefusedInvalidPKCE` | The login cookie carries no usable PKCE verifier. |
| `token_exchange_failed` | `RefusedTokenExchange` | The provider refused to exchange the authorization code for tokens. |
| `no_id_token` | `RefusedNoIDToken` | The token response carried no `id_token`. |
| `verify_id_token_failed` | `RefusedIDTokenVerification` | The ID token failed signature, issuer, audience, or expiry verification; under an Entra shape served through a shared endpoint, a token whose issuer is not its tenant's or that names no tenant. |
| `parse_claims_failed` | `RefusedClaimsParse` | The verified ID token's claims would not decode. |
| `not_workspace_member` | `RefusedNotWorkspaceMember` | Google: the user is outside the Workspace domain logins are restricted to. |
| `unlisted_tenant` | `RefusedUnlistedTenant` | Entra: the token's tenant is outside the registration's `Tenants` slot. |
| `personal_account` | `RefusedPersonalAccount` | Entra: a Microsoft personal account under a registration that admits work or school accounts only. |
| `email_not_verified` | `RefusedEmailNotVerified` | Google: the user's email address is not verified. |
| `organization_mismatch` | `RefusedOrganizationMismatch` | WorkOS: the profile belongs to another organization than the login requested. |
| `no_roles` | `RefusedNoRoles` | The directory asserted no role the application recognizes: refused before anything is resolved or written, or by role synchronization once the session stood, for a method that cannot tell beforehand. |
| `no_email_claim` | `RefusedNoEmailClaim` | Google: the ID token carries no `email` claim. |
| `identity_rejected` | `RefusedIdentityRejected` | Auth: the account rule rejected the identity (its own code wins when it sets one), linked a user that has a password without confirmation, asked for a password confirmation on an Auth that registers no password method, a password-less user who signs in through another connection was matched or linked without trust, a password-less user who already signs in through an identity was matched by the sign-in name with no verified address to prove it, a password-less user who has never signed in and holds no email address was matched by a sign-in name the method does not vouch for, provisioned a new user for an identity whose verified address another user holds verified, a sign-in whose method vouches for its address reached a user who holds no verified address while another user holds that address verified, or the identity resolved to another user than it waited on. |
| `not_invited` | `RefusedNotInvited` | Auth: the sign-in method is invite only, and the person holds no account and carries no invitation. |
| `invitation_only` | `RefusedInvitationOnly` | Auth: a stranger from a directory a tenant declares with no default seat (`SetDirectoryAdmission`), carrying no invitation; nothing is written. |
| `invitation_expired` | `RefusedInvitationExpired` | Auth: through a sign-in method that is invite only, the invitation the sign-in carried, which alone admitted the person, had expired or been used by the time the sign-in accepted it; nothing is provisioned. It is also the answer to a sign-in code mailed for an invitation, and to the sign-in that entered it, once that invitation was used, withdrawn or ran out: the code accepts nothing. |
| `invitation_spent` | `RefusedInvitationSpent` | Auth: a sign-in code asked for, or entered, to accept an invitation that takes no more codes: twenty-five were entered for it and none accepted it. The count is the invitation's, across every sign-in that carried it, and never starts again, so nothing more is mailed for it and no code is compared. The person asks whoever invited them for a new invitation (see "The second factor"). |
| `invitation_not_open` | `RefusedInvitationNotOpen` | Auth: a sign-in code mailed for an invitation, entered in a browser that carries no invitation or another one. The code is not compared, nothing is written, and it takes its place in the invitation's count. The person opens the invitation's link again and enters the code. |
| `invitation_address` | `RefusedInvitationAddress` | Auth: a sign-in or an acceptance by hand carried an invitation mailed to another address than the one it answers for (the one the method verified, or the account's own); refused before anything is read or written, the invitation left standing. A sign-in through an identity that asserts no address, or one its method did not verify, which accepts by a mailed code, is refused with it once the accounts are read and before anything is written: the account it is linked to, or would be linked to, is not the one that holds the invited address. An account that names itself and has not confirmed the invited address is not refused at a sign-in, which waits for the code mailed to the invited address; its acceptance by hand, where nothing can wait, is refused with this code and a sentence that says to sign in again from the invitation's link. The login page names the invited address and asks for a sign-in with that account. |
| `organization_account_required` | `RefusedOrganizationAccountRequired` | Auth: a sign-in, a sign-up with a password or an acceptance by hand carried an invitation that makes a tenant entered through its directory only, and came through no organization's directory (a password, a personal account, a session established any other way); refused before anything is read or written, no account made and the invitation left standing. The login page asks for a sign-in with the organization's account. |
| `guest_account` | `RefusedGuestAccount` | Auth: the identity is a guest of its directory (an Entra B2B guest), which the standard account rule refuses. |
| `policy_denied` | `RefusedByPolicy` | Auth: the sign-in policy denied the sign-in (its own code wins when it sets one). |
| `user_disabled` | `RefusedUserDisabled` | Auth: the sign-in resolved to a disabled user. |
| `pending_expired` | `RefusedPendingExpired` | Auth: a confirmation or MFA step arrived after its pending identity expired or was used. |
| `no_email` | `RefusedNoEmail` | The external identity carries no email address where one is required: an application's rule asks for one, or the standard account rule found no sign-in name to use as the username. |
| `password_change_required` | `RefusedPasswordChangeRequired` | Auth: an administrator required a new password of the signed-in user; answered with 403 by `ValidateSession` on every request but the password change, the session read and the logout, until the change (see "Users and the forced password change"). |
| `no_tenant` | `RefusedNoTenant` | Auth, the tenant policy: the person is a member of no tenant, and the policy was not built with `signin.AdmitWithoutTenant()`. |
| `connection_not_in_tenant` | `RefusedConnectionNotInTenant` | Auth, the tenant policy: the sign-in came through a directory (its method and connection), every tenant that seats the person is directory only, and none of them has that directory as its own. |
| `directory_only` | `RefusedDirectoryOnly` | Auth, the tenant policy: a sign-in with a password, or a session the application started itself, by a person whose every tenant is directory only. A password sign-in by a person who administers one of those tenants is not refused: it is held for the second factor (see "The tenant policy", rule 2). |
| `directory_only_personal_account` | `RefusedDirectoryOnlyPersonalAccount` | Auth, the tenant policy: a sign-in with a personal account (through Google or Microsoft, with an account that is in no organization's directory) by a person whose every tenant is directory only. The person used no password, so a login page asks them for their organization's account. |
| `other_tenant` | `RefusedOtherTenant` | Never answered by the session library itself: the generated user management answers it from its hooks where the application declared that a person belongs to one tenant (`identity.WithOneTenantPerPerson()`). The invitation the sign-in carried, or the default role of the directory it came through, would give the person a role in a second tenant; the hook fails the sign-in's transaction, so nothing is consumed and no account is made. |
| `organization_has_tenant` | `RefusedOrganizationHasTenant` | Never answered by the session library itself: the generated user management answers it from its invitation hook. The invitation the sign-in carried makes a tenant, and the organization's directory the person signed in through is already another tenant's; the hook fails the sign-in's transaction, so no tenant and no account is made and the invitation stands. |
| `too_many_attempts` | `RefusedTooManyAttempts` | Auth: a password attempt on a name whose failed attempts reached the limit, or from a client held for its own; answered with 429, the wait in seconds as `retryAfter` and the `Retry-After` header, until the lock passes (see "Attempt limiting and the after-sign-in hook"). |
| `too_many_emails` | `RefusedTooManyEmails` | Auth: a sixth sign-in code or link asked for one address within fifteen minutes while the five before it went unused (a code or link that is used resets the count; the codes of the address's own sign-ins are counted apart from everything else); answered with 429, the wait in seconds as `retryAfter` and the `Retry-After` header, until the lock passes. |
| `invalid_code` | `RefusedInvalidCode` | Auth: the sign-in code entered is not the one sent; the code stands with one fewer attempt (see "The second factor"). |
| `code_expired` | `RefusedCodeExpired` | Auth: a sign-in code entered while none stands: none was sent, or the one sent expired, was used, took its last attempt or was mailed to an address the account no longer holds; the page asks for a new one. |

**The resolver rule.** A custom session data resolver's error decides the code by its
shape. A `sessioninfo.LoginRefusal` (`sessioninfo.NewLoginRefusal(code, cause)`) answers
its code, and the code may be the application's own — `LoginRefusalCode("not_provisioned")`
— as long as the application's login page holds text for it. An httpio client message with
no code answers `login_refused`, and its text stays in the log. Any other error answers
`internal_error`. `LoginRefusal` unwraps to its cause, so the log handler still sees the
client message's status and `errors.Is` still matches the cause.

```go
func resolve(ctx context.Context, txn *spanner.ReadWriteTransaction, req *sessioninfo.NewSessionRequest) (*SessionClaims, error) {
    member, err := lookUpMember(ctx, txn, req.Username)
    if err != nil {
        return nil, errors.Wrap(err, "lookUpMember()") // internal_error
    }
    if member == nil {
        return nil, sessioninfo.NewLoginRefusal("not_provisioned", httpio.NewForbiddenMessage("member is not provisioned"))
    }

    return &SessionClaims{...}, nil
}
```

**The page authors all text.** A login page reads `code` and renders the sentence it holds
for it; it never renders the parameter. Angular applications get the mapping from
`@impulseframework/resource-angular`: `LOGIN_MESSAGES` holds a sentence for every code above,
`UiCoreService.loginMessage(code)` looks one up (empty for an unknown code) and
`publishLoginError(code)` raises it as a notification, and `provideLoginMessages({...})` in
the application's providers adds the application's own codes or rewords a default.

## Custom session data

Custom session data attaches app-specific values to a session — a selected tenant, a role
snapshot, an impersonation context. The values live in a table you own (one row per
session), are written when the session is created, and are available to every request for
the life of the session.

How it works:

- **One struct declares your data.** Each tagged field of your struct `T` maps to a
  column in your table (`spanner:"TenantId"` / `db:"TenantId"`). The library reflects
  over `T` once at startup to derive the columns — there is no hand-written decoder
  and no column list to maintain.

- **A resolver writes it — once, at creation.** Your resolver runs **inside the same
  transaction that inserts the session row** (login, external auth, session
  regeneration) and returns the `*T` to store, committed atomically with the session.
  If it returns an error, the login fails: no session, no cookies. For an `Auth`
  sign-in it runs after the user resolution has committed (see "Auth sessions"); a
  pending identity has no session row, so nothing is resolved until it completes.

- **Every request reads it — automatically.** Session validation fetches the custom
  row together with the session (a single `LEFT JOIN` query) and your handlers get a
  typed `T` back with `auth.API().CustomData(ctx)`. No second round-trip, no manual
  decoding.

- **Mid-session changes are deliberate.** `UpdateCustomSessionData` is a transactional
  read-modify-write: a typed callback mutates the current row and the full row is
  written back. It exists for *changing* state mid-flight (a tenant switcher) — never
  for initial population.

- **Everything is checked at startup.** The session type is generic over the same
  struct (`Auth[MyData, ...]`), and construction verifies the storage's custom data
  configuration was built for it. Misconfiguration is a boot failure, not a request-time
  surprise; a resolver typed to the other driver's transaction does not compile.

- **Cleanup is the database's job.** Sessions are marked expired in place; your
  `ON DELETE CASCADE` foreign key removes the custom row whenever a session row is
  deleted. Regeneration (e.g. a password change) re-resolves fresh — updates don't
  carry over.

That is the whole model: **declare with tags, resolve once, read typed, update
deliberately.** Everything below is reference detail.

One boundary before the details: this is **session data** — born with the session, reset
when the session is regenerated, gone when the session dies. Durable facts about the
*person* (profile fields, provisioning state) belong with the user record — see the
"Custom user data" section below and
[docs/data-storage-guidance.md](docs/data-storage-guidance.md).

### Schema requirements

You create the table. The contract:

- A primary-key column named `SessionId` that is a **foreign key to the session table's
  primary key with `ON DELETE CASCADE`**.
- Never tag a field `SessionId` — it is implied and reserved.
- The resolver (or per-call data) writes the **full row** from `T`, so every tagged field
  is always written — plan `NOT NULL` constraints accordingly.
- **Columns that can hold `NULL` must map to nullable Go types** (Spanner:
  `spanner.NullString` and friends, or pointers; Postgres: pointer fields). Scanning a
  `NULL` into a non-nullable field is a request-failing error.

```sql
-- Spanner
CREATE TABLE SessionCustomData (
    SessionId STRING(36) NOT NULL,
    TenantId  STRING(36),
    RoleId    STRING(MAX),
    CONSTRAINT FK_SessionCustomData_Sessions FOREIGN KEY (SessionId)
        REFERENCES Sessions(Id) ON DELETE CASCADE,
) PRIMARY KEY (SessionId);
```

### The struct-tag contract

The library never introspects your schema. The same identifiers must agree in two places:

1. **Your DDL** — the physical columns.
2. **`T`'s struct tags** — the `spanner:"..."` (Spanner) or `db:"..."` (Postgres) tag on
   each exported field names its column. An untagged exported field uses the field name;
   a `-` tag skips the field; embedded structs contribute their promoted fields.

The derived columns drive the `SELECT c.<name>` list on every read *and* the INSERT
columns at creation. Names are validated for shape at construction (letter/underscore
start, ≤128 chars), but whether they exist in your table is discovered by the database —
a mismatch surfaces as a query error.

### Configuration — Auth

An `Auth` store takes one custom session data configuration. The struct, the table
name and the resolver are declared together with `NewCustomSessionData`; `T` and the
store's transaction type are inferred from the resolver, and the transaction type
selects the driver and the tags that name the columns (`db:"..."` for PostgreSQL,
`spanner:"..."` for Spanner). The configuration is attached with the store's
`WithCustomSessionData`, which takes only a configuration typed to that store's
transaction: one built for the other driver does not compile there. The session type is
built for the same `T`:

```go
type MyData struct {
    TenantID string `db:"TenantId"`
    RoleID   string `db:"RoleId"`
}

sessionData, err := sessionstorage.NewCustomSessionData(
    "SessionCustomData",
    func(ctx context.Context, tx pgx.Tx, req *sessioninfo.NewSessionRequest) (*MyData, error) {
        // Runs ONCE per session creation, inside the transaction that inserts the
        // session, after the user resolution: req.UserID is the user (one the
        // resolver just provisioned included) and req.User says how it was found.
        member, err := loadMember(ctx, tx, req.UserID)   // reads are tx-consistent
        if err != nil {
            return nil, err                             // aborts the sign-in
        }
        return &MyData{TenantID: member.TenantID, RoleID: member.RoleID}, nil
    },
)
if err != nil { /* non-struct T, no persistable fields, invalid identifier, reserved SessionId, ... */ }

store := sessionstorage.NewPostgresAuth(pool, sessionstorage.WithAuthEvents(events)).
    WithCustomSessionData(sessionData)
auth, err := session.NewAuth[MyData, session.NoCustomData](store, cookieKey, superAdministrators, mail)
```

With a `nil` resolver, name both types: `NewCustomSessionData[MyData, pgx.Tx]("SessionCustomData", nil)`;
session creation then performs a plain insert, and custom data comes only from per-call
values (below) or `UpdateCustomSessionData`. A Spanner store takes a resolver over
`*spanner.ReadWriteTransaction` and reads `spanner:"..."` tags; a struct tagged for both
drivers serves either.

### Configuration — Preauth

A `Preauth` store takes its one configuration the same way, through its own
`WithCustomSessionData`, typed to its transaction, and the session type is built for
the same `T`:

```go
sessionData, err := sessionstorage.NewCustomSessionData[MyData, pgx.Tx]("SessionCustomData", nil)
if err != nil { /* as above */ }

store := sessionstorage.NewPostgresPreauth(pool).WithCustomSessionData(sessionData)
preauth, err := session.NewPreauth[MyData](store, cookieKey)
```

A Preauth session's row is the caller's: `Login` is given it (see "Per-call custom
data"). A configuration that carries a resolver has it asked for a session started
with no row, a `Login`'s with `Reason: ReasonPreauth` and the username; the request
names no user and no identity, since a Preauth keeps none.

### Reading the data in handlers

```go
data, err := auth.API().CustomData(ctx)                    // returns MyData
// or, without the session type in scope:
data, err := sessioninfo.CustomDataFromCtx[*MyData](ctx)   // note: pointer type
```

The context stores a `*T` (pointer to the config's struct type), so `CustomDataFromCtx`
must be instantiated with the pointer type; `API().CustomData` hides this and returns the
value. Because the read is a `LEFT JOIN`, a session with **no** custom row still loads —
it yields a **zero-value `T`**, not an error.

### Per-call custom data

When the values come from the request rather than from config-time logic, pass the row
directly at session creation — written atomically with the insert, and **the configured
resolver is skipped for that creation (per-call data wins)**:

```go
// Auth: a session the application authenticated itself, by user ID, recording how
// (decided like a login: the sign-in policy answers when one is set):
sessionID, err := auth.API().StartAuthenticatedSession(ctx, w, userID,
    []sessioninfo.AuthEvent{{Method: "api-key"}},
    &MyData{TenantID: tenantID, RoleID: roleID},
)

// Preauth (trust-the-caller):
sessionID, err := preauth.API().Login(ctx, w, username,
    &MyData{TenantID: tenantID},
)
```

At most one value may be passed — it is the complete custom data row. Per-call data
requires a custom session data configuration on the storage (the config may have a nil
resolver). Passing data with no configuration attached is an error before anything is
inserted.

### Mid-session updates

Some session state legitimately changes while the session is alive — the canonical case
is a **tenant switcher**: the resolver records the user's default tenant at login, and the
user later selects a different one. That is what `UpdateCustomSessionData` is for:

```go
err := auth.API().UpdateCustomSessionData(ctx, sessionID, func(data *MyData) error {
    data.TenantID = newTenantID

    return nil
})
```

It is available on every session API: `AuthAPI` and `PreauthAPI`.

Semantics to know:

- **It is a transactional read-modify-write.** The current row is read (zero-value `T`
  when the session has no row yet), your callback mutates it, and the **full row** is
  written back — fields you don't touch are preserved, because they ride along in `data`.
  A callback error aborts the transaction with nothing written. Concurrent updates are
  serialized by the transaction; last committed callback wins.
- **It takes effect on the next request** — the per-request read picks up the new
  values; requests already in flight see the old ones.
- **The library does not authorize the change.** The resolver established what the user
  was granted at login; before writing a switch, your handler must verify the user is
  allowed the new value (e.g. is a member of the target tenant).
- **Updates do not survive regeneration.** A password change re-resolves fresh and the
  selected value reverts to the resolver's answer (see Session regeneration).
- Expired sessions are rejected; a custom data configuration must be attached.

The rule of thumb for choosing the verb: **resolve** for identity-derived state
(what the user *is* at login), **update** for user-chosen context within
already-granted options (which tenant they're *looking at*), and **regenerate/destroy**
for privilege changes — if a user's rights change, kill their sessions rather than
patching them (`DestroyUserSessions` on `Auth`); the next login re-resolves.

Never use `UpdateCustomSessionData` for initial population — that belongs in the creation
transaction (resolver or per-call data), which is atomic with the session insert.

### Session regeneration

A password change destroys all of the user's sessions and starts a fresh one for the
caller (session-fixation protection). Custom session data is **resolved fresh** for the
new session — the resolver runs again with `Reason: ReasonRegeneration` — and values
previously written via `UpdateCustomSessionData` do **not** carry over. If losing a value
on regeneration feels like data loss, it was user data, not session data — see
[docs/data-storage-guidance.md](docs/data-storage-guidance.md).

### Where failures surface

| Misconfiguration / failure | Surfaces as |
|---|---|
| Non-struct `T`, no persistable fields, invalid tag identifier, reserved `SessionId`, duplicate columns | Error from the config constructor, at startup |
| Session type's `T` doesn't match the storage config's `T` (or no config attached) | Error from the session-type constructor, at startup |
| A config typed to the other backend's transaction given to a store's `WithCustomSessionData` | Compile error |
| Resolver returns an error | Session creation aborts atomically: no session row, no custom row, no cookies; login fails (OIDC: redirect to the login page with a refusal code, see "Login refusal codes") |
| Per-call data with no config attached, or more than one per-call value | Error before any insert |
| Column name not in your DDL | Database error from the creation transaction (aborts atomically) or from the per-request query |
| `NULL` column scanned into a non-nullable field | The request fails (401) — map nullable columns to nullable Go types |
| `UpdateCustomSessionData` callback returns an error | Transaction aborts; nothing written |
| `UpdateCustomSessionData` on an expired session | Rejected (bad request); the row is not written |
| Session has no custom row | Not a failure: reads yield a zero-value `T` (LEFT JOIN) |

## Custom user data

Custom user data is the durable counterpart of custom session data: app-defined values
attached to the **user record** — a locale, provisioning state, profile fields synced
from the IdP. Where session data is born and dies with one login, user data **survives
logout, session expiry, and regeneration, and dies with the user** (your
`ON DELETE CASCADE` FK removes it when the user row is deleted).

The mechanism mirrors custom session data deliberately — declare with tags, one
validated config unit per backend, typed reads, transactional RMW updates — with two
differences that follow from durability:

- **Reads are on demand, never per-request.** User data is *not* joined into the
  session read and never appears in the request context; fetch it when you need it with
  `API().CustomUserData(ctx, userID)`. A user with no row yields a zero-value `U`.
- **The write path depends on the auth mode**, because the modes learn about users
  differently:
  - **Auth**: initial data is **per-call on `CreateSessionUser`**, written atomically
    with the user insert, or written by a custom account rule's `signin.Provision` hook
    in the sign-in's transaction for a user it provisions: an identity's claims reach
    the rule, which decides what to keep.
  - **Preauth**: unsupported — there is no user record to anchor durable data to
    (construction error if configured).

### Schema requirements

Identical to the session-data contract with one substitution: the primary-key column is
named `UserId` and is a **foreign key with `ON DELETE CASCADE`** to `SessionUsers(Id)`.
Never tag a field `UserId` — it is implied
and reserved. Nullable columns must map to nullable Go types.

```sql
-- Spanner
CREATE TABLE UserCustomData (
    UserId  STRING(36) NOT NULL,
    Locale  STRING(MAX),
    Theme   STRING(MAX),
    CONSTRAINT FK_UserCustomData_SessionUsers FOREIGN KEY (UserId)
        REFERENCES SessionUsers(Id) ON DELETE CASCADE,
) PRIMARY KEY (UserId);
```

### Configuration — Auth

An `Auth` store takes one custom user data configuration, which fits either driver:
`NewCustomUserData` validates the columns `U`'s `db:"..."` tags name for PostgreSQL and
its `spanner:"..."` tags name for Spanner, and the store's `WithCustomUserData` attaches
it. The session type is built for the same `U`:

```go
type UserData struct {
    Locale string `db:"Locale" spanner:"Locale"`
    Theme  string `db:"Theme" spanner:"Theme"`
}

userData, err := sessionstorage.NewCustomUserData[UserData]("UserCustomData")

store := sessionstorage.NewPostgresAuth(pool, sessionstorage.WithAuthEvents(events)).
    WithCustomSessionData(sessionData).
    WithCustomUserData(userData)
auth, err := session.NewAuth[MyData, UserData](store, cookieKey, superAdministrators, mail)

// Create: initial data lands atomically with the user insert (at most one value —
// it is the complete row). Omit it for a plain insert (reads yield zero-value U).
id, err := auth.API().CreateSessionUser(ctx, req, &UserData{Locale: "en-AU"})

// Read: on demand, by user ID — never from the session context.
u, err := auth.API().CustomUserData(ctx, userID)

// Update: transactional read-modify-write — mutate the current row (zero-value U
// when none) and the full row is written back.
err = auth.API().UpdateCustomUserData(ctx, userID, func(d *UserData) error {
    d.Theme = "dark"
    return nil
})
```

### Where failures surface

| Misconfiguration / failure | Surfaces as |
|---|---|
| Non-struct `U`, invalid tag identifier, reserved `UserId`, duplicate columns | Error from the config constructor, at startup |
| Session type's `U` doesn't match the storage config's `U` (or no config attached) | Error from the session-type constructor, at startup |
| A custom user data configuration on the storage given to `NewPreauth` | Error from `NewPreauth`, at startup |
| Per-call data on `CreateSessionUser` with no config attached, or more than one value | Error before any insert |
| `UpdateCustomUserData` for a user ID that doesn't exist | Not-found error; nothing written |
| User has no custom row | Not a failure: reads and RMW yield a zero-value `U` |

## Impersonated sessions

An impersonated session operates as a principal other than the person who
authenticated: as another **user** (support staff seeing the application exactly as
that user does, usually read-only) or as a **role** (an administrator working under a
role chosen for the session). Every session type supports both, `Auth` and `Preauth`,
through the same request on each `API()`, which only the application's declaration of
the impersonation it offers makes.

The model rests on two identities that are never conflated:

- **Actor** — who authenticated. Constant for the session's life; the identity audit
  and attribution always name.
- **Principal** — what permission checks evaluate against
  (`accesstypes.UserPrincipal` or `accesstypes.RolePrincipal`).

**A session is a session.** Once established, an impersonated session flows through
`ValidateSession`, `sessioninfo`, custom session data and every handler exactly as an
ordinary session does. The session's `Username` is its *effective identity*: the
impersonated user for a user principal (so every consumer sees precisely what that user
would see), or the actor for a role principal (nobody's identity is borrowed). The
**impersonation record** — not the username — is what marks the session as
impersonated, and only the way the session is *established* is new.

### Enabling

Create the record table from the shipped DDL
(`schema/{spanner,postgresql}/impersonation/migrations`) and attach it to the storage.
The table deliberately has **no foreign key to the session table**: the record is
evidence and outlives the session; retention is the application's policy.

```go
imp, err := sessionstorage.NewImpersonationTable("SessionImpersonations")

auth, err := session.NewAuth[MyData, session.NoCustomData](
    sessionstorage.NewSpannerAuth(client,
        sessionstorage.WithAuthEvents(events),
        sessionstorage.WithImpersonation(imp)).
        WithCustomSessionData(sessionData),
    cookieKey, superAdministrators, mail,
    session.WithImpersonationTimeout(time.Hour),        // hard cap; default one hour
    session.WithImpersonationAudit(func(ctx context.Context, e sessioninfo.ImpersonationEvent) error {
        return auditTrail.Record(ctx, e)                 // Started, Ended, IdentityOperationBlocked
    }),
)
```

The same `WithImpersonation` option and `WithImpersonationTimeout` /
`WithImpersonationAudit` session options apply to `NewPreauth`.
Without `WithImpersonation` nothing changes: no session is ever impersonated and the
impersonation APIs return a configuration error.

### Establishing a session

An impersonated session starts from a request, and **the declaration is the only way
to make one**. An application declares the kinds of impersonation it offers, each with
the grant that allows it and the widest mask that grant gives (the next section), and
the declaration turns what is asked into the request: `Request` for a person signed in
here, `RequestFor` for a server-side caller. `ImpersonationRequest` has no field an
application can set, so a request written out by hand does not compile, and the zero
value, which no declaration made, is refused when a session is started from it. So
the session API starts no session that no declared kind allows, and none without a
mask somebody chose: a kind that gives everything the person holds says so where the
application is read, with `accesstypes.AllowAll()`.

**The storage beneath is sealed.** The write the session library makes once it has
decided writes a session and its record with whatever principal, mask and actor it is
handed, and asks nothing. So it is made by the session library alone, through its API,
and the library hands it to no application code. It is a method of the store's interface
(`sessionstorage.BaseStore`) that is not exported, and its name belongs to a package
inside the library (`session/internal/sealed`), which the go command lets no package
outside the session library's directory import. Go lets no other package call a method
named there, or declare one.

**So are the reads that decide one.** Who is recorded as the actor, whom the session
acts as, what its mask keeps and whether it has ended are read through methods of the
same kind, which `sessionstorage.BaseStore` and `sessionstorage.UserStore` carry beside
the write: the actor's own session and the account acted as when a session is started,
and the session and its user on every later request. So:

- **A direct call does not compile**, on the library's stores or on the store's
  interface, under any name.
- **A store that wraps the library's keeps working, and decides no impersonated
  session.** It embeds the library's store (`*sessionstorage.Auth`,
  `*sessionstorage.Preauth`, or the interface over one) and has the write and those
  reads from it. It cannot replace them: a method of the same name in the wrapper's own
  package is another method, which the library never calls. And its own `Session`,
  `User` and `UserByUserName` are never asked what a session that acts as someone else
  is, because the library reads the stored rows itself. A wrapper that changes what
  `Session` answers changes no such session's mask, actor or principal, and a read
  cache in front of it keeps none alive after it ended. A wrapper does decide an
  ordinary session: its `Session` is asked for every session the library's own read
  does not find impersonated, and its `User` for that session's user. An answer of its
  own that carries an impersonation record is refused as an invalid session. So is an
  answer that is not the session the request's cookie names: one that carries another
  session's id, and, where the library's own store holds the session, one that names
  another account or another username than that row. A wrapper adds to an ordinary
  session and does not change whose it is, so a cache that answers one person's cookie
  with another person's session has the request refused, and no session is started as
  that person. The same holds for the two other things a request is known by. Its
  `User` answer is held to the account the session names: it carries the id that was
  asked for and, where the library's own store holds the account, the username of that
  row, or the request is refused as an invalid session, so the routes for a person's
  own account never act on another person's. An account that row holds disabled is
  disabled whatever the wrapper answers, as one the wrapper answers disabled is, so a
  store that keeps the accounts it has read keeps no disabled account signed in. The
  account of an application token is held the same way: the wrapper's `User` answer
  carries the id the token names and, where the library's own store holds the account,
  the username of that row, or the request is refused as an invalid token, and the token
  of an account that row holds disabled is refused whatever the wrapper answers. So no
  program's request runs as another person, and no disabled account's token keeps
  working. The issue of an application token (`CreateApplicationToken`) is held too: an
  account either one holds disabled is refused a new token as a disabled account is,
  and a `User` answer that is another account than the one asked for fails the issue,
  with no token made for either person. A password is not accepted for an account
  either one holds disabled: the
  sign-in is refused as a disabled account's is (`user_disabled`), and so are the check
  of the credentials alone (`ValidateCredentials`) and the password confirmation of a
  pending identity, before anything is linked to the account. A `Principal` its
  `Session` answer carries is not taken: an ordinary session's checks are made for whom
  the principal resolver chose, or for the session's own user. And where the library's
  own store holds the session, the session is expired when that row is and was last used
  when that row says, so a cache keeps no signed-out or idle session working (a wrapper
  may still end a session early, by answering it expired or with an error). The rest of
  an ordinary session is the wrapper's: its custom data and the account's other columns.
  So is the row of an application token (which token it is, the account it names and the
  outlet it is for), and a token starts no session that acts as someone else. A session
  that acts as someone else starts from the session the request's cookie names, which
  must be held under the same account in the library's store as the session the request
  was validated as, or nothing is started. Over a wrapper the library reads an ordinary
  session twice on each request, once itself and once through the wrapper.
- **A store written from nothing is no session store.** It cannot declare the methods,
  so a program that hands one to `NewAuth` or `NewPreauth` does not compile. A test
  double written by hand embeds `sessionstorage.NoImpersonatedSessions`, which gives it
  the methods with a write that refuses and no read: its `Session` answers for an
  ordinary session and never for an impersonated one.
- **The mocks write nothing.** `mock_sessionstorage.NewMockBaseStore`,
  `NewMockPreauthStore`, `NewMockUserStore` and `NewMockAuthStore` mock every other
  method of the store. Their write refuses, and a `Session` of theirs that answers an
  impersonation record is refused as an invalid session. A test in which a session is
  impersonated runs over a store the library made (`sessionstorage.NewPostgresAuth` and
  the others, on a test database).

A write the library did not make is refused with `sessionstorage.ErrImpersonationSealed`
before anything is written: that is what a mock, a store that embeds
`NoImpersonatedSessions` and a store value nobody built answer. An application starts an
impersonated session through the session API, from a request its declaration made, and
through the library's API there is no other way.

The seal is the library's API and no more. These are outside it:

- **The tables.** The application owns them, so SQL of its own writes any row, and the
  store's `NewSession` and `CreateSession` write an ordinary session for any username,
  by design.
- **The resolver of custom session data.** An application that keeps custom session
  data registers a resolver (`sessionstorage.NewCustomSessionData`), and the library
  calls it inside the write's own transaction, once the session and its record are
  written there, with the request and the transaction. It is the application's code.
  What it changes in the request reaches no row the library writes; what it can do to
  the session and its record is a write of its own on those tables in that transaction,
  which is the limit above.
- **The connection the application hands the store.** The store writes through the
  connection it was made with, and that connection is the application's. For PostgreSQL
  it is any value that answers the store's `postgres.Queryer`: a pool, or a type of the
  application's own that wraps one, which is handed every statement the library sends,
  with its arguments, and could send another. For Spanner it is the client the
  application dialed, and the gRPC interceptors it was dialed with are handed every
  request and could change it. Both see and could rewrite what the library writes. It is
  the tables again, one step before the database.
- **What a store that wraps the library's still carries.** It answers for ordinary
  sessions, as said above: what it adds to one is the application's, and a session the
  library's own store does not hold is its to answer whole, under the id that was asked
  for. And the writes that end or extend an impersonated session
  pass through it (`DestroySession`, `DestroyImpersonatedSession`,
  `DestroyImpersonatedSessions`, `EndImpersonation`, `UpdateSessionActivity`), as does
  the listing of the live ones (`ActiveImpersonations`). A wrapper that drops one of
  those writes leaves the session to its idle timeout and its hard cap, both of which
  the library reads from its own store.
- **Code the type checker does not read.** The seal is the type checker's, and a
  program that imports `unsafe`, or brings assembly or C of its own, steps around what
  the type checker refuses, here as anywhere in Go. The plainest form is a
  `//go:linkname` directive in a file that imports `unsafe`: it links to a function or
  a variable of the library by its symbol's name, the sealed write among them, and the
  linker accepts it. `impulse check` refuses the directive (`framework-linkname`): a
  `//go:linkname` that names a package of the library in any Go file under the
  application's root, a test file, a generated file and a vendored file included, and
  a file in a directory the tree reaches through a symbolic link. It refuses nothing
  else of this kind: `unsafe` reaches the memory of a value a program holds with no
  directive at all.
- **A module under the library's import path.** The go command opens an internal
  directory to every package whose import path is under the directory's parent, and it
  goes by the path, not by where the code is kept. A module whose path is under the
  library's is inside the library to it: a `replace` in `go.mod` or a workspace gives
  a directory of the application's own such a path, and its packages may then import
  the internal package and make the write themselves. `impulse check` refuses it
  (`framework-modules`): a module path under the library's that is not one of the
  library's own modules, as the application's own module path, a requirement or a
  replace in `go.mod`, as a used directory's module or a replace in a `go.work` at the
  application's root, and as a requirement or a replace in the `go.mod` of each
  directory that `go.work` uses, since in a workspace the go command applies the
  replaces of every module it uses.
- **A copy of the library the application changed.** An application that builds against
  a copy of the library with a change of its own in it, through a `replace` of the
  library's module or a vendor directory edited by hand, builds another library, and
  nothing this one refuses holds there. `impulse check` warns of the replace (`pins`);
  nothing compares a vendor directory with the release.

The two checks read the application's own tree as it stands: its Go files, its `go.mod`,
a `go.work` at its root and the `go.mod` of each directory that `go.work` uses. They do
not read the code of the modules the application requires, or a build told to read
files from elsewhere: the `-modfile` flag names another module file, the `-overlay`
flag replaces or adds source files from anywhere, and the `GOFLAGS` variable carries
either with nothing on the command line. They do not read a workspace file that is
not at the application's root either: the go command takes a `go.work` from any
directory above the one it runs in, and the file the `GOWORK` variable names, and
either can use a directory whose module path is under the library's. An application
that must rule those out reads its dependencies, its build's commands and the
environment they run in, in its own review.

The application's principal resolver (`WithPrincipalResolver`) is not on this list: it
is never asked about a session that acts as someone else ("Choosing the principal").

**No option opens the session.** The session the library builds inside `NewAuth`,
`NewPreauth` and the other constructors is the one thing that starts that write, so
nothing hands it to a program. The options that configure it (`session.WithSessionTimeout`,
`WithImpersonationTimeout`, `WithImpersonationAudit`, `WithPrincipalResolver`,
`WithLogHandler` and the table names) are opaque values: `session.BaseSessionOption` is
a struct with no exported field and no method, which a program passes to a constructor
and can do nothing else with. It is not a function, so a generic function instantiated
with it learns nothing of the session it configures, and a program can neither call
one, read one nor write one of its own; each of those does not compile. The zero value,
the one option a program can still declare, sets nothing and is refused by every
constructor. Reflection can find the type of the session inside and make a value of it;
that value was not built by the library, and it starts no impersonated session: it is
refused with `sessionstorage.ErrImpersonationSealed` before the store is asked anything.

```go
// A person signed in here views bob's pages. The route is behind ValidateSession: the
// actor is the validated session's user, acting from that session.
req, err := impersonations.Request(ctx, &session.ImpersonationAsk{
    Kind:      "view",
    Principal: bob.ID.String(), // a person is named as the permission store names them: by the id of their account
    Reason:    "ticket JRN-123",
}, resource.HoldsAction(a.UserPermissions(r), accesstypes.GlobalScope()))
if err != nil {
    return httpio.NewEncoder(w).ClientMessage(ctx, err) // 400 or 403, a sentence the person can act on
}
id, err := auth.API().StartImpersonatedSession(ctx, w, req)

// A server-side caller: the administration application signed alice in and hands her
// over to this portal, to work under a role. The call arrives on no session of this
// application, so it names the actor, and the portal's own check of the calling system
// answers for the kind's grant.
req, err := impersonations.RequestFor(ctx,
    session.ImpersonationActor{Name: "alice@example.com", Realm: "admin-portal"},
    &session.ImpersonationAsk{Kind: "partner", Principal: "PartnerViewer"},
    func(ctx context.Context, grant accesstypes.Resource) (bool, error) {
        return handOff.MayStart(ctx, grant) // which kinds the system this handler authenticated may start
    })
if err != nil {
    return httpio.NewEncoder(w).ClientMessage(ctx, err)
}
id, err := auth.API().StartImpersonatedSession(ctx, w, req, &MyData{PartnerID: partnerID}) // custom session data rides along as usual
```

| The call | Makes the request with | The actor | The check of the kind's grant |
| --- | --- | --- | --- |
| Arrives on a person's validated session of this application | `Request` | That session's user, acting from that session. A call that carries no validated session is refused as a mistake in the program | Whether the person holds the grant: `resource.HoldsAction` over the request's permissions |
| A server-side caller: a hand-off from the system that signed the actor in, or any call that arrives on no session here | `RequestFor`, with an `ImpersonationActor` | Named by the call. With a `Realm`, an actor another system signed in, recorded under the name given. With none, a person of this application, whose own live session the call names in `SessionID`. A call that does carry a validated session, and an actor with neither a `Realm` nor a `SessionID`, are refused as mistakes in the program | The caller's own: what the calling system, which the handler has authenticated, may start. A caller it refuses is answered 403, "this caller may not start a session of the kind…" |

`AuthAPI.StartImpersonatedSession` and `PreauthAPI.StartImpersonatedSession` take the
same request: a session type that keeps no accounts declares its kinds as any other
does. A declaration is held to the application's actions, which the generated
permission collection lists (`router.Collection()`). An application that has none, a
session type used on its own, names the grants of its kinds itself:

```go
// A partner portal on Preauth, with no generated collection. Its kinds' grants are
// names its own check answers for; no action carries a class, so its masks keep or
// drop permission kinds.
impersonations, err := session.NewImpersonations(session.NamedActions("ViewAsUser", "WorkAsPartner"),
    session.ImpersonatesUser("view", "ViewAsUser",
        accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read)),
    session.ImpersonatesRole("partner", "WorkAsPartner", accesstypes.AllowAll()),
)
```

What a request carries is read through its methods (`Kind`, `Actor`, `ActorRealm`,
`ActorSessionID`, `Principal`, `Mask`, `Reason`, `MaxDuration`), so a route can answer
the person with what their session may do (`req.Mask().Words()`). None hands out
anything that can be written to.

A request starts one session. It is for the call that follows it: the grant was checked
once, when the request was made, and that check stands for one start. The first
`StartImpersonatedSession` a request is handed spends it, whether or not the session
came to be, and a second start from the same request, or from a copy of it, is refused
before anything is written, with an error that says so (a mistake in the program, so no
client message). A handler takes a new request from the declaration for each session it
starts, which checks the grant again.

The session row and the record are written in one transaction; the cookie is set only
after the `Started` event has been delivered (a failing audit hook destroys the session
and fails the call). Refused everywhere: a request no declaration made, a request that
has started a session already, a missing actor or principal, a caller that is itself an
impersonated session (no chaining, refused when the request is asked for and again when
a session starts), and a person of this application whose own session is not their live
session here.

A call that arrives on a validated session of this application (the context has passed
`ValidateSession`) is bound to that session twice. The declaration names nobody but its
user: `Request` reads the actor off it, and `RequestFor` refuses a call that carries
one. And the library binds again as it establishes: the actor must be the session's
user, acting from it, and never an actor of another system, since a person signed in
here is local. The binding keeps a caller from minting a session as another user by
naming them and one of their session IDs, which appear in logs, spans and
`ActiveImpersonations`. A call with no session in its context (a server-side caller) is
taken at its word about the actor, so its handler must authenticate the caller, and its
check of the grant is where it says what that caller may start.

*Who may start which kind* is what the declaration holds every request to: the kind's
grant, asked of the application's check. *Whom* a person may impersonate under a kind
(which people, which roles) is not something a kind says: the check is asked about the
grant, never about the principal. An application that limits it makes that check
itself, before it asks for the request or inside the check it passes, which it writes.
The library records what happened.

### Impersonation is granted with a mask

The rule, in the words it was given in: **"If you allow them to impersonate without a
mask, then you're giving them those privileges."** To let someone impersonate a person
with no mask is to give them everything that person holds, their ways of changing how
other people sign in among it. Nothing else stands in the way: no action refuses an
impersonated session by a rule of its own because of what the action does. **The mask is
the control.**

So the grant to impersonate carries a mask, and an application says both in one place.
It declares each *kind* of impersonation it offers with the grant that allows it and the
widest mask that grant gives:

```go
// Built once, where the application is constructed. The generated collection
// (router.Collection()) is what the declaration is held to.
impersonations, err := session.NewImpersonations(collection,
    // View as a person: reading only.
    session.ImpersonatesUser("view", resources.ViewAsUser,
        accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read)),
    // Act as a person: everything they may do, except the credential actions and the
    // actions that change who may do what.
    session.ImpersonatesUser("act", resources.ActAsUser,
        accesstypes.AllowAll().Refusing(
            accesstypes.RefuseClass(router.ClassCredential), accesstypes.RefuseClass(router.ClassAccess))),
    // Act as a role: what the role holds. The kind says in so many words that it is unmasked.
    session.ImpersonatesRole("role", resources.AssumeRole, accesstypes.AllowAll()),
)
```

- **The grant** is an action the application registers by hand
  (`@manualAddResource(Execute)` on a constant), which its role file gives on purpose.
  Each kind has a grant of its own, since a grant carries one widest mask: the grant to
  view as a person is not the grant to act as them.
- **The widest mask is not optional.** A kind that gives everything the person holds
  says so, `accesstypes.AllowAll()`, where the application is read.
- **The declaration is checked when the application starts.** `NewImpersonations`
  refuses a kind with no name, a name or a grant two kinds share, a grant that is no
  Execute action of the application, and a mask that names something the application
  does not have (below): a kind or a refusal that names nothing would keep or refuse
  nothing, and whoever wrote it would believe otherwise. A mask written with the
  collection file's own constant (`router.ClassCredential`, one per class in use) does
  not compile once no action carries the class.

A mask, the declaration's or a request's, is held to the application's actions. It may
keep only the six permission kinds, spelled as they are (`Read`, not `read`). What it
refuses must be something to refuse:

| A refusal of | Is accepted when |
| --- | --- |
| A class | Some action carries it |
| Execute on a name | The name is an action |
| List, Read, Create, Update or Delete on a name | The name is a record, whether or not the application offers that permission on it: `RefuseWrites` of a record that is never deleted holds as it reads |
| A permission on `Record.field` | The field carries that permission itself (`perm` on the field), which is when a check asks about the field by name; any other field is refused with its record |

What a mask keeps out of a class it refuses (`accesstypes.RefuseClassExcept`, "What a
mask says") must be something the exception keeps. An exception that keeps nothing is
refused, because whoever wrote it would believe otherwise:

| An exception | Is refused when |
| --- | --- |
| Names what the application does not have | No action, record or field of that name carries the permission (the rules of the table above) |
| Names what the class does not cover | The action does not carry the class, or that write of the record does not (a field is judged by its record) |
| Names what the same mask refuses all the same | The mask refuses it by name, does not keep its kind, or refuses another class it carries without the same exception |

A request names the kind and may name a narrower mask. `Request` turns it into the
`ImpersonationRequest` a session starts from, or refuses it with nothing started:

```go
req, err := impersonations.Request(ctx, &session.ImpersonationAsk{
    Kind:      body.Kind,      // "view", "act" or "role"
    Principal: body.Principal, // the id of the person's account, or the role's name
    Mask:      asked,          // the zero mask asks for none: the session takes what the kind gives
    Reason:    body.Reason,
}, resource.HoldsAction(a.UserPermissions(r), accesstypes.GlobalScope()))
if err != nil {
    return httpio.NewEncoder(w).ClientMessage(ctx, err) // 400 or 403, a sentence the person can act on
}
id, err := auth.API().StartImpersonatedSession(ctx, w, req)
```

The session's mask is the grant's, narrowed by the request and never widened
(`accesstypes.PermissionMask.Narrow`):

| The request | The session's mask |
| --- | --- |
| Names no mask | The kind's widest |
| Names permission kinds, each of which the kind gives | Those kinds, and everything the kind refuses inside them |
| Refuses a class, an action or a record's permission beside what the kind refuses | The kind's kinds (or the ones it named), with both sets of refusals: refusals are joined |
| Does not refuse a class the kind refuses with exceptions | The class refused as the kind has it, exceptions and all |
| Refuses a class the kind refuses with exceptions, naming some of the kind's exceptions or none | The class refused with the exceptions the request named: a request drops an exception by leaving it out |
| Refuses by name something the kind keeps out of a class | The exception is dropped and the name is refused |
| Keeps something out of a class that the kind does not keep out of it | **Refused (403), never trimmed** |
| Names a kind of permission the grant does not give | **Refused (403), never trimmed**: nobody is left believing they hold what they do not |
| Names a kind that is no permission kind (`read` for `Read`) | Refused (400), with the kinds there are: a session that kept a kind nobody checks would keep nothing |
| Refuses a class no action carries, or names an action, a record or a field the application does not have | Refused (400) |

Letting the person choose a narrower mask is a safety precaution, theirs to take, and
not a permission: a support engineer who means only to look asks for reading only under
a grant that would let them act. `Request` also refuses a kind the application does not
offer (400), a request with no principal (400), and a person who does not hold the
kind's grant (403). `RequestFor` does the same for a server-side caller, which names
the actor and passes its own check ("Establishing a session").

A person refused through `Request` reads one plain sentence: "You may not start this
kind of session." where they do not hold the kind's grant, and "This kind of session
gives less than you asked for." where the mask they asked for is wider than the kind's.
The kind's name, its grant and what the two masks say are in the server's log and never
in the answer. A server-side caller of `RequestFor` is answered in full, with the kind,
the grant its check refused and what the request asks for beyond the kind: a program
reads that answer, or its operator.

#### What a mask says

A mask (`accesstypes.PermissionMask`) says which permission kinds the session keeps
(List, Read, Create, Update, Delete, Execute) and, inside a kind it keeps, what it
refuses:

| Built with | Refuses |
| --- | --- |
| `MaskPermissions(DenyAll(), List, Read)` | Every kind but the ones named |
| `.Refusing(RefuseClass(class))` | Every action that carries the class, the ones declared after the mask was written among them |
| `.Refusing(RefuseClassExcept(class, exceptions...))` | The class, except what is named: an action (`ExceptAction`), a record's three writes (`ExceptWrites`), or one permission on a record or on one of its fields (`ExceptPermission`). A name beats its class, and the rest of the class stays refused, whatever is added to it later |
| `.Refusing(RefuseAction(action))` | One action by name (Execute on it), and nothing else |
| `.Refusing(RefusePermission(perm, record))`, `.Refusing(RefuseWrites(record)...)` | One permission on one record and its fields (or on one field, named `Record.field`, and nothing else); the three writes of a record |

An action carries its **classes** where it is declared: `@class(name)` on a generated
method's struct or on the constant that registers the action by hand
(`@manualAddResource(Execute)`), or `ManualRegistration.Classes` (the resource package's
"Annotation reference"). A class is an action's alone: a registration of another
permission is a record's, and the generator refuses a class on it. The library
names two classes and marks its own actions with them. `accesstypes.ClassCredential`,
`credential`, is on every generated identity method that changes how a person signs in
(a new password required, a password removed, a reset link
sent, a sign-in identity unlinked, a username or an address changed).
`accesstypes.ClassAccess`, `access`, is on every one that gives or takes away what
someone may do (a role given or removed, a role a person takes themselves for a set
time, a person put in a group or taken out, a role
given to a group or taken from it, a group made or deleted, an invitation sent or
withdrawn, which carries the roles its person will hold, and what a
sign-in method or a tenant's directory gives everyone who arrives through it), and on the
settings that say
who may sign in at all: the switch that makes a tenant sign in with its directory
alone, and the one that lets people ask at the sign-in page for a link that resets
their own password. The making of a tenant of one's own carries it too: the generated
create-your-tenant route, which no grant admits, has an action's name for the mask to
refuse, and asks the mask itself (the resource package's "How a tenant comes to
exist", "The first administrator"). An account disabled, enabled or deleted carries
neither class: that is the work a person who stands in for an administrator is there to
do, so a mask that refuses both classes leaves it to them. An application marks its own
actions with either or with a class it names. The generated permission collection
carries each action's classes, which is where a check reads them.

**What no mask decides.** In an impersonated session nobody is the subject of what they
do: the generated user management refuses every change to the account of the person who
started the session, whatever the mask keeps and whether or not the kind was declared,
since the rule reads the session's record alone (the actor it names). A person who acts
as an administrator would otherwise give their own account a role and keep it after the
session ends, or enable their own account again after it was disabled. Disabling,
enabling and deleting the real person's own account are refused the same way, in one
sentence form and with one log line, and the session API's own three switches
(`DeactivateSessionUser`, `ActivateSessionUser`, `DeleteSessionUser`) ask the rule
themselves of the session they are called in, for a route an application wrote. That
is where the rule lives. The store's own three beneath them (`DeactivateUser`,
`ActivateUser`, `DeleteUser`) are bare writes that read no session and ask nothing, so
a route an application wrote that calls the store's method makes the switch unasked:
it calls the session API's. The rule covers a record that decides what a person may do as well: the
generated write of a record the policy reads as a subject value or a subject set (a
clearance, a place in a team) refuses the real person a row that names them, asked
before the write and after it, so they neither raise their own clearance nor move
someone else's row onto themselves. An application's own action that names a person
asks the same rule (`identity.RefuseImpersonatorAsSubject`; the resource package's "User
management", "The rules on who is asking"). The class `access` is for what that rule
cannot see: a role that reaches the person through a group they are in, through another
account or through a colleague. That is the rule's stated limit: a session whose mask
does not refuse the class gives such a role. A row of the application's own that
reaches the person without naming them is not a limit: the generated write refuses the
real person a pass given to a team they are in
(`identity.RefuseImpersonatorAsSetSubject`) and their own team moved to another
division (`identity.RefuseImpersonatorAsReadThroughSubject`), under every mask. The
generated code names the column and the set, and the library asks the set by its own
declaration, so the rule and the policy agree on who is in it. The generator also gives
the class to the create, the change and the delete of every record that decides what a
person may do (one that carries `@subjectValue` or `@subjectSet`) and to the change of
a record such a value is read through, so no application lists those records by hand,
and a kind that is to keep one of them names it as an exception to the class
(`accesstypes.RefuseClassExcept`): the session then writes the record for other people,
and never for the real person, a set they are in, or the row their own standing is read
through. To let someone act as a person with no mask is to give them what the person
holds, and an application closes the limit through its mask.
A row removed with its parent, which may name the person, is a limit of its own: the
database removes a pass with the place it opens, no write of the pass is made, and so
neither the rule nor a mask that refuses the writes of passes is asked. An application
closes it under the mask by refusing each request that removes a row above the pass:
the parent's delete (`accesstypes.RefusePermission(accesstypes.Delete, parent)`), the
delete of each record further up while the cascade goes on (the grandparent's delete
removes the parent and the pass with it), and an action that removes one of them in its
own body, by name (`accesstypes.RefuseAction`). Where a foreign key changes the row
instead of removing it (`ON DELETE SET NULL` or `SET DEFAULT`, and `ON UPDATE CASCADE`,
which follows a change of the parent's key; PostgreSQL has them), the same refusals and
the refusal of the parent's update close it. A column that records who
made a row, which a grant compares to `subject`, is another: the row names a person and
decides what they may do, and the rule does not read the column. While the application
stamps the column from the session and people cannot write it, no write moves a row
onto the real person, and a row made acting as a person is stamped with that person. A
row made in a session that acts as a role is the stated case: a role is not anyone, so
the row is stamped with the person who started the session, and what the grants give
the row's author is theirs after the session too, for a row the role's powers made. An
application that does not want that refuses the record's create in the role kind's mask
(`accesstypes.RefusePermission(accesstypes.Create, record)`), or stamps the column with
nobody in such a session. Where people can write the column, the application refuses
the record's writes under the mask.
One limit no class closes: the rule knows the real person by the account they signed in
with, so a second account of their own is someone else's to it, and a person who acts
as an administrator can enable that account again, or disable or delete it, as the
administrator can. An application that must hold this refuses the three actions by name
in the kind's mask (`accesstypes.RefuseAction`).
An application that lets people act as a person and
does not mean them to change who may do what refuses both classes, as the declaration
above does. The class `access` holds the writes of its own records that decide what a
person may do, so the kind names none of them; a record the kind is to keep is named
as an exception to the class (`accesstypes.RefuseClassExcept(class,
accesstypes.ExceptWrites(record)...)`), and the session then writes it for everyone
but the real person, the sets they are in and the row their own standing is read
through. The kind refuses by name the delete of a record such rows are removed with
(`accesstypes.RefusePermission(accesstypes.Delete, parent)`): the rule holds the real
person alone, and the mask is what keeps such a session from changing anyone else's.

A masked check is refused when the mask does not keep its kind, refuses one of its
resource's classes, or refuses the permission on the resource by name. The refusal comes
before any policy is read and is logged as the mask's, naming the mask, the person the
session acts as, the resource and the reason:

```
impersonation: Execute denied by mask (*;-class:credential) for f0000000-0000-4000-8000-000000000002 on SendStaffUserPasswordResetLink: the mask refuses the class credential
```

The permission digest leaves the same things out, so no page offers them
(`resource.Masked`, which `resource.SessionPermissions` applies for every request).

What a refused class keeps (`RefuseClassExcept`) is left to policy like anything the
mask does not refuse, and stays in the digest, so the pages offer it. An excepted
record is kept with its fields, and an excepted action by its name. An excepted field
is kept to a write that sets that field and no other: an armed write tells the check
every field it sets, so the update of the excepted field alone goes through, and the
digest keeps the field and its record's permission while it drops the record's other
fields. A write that sets any other field is refused the record, and so is a check
that names no fields: a hand-written check of the record's permission, and a
patch-or-add, which is asked for its grant before its fields are read.

A refusal holds wherever the permission is asked: the generated routes, a method's
handler, and a write a method's body arms against the caller (`Enforce`). It does not
reach inside a method that is kept: a body's writes run trusted unless the body arms
them, so a method whose body writes a record trusted, or arms the write against a
role's permissions (`Caller.As`), still writes it in a session whose mask refuses the
record's writes. Such a method is refused by its own name (`RefuseAction`) or by a
class it carries, which is what a class is for.

The mask is kept on the impersonation record in its text form, in the table's one
`Mask` column on both databases: the kinds it keeps, sorted and joined with commas (`*`
for every kind), then each refusal after a semicolon, `-class:<class>` or
`-on:<Permission>:<Resource>`, and each exception of a class after its class, as
`+class:<class>:<Permission>:<Resource>`. A read-only mask is `List,Read`; everything except the
credential actions is `*;-class:credential`, and everything except those and the actions
that change who may do what is `*;-class:access;-class:credential`; an unrestricted
session's column is NULL. The record so keeps what the session was given out of a
class, for as long as the record is kept.
A column that does not read as a mask holds the session to nothing: a mask is never
read wider than it was written, and each time the record is read the log says which
session's column does not read and why (`impersonation: the mask stored for session
... does not read, so the session is held to nothing`). For people the mask is said in words
(`PermissionMask.Words()`): "reading only", "everything except credential actions",
"everything except the action ScrapShip", and "everything except writing Invoices" for
a record refused its three writes; what a refused class keeps is said beside it,
"everything except access actions (still allowed: writing Passes)". The session endpoint carries them
(`impersonation.maskWords`, beside `mask`, `refusedClasses`, `refused` and `excepted`,
which lists what each refused class keeps), and a list
of impersonated sessions shows them for each: `ActiveImpersonations` returns each
record with its mask, and the generated user management's list of the impersonation
records (`<Auth>SessionImpersonations`) reads the column as words itself, through the
type of its `mask` field (`accesstypes.NullMaskWords`: "everything" for NULL, and
"nothing (the stored mask does not read)" for a column that is no mask's text), so the
list never shows the text form.

#### Identity by session type

A role principal's session is always the actor's: nobody's identity is borrowed. What a
**user** principal resolves to depends on what the session type knows about users:

| Session type | User principal becomes | User ID | Refused |
| --- | --- | --- | --- |
| Auth | The `SessionUsers` record's username; the session belongs to that user and records an `impersonation` auth event naming the actor | The record's ID | A missing or disabled user |
| Preauth | The name as given | Zero | — (trust-the-caller, as `Login` is) |

On an `Auth` the user principal is the id of the person's account, and the record keeps
it in the one text every membership is held under, whatever form of the id the request
gave.

A name and an id are kept apart. The permission store names an account by its id and a
session with no account (a `Preauth` session, an actor from another application) by its
name, so a name must never read as an id where the two are asked of one store. An `Auth`
refuses a foreign actor whose name parses as an id. `Preauth` trusts its caller and takes
any name, so an application that runs a `Preauth` session type beside an `Auth` over one
permission store keeps the rule itself: the names it hands `Preauth` must not read as
ids.

On an `Auth` session an impersonation carries no identity of its own: no identity is
linked, no roles are synchronized, and the custom session data resolver receives
`ReasonImpersonation` with no claims. A provider-initiated logout reaches it through the
session it was started from: `azure.Method.FrontChannelLogout()` ends the impersonations
started from a session under the `sid` it names (their records end `Logout`) and no
other (see "Every session").

#### Local and foreign actors

The record's `ActorRealm` says where the actor was authenticated, and it decides what
the actor's name means in this application's session table. It is empty for a request
`Request` made, and `ImpersonationActor.Realm` for one `RequestFor` made:

| | Local actor (`ActorRealm` empty) | Foreign actor (`ActorRealm` set) |
| --- | --- | --- |
| Who | A user of this application, logged in here | Authenticated by another application or IdP |
| The actor's own session (the record's `SourceSessionID`) | Required and verified: the actor's own live, non-impersonated session here, carrying their username. `Request` names the session the call arrives on; a server-side caller names it in `ImpersonationActor.SessionID` | Optional (`ImpersonationActor.SessionID`); the session in the source application, for correlation |
| The actor's account | The record carries its id (`Impersonation.ActorID`, the table's `ActorUserId` column), read from the source session: the permission store names the actor by it (`ActorUser`), so the actor's own roles are found under it and a row condition's `subject` binds to it in a role principal's session | None here: `ActorID` is null, the permission store names the actor by the name the record carries, and the actor holds nothing in this application |
| The actor's name | The username of the source session | Any name that does not read as the id of an account: an Auth refuses a foreign actor whose name parses as one (400, in every form the id parser reads), since a text that reads as an id names an account here |
| A role principal's session | The actor's own session, narrowed to the role | A session under a borrowed name |
| Kept alive | The source session's activity is refreshed with the impersonated session's, so `EndImpersonation` can return to it | — |
| Deactivating or renaming the user named `Actor` | Includes the role session and every impersonation the actor holds (see *One name, one user*) | Never touches the role session |

Minting an impersonated session in the same application the actor is logged into
replaces the browser's session cookie — which is why the source session is verified,
kept alive, and returned to.

### What the session carries

Every validated request exposes the record without branching on whether the session is
impersonated:

```go
principal := sessioninfo.PrincipalFromCtx(ctx) // UserPrincipal(the id of the account) for an ordinary session
actor     := sessioninfo.ActorFromCtx(ctx)     // == Username unless impersonated: what people read
who       := sessioninfo.ActorUserFromCtx(ctx) // the same person as the permission store names them: the id of their account
mask      := sessioninfo.MaskFromCtx(ctx)      // unrestricted unless impersonated
imp, ok   := sessioninfo.ImpersonationFromCtx(ctx)
```

A permission check honors the mask by asking it before asking policy, and picks the
check by the principal's kind (`Role()` → `CheckRoleResources`, otherwise
`CheckUserResources`). The shared implementation is `resource.SessionPermissions`, which
the generated handlers go through: it asks the mask of each resource
(`PermissionMask.Refuses(perm, resource, classes...)`, with the classes the generated
collection gives the action), so a class or a named action refused inside a kept kind is
refused. `PermissionMask.Allows(perm)` answers for the kind alone. Forgetting the mask
fails *open*, so a check written by hand goes through the shared implementation, never
around it.

### Choosing the principal

By default a session's principal is its user, or the impersonation record's principal,
and for almost every application that is the end of the story: put role membership in
the permission store, check with `ForUser`, and a role change is enforced on the user's
next request. `WithPrincipalResolver` exists for the narrow case where an ordinary
session's subject genuinely is not a user. The resolver runs inside the validation of
an ordinary session, with the session and its custom data in the context, and returns
the principal `PrincipalFromCtx` reports for the request. A session that acts as
someone else never asks it: whom such a session acts as is what its kind declares and
its record names, and no code of the application's decides it.

**The resolver chooses *which subject*, never *which grants*.** Grants always come from
the permission store at check time. The moment a resolver reads role membership from
somewhere that was copied at login, the application has built a cache of its
authorization model with a lifetime of one session, and no amount of live grant checking
repairs that.

#### Intended uses

- **Machine and API-key sessions.** The caller is a service, not a person; the subject is
  the service's role, decided from the credential the session was established with.
- **Membership that lives outside the permission store and is read live.** The resolver
  asks the external system on each request (or through a cache with a deliberate,
  short TTL that the application owns and documents). Staleness is bounded by that TTL,
  not by the session.

```go
// A service session acts as the role bound to its API key. The key-to-role binding is
// read on every request, so revoking or re-binding the key takes effect immediately.
auth, err := session.NewPreauth[APIKeySession](storage, cookieKey,
    session.WithPrincipalResolver(func(ctx context.Context) (accesstypes.Principal, error) {
        data, err := sessioninfo.CustomDataFromCtx[*APIKeySession](ctx)
        if err != nil {
            return accesstypes.Principal{}, err
        }
        role, err := keys.RoleForKey(ctx, data.KeyID) // live lookup, not a stored copy
        if err != nil {
            return accesstypes.Principal{}, err
        }

        return accesstypes.RolePrincipal(role), nil
    }),
)
```

#### Not for this

- **A role snapshotted into custom session data at login.** `RolePrincipal(data.RoleID)`
  where `RoleID` was resolved when the session was created is the anti-pattern this
  section exists to name. The user keeps the old role until they log in again; an
  administrator's change is silently ignored for the life of every open session. The fix
  is not a resolver — it is moving membership into the permission store
  (`AddUserRoles` / `DeleteUserRoles`) and letting the default user principal do its job.
- **"The user picked a role for this session."** That is a deliberate, time-capped act
  by an actor, which is what the impersonation record is for: establish a role-principal
  impersonation with the user as actor and you get evidence, a hard cap, listing and
  revocation. A resolver gives you none of those.
- **Avoiding a permission-store write.** If the only reason to reach for the resolver is
  that membership is stored somewhere else and nobody wants to move it, move it.

#### Mechanics

- The choice is made per request at validation and never stored; nothing changes in the
  schema and no impersonation record is written.
- The resolver runs for ordinary sessions alone. A session that acts as someone else,
  as a person or as a role, skips it, and `PrincipalFromCtx` answers the principal its
  record names. So a session that acts as a person whose own sessions the resolver
  gives another subject acts as the person: under the grants the permission store
  holds for their account, not under the subject the resolver would have chosen. An
  application that wants a session to act as that subject declares a kind that acts
  as the role.
- Returning the zero `Principal` keeps the default. An error fails the request as a
  server error, not an unauthorized one: the session is valid; the application could not
  decide what it acts as.
- When the resolver changes the principal, the request's log entry and trace span carry
  `principal.kind` and `principal` (`sessioninfo.AttrPrincipalKind`,
  `sessioninfo.AttrPrincipal`); unchanged requests carry nothing extra.

### Evidence

Everything an impersonated session touches names the actor and the principal:

| Evidence | Where |
| --- | --- |
| The record (actor, realm, source session, principal, mask, reason, started/expires/ended, end reason) | The impersonation table, durable |
| `impersonation.actor`, `.actor_realm`, `.principal_kind`, `.principal`, `.mask`, `.session_id`, `.source_session_id` | The request-level log entry and every line logged within the request (constants in `sessioninfo`) |
| `principal.kind`, `principal` — when a `WithPrincipalResolver` changed the request's subject | The request-level log entry and every line logged within the request |
| `Started` / `Ended` / `IdentityOperationBlocked` / `WriteBlocked` events | Structured log lines, span events (`impersonation.Started`, …) on the current trace span, plus the `WithImpersonationAudit` hook |
| `enduser.id` (the session's username), the same `impersonation.*` attributes, and `principal.*` when a resolver changed the subject | The request's server span from the `ValidateSession` middleware (the caller's current span from `ValidateSessionAPI`), impersonation attributes set before any refusal so a refused request's trace still names the actor; the establishing call's own span on the source side |
| The establishing call's own log entry | The source application's request log |
| `impersonation` object in the `Authenticated()` response: the actor, the principal, the mask (`mask`, the kinds it keeps; `refusedClasses` and `refused`, what it refuses inside them; `maskWords`, the whole of it as a person would say it), the reason and the hard cap | For the frontend to banner the session, say what it may do, and render read-only affordances |

### Lifecycle and guards

- **Hard cap.** `ExpiresAt` is fixed at establishment (`WithImpersonationTimeout`,
  default one hour, shortened per call by `MaxDuration`); idle renewal never extends past
  it. The idle session timeout applies independently.
- **Ending.** Logout, the hard cap, idle expiry, `EndImpersonation`, and
  `DestroyUserSessions` all end the
  record with a reason (`Logout`, `Expired`, `Released`, `Revoked`); an `Auth` session's
  Azure front-channel logout ends the records of the impersonations it reaches `Logout`.
  `API().DestroyImpersonatedSessions(ctx, actor)`, on every
  session type, is the offboarding and incident tool: it expires every live impersonated
  session an actor established.
- **Returning to self.** `EndImpersonation` (a handler on every session type, routed
  inside the validated group; `API().EndImpersonation(ctx, w)` beside it) ends the
  impersonated session `Released` and, for a local actor whose own session is still live,
  not itself impersonated, and still theirs, gives the response that session's cookies:
  the actor is back in their own session without logging in. The body says whether that
  happened (`{"restored": true}`); when it did not, send the actor to login. The hard cap
  is a hard boundary — a session past it is refused at validation before any handler runs,
  so a frontend offers *return to self* before `expiresAt` from the `Authenticated()`
  response, or warns and calls it as the cap nears. The route is a POST, so it sits
  outside any group carrying `EnforceReadOnlyMask` (see *Read-only middleware*).

  ```go
  r.Group(func(r chi.Router) {
      r.Use(auth.ValidateSession, auth.ValidateXSRFToken)
      r.Post("/impersonation/end", auth.EndImpersonation())
  })
  ```
- **Validation (`Auth`).** A user principal's record is looked up like any
  session's — a disabled impersonated user ends the session. A foreign actor's role
  principal has no local user: no record is looked up and `UserFromCtx` carries the
  actor's username with the zero ID, so self-referential checks (cannot delete yourself)
  go inert, correctly. A local actor's role principal is their own session, so their
  record is looked up, by the username the session carries — a disabled actor's role
  session ends with their others.
  Preauth validates an impersonated session exactly as any other.
- **Identity operations.** A person's own account is theirs alone: every handler of the
  library's that writes to the signed-in person's account refuses a session that views
  as them, whatever its mask, with 403 and an `IdentityOperationBlocked` event, before it
  reads its body. The person reads "You are acting as someone else, and this session
  cannot change their account." (in a session that acts as a role, "cannot do this."):
  the handler's name is the event's and the server's log's, never the sentence's. They are
  `ChangeUserPassword` and `RemoveUserPassword` (the password
  method's), `ChangeName`, `ChangeLook`, `ForgetBrowser`, `Email().Add()` and
  `Email().Verify()` (the address the code of their second factor is mailed to),
  `ApplicationTokens().Create()` and `ApplicationTokens().Revoke()`, `Device().Approve()`
  and `Device().Deny()` (a token and an approved device act as the person for as long as
  they live) and `Invitation().Accept()` (which gives the person roles). The routes that
  only read (`ApplicationTokens().List()`, `Device().Lookup()`) answer a view, and
  `Logout` and `EndImpersonation` end it. The generated router registers the routes it
  mounts behind `ValidateSession` (the invitation's, the address's, the tokens' and the
  device's) in front of the application's hook, so what the hook asks of a request, a
  tenant or a role say, gates the application's generated routes and never a person's
  own account; and it mounts the read-only mask (`EnforceReadOnlyMask`) in front of
  them itself, as it does in front of the generated routes behind the hook. The
  password's, the name's, the look's and the remembered browser's validate the session
  themselves, in front of the session check, and the handler's own refusal is what
  answers a view there. On `Auth` the user operations are `AuthAPI` methods behind the
  application's own handlers, which guard them.
- **One name, one user.** A role principal's session carries the actor's own username,
  in the same session table as the sessions of the application's users, and belongs to
  no account. On an `Auth` what is a user's is found by the id of their account:
  `DestroyUserSessions(ctx, userID)` ends the user's own sessions under any username,
  the user-principal impersonations of them and every impersonation they hold as a
  local actor, their role sessions among them, ending the records `Revoked`. A local
  actor's role session is reached through the actor's username: validation loads the
  actor's user by the username the session carries, and the records a local actor
  holds are found by the actor's username. So `ChangeSessionUserUsername` renames the
  user's live sessions, their role sessions with them, and moves the live records they
  hold as a local actor to the new name. For a *foreign* actor the name is borrowed,
  so an `Auth` refuses a foreign actor's role principal when that name is already a
  `SessionUsers` username (403: the actor logs in as that user, or impersonates it as
  a user principal), and neither operation touches a session carrying a live foreign
  role-principal record: a user created later under that name cannot reach the actor's
  session either. `Preauth` keeps no accounts, so there a name is all a session has,
  and there is no user for a foreign actor's name to collide with:
  `PreauthAPI.DestroyAllUserSessions(ctx, username)` expires every session under the
  name but a foreign actor's role session, and every impersonation the name holds as a
  local actor under other names, ending the records `Revoked`.
  This is the store-level complement of the authorization rule: under a role principal the
  subject is the role, and application policy must not key row conditions on the session
  username.
- **Listing.** `API().ActiveImpersonations(ctx, q)`, on every session type, lists the
  impersonated sessions that are live right now, newest first — the admin surface's view
  of who is acting as whom. *Active* means: the record has not ended, the hard cap has
  not passed, the session row is not expired, and the session has seen activity within
  the idle session timeout. `q` (`*session.ImpersonationQuery`) narrows by `Actor`
  and/or `Principal`; `nil` lists everything.

  `API().DestroyImpersonatedSession(ctx, sessionID)` is the action on a row: it expires
  that session and ends its record `Revoked` in one transaction, so the next request on it
  is refused. Who may list or revoke is the application's guard.

  ```go
  imps, err := auth.API().ActiveImpersonations(ctx, &session.ImpersonationQuery{Actor: "alice@example.com"})
  err = auth.API().DestroyImpersonatedSession(ctx, imps[0].SessionID)
  ```
- **Read-only middleware.** `EnforceReadOnlyMask` (on every session type, after
  `ValidateSession`) refuses non-safe requests — anything but GET, HEAD, OPTIONS and
  TRACE — from a session whose mask is *read-only*: it names the kinds it keeps, and
  keeps nothing beyond `List` and `Read` (`PermissionMask.ReadOnly()`; a mask that keeps
  every kind is not read-only, whatever it refuses inside them). The refusal is 403, "You are acting as someone else, and
  this session cannot change anything.", with a `WriteBlocked` event naming the
  method and path. It is a backstop that keeps a read-only session away from
  every mutating handler whether or not that handler consults the mask; it does not
  replace honoring the mask in permission checks (`Execute` reaches handlers by POST, and
  a mask including `Execute` is not read-only). The generated router mounts it by
  itself in front of an outlet's generated routes, wherever the application's hook
  registers them, and in front of the routes a signed-in person calls on their own
  account; a route an application registers by hand, through the hook or on a router
  of its own, is under it only where the application mounts it, as below. Ending a
  session is never a safe method
  either — `EndImpersonation` is a POST, `Logout` a POST or DELETE — so the
  `EndImpersonation` route (and `Logout`) must be mounted outside the group that carries
  the backstop, in a validated group of their own, or a read-only view-as session could
  never end itself.

  ```go
  // Ending a session is never a safe method: these stay out from under the backstop.
  r.Group(func(r chi.Router) {
      r.Use(auth.ValidateSession, auth.ValidateXSRFToken)
      r.Post("/impersonation/end", auth.EndImpersonation())
      r.Delete("/session", auth.Logout())
  })
  // Everything else a read-only session may reach is under it.
  r.Group(func(r chi.Router) {
      r.Use(auth.ValidateSession, auth.ValidateXSRFToken, auth.EnforceReadOnlyMask)
      // ...
  })
  ```

##### Created and maintained by the CCC team.
