# Storing Data Alongside Sessions: Guidance and Anti-Patterns

> **Status: ADOPTED** (2026-08-09, committed with the custom-session-data series).
> The taxonomy, patterns (P-n), and anti-patterns (AP-n) are the team's guidance —
> argue about them by name. Open questions (OQ-n) at the end carry their current
> direction or resolution; several are now implemented and marked as such.

## The two kinds of data

Everything an app wants to store "with the session" is one of two things, and
almost every design mistake in this space comes from confusing them:

**Session data** is ephemeral by nature. It describes *this login*, not the
person: the currently selected tenant, an impersonation context, an MFA
elevation flag, a claims snapshot taken at login. It is born when the session
is created, dies when the session dies, and is **reset when a session is
regenerated** (e.g., on password change). If losing it on regeneration feels
like data loss, it was never session data.

**User data** is durable. It describes the person across logins: provisioning
state, profile fields, preferences, the immutable identity key from an external
IdP, anything other records reference. It must survive logout, session
expiration, regeneration, and renames.

**The rule:** the custom session data table holds session data only. User data
belongs in an app-owned user table, keyed by a stable identifier. The
session-creation resolver is the *hook* for writing both (it runs inside the
session-insert transaction with the claims/user in hand), but the two kinds of
data have different *destinations*.

## What the library gives you, per auth mode

| | Auth (every method) | Preauth |
|---|---|---|
| Library user table | `SessionUsers` (UUID PK, username unique); external identities in `SessionIdentities`, keyed `(method, connection, subject)` | none |
| Stable identity anchor | `SessionUsers.Id` (UUID), whatever method signed the person in; every session carries it as `SessionData.UserID` | caller-defined |
| Username mutability | mutable, tracked (`ChangeSessionUserUsername` updates in place) | caller-defined |
| Custom session data | resolver typed to the store's transaction (`NewCustomSessionData`), fed `req.UserID`, `req.User`, `req.Identity` and `req.Claims`; per-call on `StartAuthenticatedSession` | caller-supplied per-call (`Preauth.API().Login()`) |
| Custom user data | per-call on `CreateSessionUser`, or written by a custom account rule's `signin.Provision` hook for a user it provisions; RMW `UpdateCustomUserData`; FK → `SessionUsers.Id` | unsupported (no user record) |
| Session regeneration | on password change (custom session data re-resolved with `ReasonRegeneration`; user data untouched) | n/a |

**Auth sessions** (`NewAuth`, see the README's "Auth sessions" section) use the
`SessionUsers` user for every sign-in method. The stable anchor is `SessionUsers.Id`,
which every session carries as `SessionData.UserID` and every resolver receives as
`req.UserID` once the user is resolved; external identities are rows of
`SessionIdentities` that point at it, so durable user data keys off the user, never
off an identity's subject or email. Custom session data is resolved with `ReasonLogin`,
`ReasonIdentityLinked` (a confirmed link), `ReasonStepUp` (an MFA completion),
`ReasonExternalAuth` (`StartAuthenticatedSession`) or `ReasonRegeneration`, with
`req.Identity` and `req.Claims` for the external methods, and with `ReasonPreauth` for
a preauth stepping-stone row, which belongs to no user. A pending identity (a sign-in
waiting for its confirmation or MFA) is a row of `SessionPendingSignIns`, not a
session, and resolves no custom data until it completes. Custom user data is written
per call on `CreateSessionUser`, or in a custom account rule's `signin.Provision` hook,
inside the transaction that creates the user.

## Patterns

### P-1: Extend the user record off the stable key, never the username

- **Auth:** key app user-data tables by `SessionUsers.Id`, whatever method signed
  the person in. An identity's subject or email is never a key: an identity is a link
  to the user, and a user may hold several. A username change leaves the id as it
  is, so everything keyed by it holds. For small extensions, the library ships
  this pattern: custom user data (FK → `SessionUsers.Id`, per-call on
  `CreateSessionUser`, RMW updates).
- **Preauth:** the library never sees claims or credentials, so the caller owns
  identity. Whatever string is passed as `username` becomes the identity for
  the session's lifetime — pass a stable identifier, or maintain the mapping
  yourself. Durable custom user data is deliberately unsupported here: keying
  it on a caller-supplied username string is AP-1.

#### OIDC key choice: `oid` alone vs `(tid, oid)` composite

What the claims mean:

- **`oid`** is the GUID of the user object in the Entra ID directory (tenant)
  that issued the token. It is immutable for the life of that object and
  unique **within that tenant**. It is the same value the Graph API reports
  as the user's object id, so it correlates across all of your apps and with
  directory tooling. (Prefer it over `sub`, which is pairwise — unique per
  user *per application* — and therefore useless for correlating anything.)
- **`tid`** is the GUID of the tenant the token was issued for.

**Single-tenant app** (all users from one directory): `oid` alone is a
sufficient primary key. `tid` is a constant, so including it in the key adds
nothing. Cheap insurance if multi-tenancy is ever plausible: store `tid` as a
plain column from day one — promoting it into the key later is then a schema
change, not a data backfill.

**Multi-tenant app** (sign-ins from multiple directories): the primary key
must be the composite `(tid, oid)`. Two reasons:

1. **Uniqueness is only guaranteed per tenant.** Microsoft's own guidance is
   that a cross-tenant user identifier is the `tid` + `oid` pair; treating
   `oid` as globally unique is relying on an accident of GUID generation, not
   a contract.
2. **The semantics are per-tenant anyway.** The same human reaching your app
   through two tenants (home tenant, or as a guest in another) presents a
   *different* `oid` per tenant. `(tid, oid)` makes each tenant-scoped
   identity its own row — which is almost always what a multi-tenant app
   wants. If the product genuinely needs "one human across tenants," that is
   a user-linking feature the app builds on top, not something any claim
   gives you.

### P-2: JIT-provision user data inside the account rule's transaction

**Library-managed:** on `Auth`, a linked identity reaches its user with no lookup
by the application, and an unlinked one goes through the registration's account rules:
the standard rule provisions the user alone, and a custom rule's
`signin.Provision` creates the user and runs `onProvisioned` in the same
transaction, where the application writes its own rows for it. An `onProvisioned`
failure rolls that transaction back: no user, no link, and the sign-in fails.

For app-owned tables, the same steps hand-rolled in the session-creation
resolver (which runs inside the same database transaction as the session
insert):

1. Look up the user row by stable key (`SessionUsers.Id`, which the resolver
   receives as `req.UserID`).
2. Not found → provision the row.
3. Found with a different username → **it's a rename**; update the username
   attribute in place. Continuity is preserved: no orphaned record, no cleanup
   needed, no data inherited by a future holder of the old name.
4. Found → touch last-login (or whatever bookkeeping the app wants).

A resolver failure aborts the login atomically — no session exists without its
provisioning having succeeded.

### P-3: Carry the durable key as session data

Return the stable user key (the user-table PK) from the resolver
as a custom session data column. Every subsequent request can then reach the
durable user record straight from the request context — no per-request
username→key lookup, and no temptation to key anything else by username.
The library's own key needs no copy: the resolver receives it as `req.UserID`,
and an `Auth` session that belongs to a user carries it as `SessionData.UserID`.

### P-4: Namespace usernames when multiple authorities mint them

When more than one system creates sessions in the same session-user space —
imagine a back-office app, *Dispatch*, whose support staff can open
impersonation sessions in a customer-facing app, *Waypoint* — prefix the
minted username with an authority marker that is invalid in real usernames
(e.g., `support:` — `:` cannot appear in email-format usernames). This makes
collision with a real user impossible and provenance visible in logs.

## Anti-patterns

### AP-1: Username as the primary key for durable user data (any mode, worst in OIDC)

Two failure modes, both silent:

- **Rename:** the user self-provisions under the new name; their accumulated
  record is orphaned under the old one and eventually swept by stale-user
  cleanup. That is data loss for a real person, not clutter.
- **Recycled username:** a new hire receives a departed employee's UPN before
  cleanup fires and *inherits their record* — a correctness and privacy
  failure.

On an `Auth` this is an unforced error: every user has a UUID
(`SessionUsers.Id`), whatever method signed the person in; use it. Note
what stays fine: username as *session plumbing* (session rows, log
attribution) is unaffected — sessions are per-login.

### AP-2: User data in the custom session data table

Storing profile fields, provisioning state, or the oid *only* in the session
extension table means: duplicated per login, gone on logout/expiry, reset on
regeneration, and unreachable when the user has no active session. The
regeneration semantics (see below) are not a bug to work around here — they are
the signal that the data is in the wrong place. Snapshotting a *copy* of a
claim into session data for convenience (P-3) is fine; making it the system of
record is not.

### AP-3: Populating session data outside the creation transaction

Creating a session and then back-filling its custom data in a second call
(`StartAuthenticatedSession` + `UpdateCustomSessionData`) leaves a window where
the auth cookie is live but the session's data row is absent — and if the
second call fails, a broken-but-authenticated session escapes. The atomic path
exists: pass the data at creation (per-call data on `StartAuthenticatedSession`
and `Preauth.API().Login()`, or the configured resolver), written in the same
transaction as the session insert. `UpdateCustomSessionData` is for *changing*
session state mid-flight, not for initial population.

### AP-4: Treating `preferred_username` differences as different people

Any OIDC flow that compares usernames to decide "same user?" is wrong in both
directions (renames split one person in two; recycling merges two people into
one). Identity comparison is `oid` (or `(tid, oid)`) — always.

## Regeneration semantics

On password change, every session for the user is destroyed and the caller
gets a regenerated session. Custom session data is **resolved fresh** by the
configured resolver — which receives `Reason: ReasonRegeneration` so apps can
distinguish the trigger; values previously set via `UpdateCustomSessionData` do
not carry over, and if no resolver is configured the new session has no
custom-data row (decoders must tolerate all-NULL — see the LEFT JOIN
contract). This is correct for session data by definition; if it hurts,
see AP-2.

## Open questions

- **OQ-1:** ~~Should the library ever offer an optional, library-managed user
  table for OIDC?~~ **Resolved — implemented:** on an `Auth` every external
  identity is linked to a `SessionUsers` user through `SessionIdentities`,
  keyed `(method, connection, subject)`, with the username as a mutable
  attribute of the user. Custom user data attaches to the user
  (FK → `SessionUsers.Id`). See the "Auth sessions" and "Custom user data"
  sections of the README.
- **OQ-2:** ~~Should the resolver be regeneration-aware?~~ **Resolved —
  implemented:** the resolver receives `NewSessionRequest.Reason`
  (`Login` / `ExternalAuth` / `Regeneration` / `Preauth`); default behavior is
  reset/re-resolve with no per-app code. Passing the prior session's custom
  data was deferred — adding a field to the request struct later is additive
  and non-breaking.
- **OQ-3:** ~~Preauth has no custom session data support.~~ **Resolved —
  implemented:** both session types share the mechanism. `Auth`: a resolver,
  fed the user and, for an external method, the identity and its raw verified
  claims, plus per-call data on `StartAuthenticatedSession`; Preauth:
  caller-supplied per-call data on `Preauth.API().Login()`, atomic with the
  insert.
- **OQ-4:** ~~`StartAuthenticatedSession` vs `Preauth.Login` — do we need
  both?~~ **Resolved — keep both; boundary documented in godoc:**
  `StartAuthenticatedSession` requires an existing, non-disabled
  `SessionUsers` record and participates fully in custom session data
  (`ReasonExternalAuth`); `Preauth.Login` is trust-the-caller with no user
  record — the stepping-stone (e.g., MFA-pending) session. Each godoc names
  the other as the alternative.
- **OQ-5:** ~~Multi-tenant OIDC: do we take a position on `(tid, oid)`
  composite keys?~~ **Resolved:** the guidance takes a position — see
  "OIDC key choice" under P-1.
