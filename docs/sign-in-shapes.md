# Sign-in shapes

There are eight shapes an application's sign-in can take. Each one says whether the
application has tenants, who gets an account and with what role, where one person can hold
roles, and who manages the account. Each shape can use some sign-in methods and not
others.

This document describes the eight shapes, the choices each one is made of, the commands
that start an application from one, and what `impulse audit` prints for it.

- [The words used here](#the-words-used-here)
- [What a shape is](#what-a-shape-is)
- [At a glance](#at-a-glance)
- [How wide a Google or Microsoft sign-in can be opened](#how-wide-a-google-or-microsoft-sign-in-can-be-opened)
- [Which sign-in methods each shape can use](#which-sign-in-methods-each-shape-can-use)
- [Shapes and the impulse command](#shapes-and-the-impulse-command)
- [In every shape](#in-every-shape): the email address on an account, the way back in, and
  the mail an account gets
- [Without tenants](#without-tenants): shapes [1](#1-invite-only),
  [2](#2-directory-members), [3](#3-directory-roles) and [4](#4-open-sign-up)
- [In every shape with tenants](#in-every-shape-with-tenants)
- [With tenants, one tenant per person](#with-tenants-one-tenant-per-person): shapes
  [5](#5-tenant-owned-users-invite-only) and [6](#6-tenant-owned-users-by-directory)
- [With tenants, several tenants per person](#with-tenants-several-tenants-per-person):
  shapes [7](#7-global-users-invite-only) and [8](#8-global-users-each-tenant-chooses)
- [Outside the eight shapes](#outside-the-eight-shapes)

## The words used here

- **Auth**: one set of users, with the sign-in methods they use and the roles they can
  hold. An auth is a package of the application, `pkg/auth/<auth>`. A new application has
  one, and `impulse add auth` adds another. Each auth has its own users table, so an
  account in one auth is no account in another.
- **User**: a person's account in an auth. A person has one account in an auth, however
  many tenants they are in.
- **Role**: a named set of grants from the auth's role file, `pkg/auth/<auth>/roles.json`.
  A **grant** is one line of that file: it lets the role's holders do one thing, such as
  list the accounts or disable one. A grant can carry a condition, a test of the row it
  is used on. A person holds a role in one of three places: for the whole application, in
  one tenant, or in every tenant. The role file keeps the roles held for the whole
  application apart from the roles held in a tenant. A person holds a role of their own
  or through a **group**: a named set of people that is given roles, kept for the whole
  application or in one tenant.
- **Super Administrator**: the one role the framework defines in every auth. It is held
  for the whole application. Its holders give and take every role, and no tenant's
  choice keeps them out.
- **Tenant**: one organization's own part of the application, with its own data and its
  own people. Each tenant is a row of the application's tenant record, the table that
  lists its tenants.
- **Directory**: the accounts an organization keeps for its own people, which they sign
  in with: a Google Workspace, a Microsoft Entra tenant, or the organization's own
  identity provider. Microsoft calls an organization's directory a tenant. In this
  document "tenant" alone always means the application's, and Microsoft's is always
  written "Microsoft Entra tenant".
- **Invitation**: a link mailed to an email address. Accepting it gives the person the
  roles it carries.
- **Sign-in method**: one way an auth's people sign in: a password, Google, Microsoft, or
  another identity provider. An auth registers each method in code, under a name. The
  command's words for the kinds are `password`, `google`, `azure` (Microsoft) and `oidc`
  (another identity provider).
- **Sign-in identity**: the link between an account and the person's account at Google,
  Microsoft or another provider.
- **Login page** and **Sign-in page**: the login page is where a person signs in. The
  Sign-in page is where an administrator sets who may sign in. There is one Sign-in page
  for each auth (in the skeletons: Sign-in, in the System menu) and, where a tenant can
  have a directory of its own, one for each tenant (in the skeletons: Sign-in settings,
  in the Tenant menu).
- **Outlet**: a URL space of the application. The kind this document uses has its own
  browser application and its own login page, and is bound to one auth. A new application
  has one, named `default`, and `impulse add outlet` adds another on the same host.
- **Settings**: the deployment's environment variables, which the application reads when
  it starts.
- **Skeleton**: one of the starting applications the `impulse` command carries. A new
  application is made from one, with its role file and its pages.

## What a shape is

A shape is the answers to four questions.

1. Are there tenants?
2. Who gets an account, and with what role?
3. Where can one person hold roles?
4. Who manages that person's account?

A shape belongs to an auth. An application with one auth has one shape. An application
with two auths has two, one for each: its staff can be invite only while its members come
through their organization's directory.

No application declares its shape, and nothing stores one. An application makes separate
choices, some in its code and some while it runs. A shape is the name of what they add up
to. The framework's libraries have no notion of a shape: what they refuse is always a
combination of the separate choices.

| The choice | Where it is made |
| --- | --- |
| Tenants, or none | In code: the application has a tenant record, or it has none. |
| Which sign-in methods an auth has, and how wide each one is | In code, where the auth registers the method. |
| Who gives the roles: the application or the directory | In code, on each Google, Microsoft or other provider's method. |
| Whether a person belongs to one tenant or can be in several | In code: the option `identity.WithOneTenantPerPerson()`, described under [one tenant per person](#with-tenants-one-tenant-per-person). |
| Whether a tenant can have a directory of its own | In code: described under [A tenant's directory](#a-tenants-directory). |
| Open to sign-up or invite only, for each sign-in method | While the application runs, by an administrator, on the auth's Sign-in page. |
| Where a tenant can have a directory: whether a tenant takes one, the role its directory's people start with, and whether the tenant is entered through its directory only | While the application runs, by the tenant's administrator, on the tenant's Sign-in page. |

## At a glance

| # | Shape | `--shape` | Tenants | Who gets an account |
| --- | --- | --- | --- | --- |
| 1 | [Invite only](#1-invite-only) | `invite-only` | None | People an administrator invites |
| 2 | [Directory members](#2-directory-members) | `directory-members` | None | Everyone in the organization's directory |
| 3 | [Directory roles](#3-directory-roles) | `directory-roles` | None | Everyone the organization's directory gives a role |
| 4 | [Open sign-up](#4-open-sign-up) | `open-sign-up` | None | Anyone |
| 5 | [Tenant-owned users, invite only](#5-tenant-owned-users-invite-only) | `tenant-owned-users-invite-only` | One per person | People the tenant's administrator invites |
| 6 | [Tenant-owned users, by directory](#6-tenant-owned-users-by-directory) | `tenant-owned-users-by-directory` | One per person | Everyone in the tenant's own directory, and people the tenant invites |
| 7 | [Global users, invite only](#7-global-users-invite-only) | `global-users-invite-only` | Several per person | People any tenant invites |
| 8 | [Global users, each tenant chooses](#8-global-users-each-tenant-chooses) | `global-users-each-tenant-chooses` | Several per person | Everyone in a directory a tenant has taken, and people it invites |

The `--shape` column is the word the `impulse` command takes for the shape. The number
works too (`--shape 6`). Shapes 1 to 4 are taken by `impulse add auth` and
`impulse add method`, shapes 5 to 8 by `impulse new` and `impulse add tenancy`, and
shape 3 by `impulse new` too, as
[Starting from a shape](#starting-from-a-shape-impulse-new-and-impulse-add) describes.
Each shape's heading in this document is its number and its name, which is how
`impulse audit` names it.

The Tenants column also answers where one person can hold roles. With no tenants it is
for the whole application alone. With one tenant per person it is in one tenant, the
application's own staff apart. With several per person it is in as many tenants as give
the person a role.

## How wide a Google or Microsoft sign-in can be opened

A Google or Microsoft method is registered at one of four widths. The width is whom the
provider could sign in, and so whom the method admits once an administrator opens it to
sign-up. `impulse add method` sets the width with the flag `--slot`. The table lists the
widths from the narrowest to the widest.

| Width | Whom it admits | `--slot` for Microsoft | `--slot` for Google |
| --- | --- | --- | --- |
| **One organization** | Only people from one Google Workspace or one Microsoft Entra tenant. | `one` | `one` |
| **A list of organizations** | Only people from the organizations the application names. | `listed` | `listed` |
| **Any organization** | Anyone with a work account. | `any` | `any-workspace` |
| **Personal accounts too** | Also gmail.com, outlook.com and the like. | `any-personal` | `any` |

The same word is not the same width for both: `--slot any` is any organization for
Microsoft, and personal accounts too for Google.

Without `--slot` a method is one organization's, unless `--shape open-sign-up` makes it
the widest. `impulse add auth` has no `--slot`: a method it adds is one organization's,
or the widest with `--shape open-sign-up`, and `impulse add method` adds a method of any
other width.

The width is written in the method's own file, `pkg/auth/<auth>/<method>.go`. The
organizations it names come from settings:

| Width | Setting for Microsoft | Setting for Google |
| --- | --- | --- |
| One organization | `APP_<AUTH>_<METHOD>_TENANT_ID`: the Microsoft Entra tenant's id | `APP_<AUTH>_<METHOD>_HOSTED_DOMAIN`: the Workspace's domain |
| A list of organizations | `APP_<AUTH>_<METHOD>_TENANTS`: `<name>=<tenant id>`, separated by commas | `APP_<AUTH>_<METHOD>_WORKSPACES`: `<name>=<domain>`, separated by commas |

`<AUTH>` is the auth's name and `<METHOD>` the name the method is registered under, both
in capitals, with a hyphen written as an underscore. In a list, `<name>` is the
application's own name for the organization, which every sign-in identity made through
it carries. A Google Workspace with several domains joins them with `+`
(`<name>=<domain>+<domain>`). A Workspace's name is not changed once its people have
signed in: their sign-ins would no longer match their accounts.

No page of the running application changes a width, the one organization or the list.
The width is code, and a setting is read when the application starts, so each changes
only by deploying the application again.

`--slot` has three more values, and each admits personal accounts: `personal` (Microsoft
personal accounts alone), `one-personal` (one Microsoft Entra tenant and personal
accounts) and `mixed` (any Google account, with one Google Workspace named so that its
sign-ins are told apart). The last two name their one organization in the same setting
as the one-organization width.
Anyone can have a personal account, so the framework treats each of the three as a method
anyone can use, and the tables below mark them as the widest width.

An application can also use another identity provider, such as Okta or Keycloak. That is
always one organization's. The method's kind is `oidc`: it names one provider by its
issuer, in the setting `APP_<AUTH>_<METHOD>_ISSUER`, and takes no `--slot`. The password
method admits anyone with an email address.

Every Google, Microsoft or other provider's method also needs the client id and secret
the provider gave the application. Those settings are listed with `add method` in
[cmd/impulse/README.md](../cmd/impulse/README.md).

An administrator opens a method to sign-up for the people it admits at that moment, and
for no others. After a deployment that changes whom the method admits (its width, its
organization or its list), the method is invite only again until an administrator opens
it again. The auth's Sign-in page says whom the method admits now.

`impulse audit` says each method's width in words:

| Width | Microsoft | Google |
| --- | --- | --- |
| One organization | `the accounts of one Microsoft Entra tenant` | `the accounts of one Google Workspace` |
| A list of organizations | `the accounts of the Microsoft Entra tenants it lists` | `the accounts of the Google Workspaces it lists` |
| Any organization | `the accounts of any Microsoft Entra tenant` | `the accounts of any Google Workspace` |
| Personal accounts too | `the accounts of any Microsoft Entra tenant, and Microsoft personal accounts` | `any Google account` |

Another identity provider reads `the accounts of one organization's identity provider`,
and the password method `anyone with an email address`.

## Which sign-in methods each shape can use

Each shape marks each sign-in method one of four ways.

- **Can be used**: the method fits the shape.
- **Cannot be used**: one of two things, and the shape's own section says which. Either
  the framework refuses the method (a command refuses it, or the application refuses to
  start), or the method can be registered but an auth that uses it this way is another
  shape, or none of the eight.
- **Works, not typical**: nothing refuses the method and the auth is still this shape,
  but the method limits the shape or is not what the shape is for. The shape's own
  section says how.
- **Not released yet**: an application cannot use the method. This mark is single
  sign-on's alone, in shapes 6 and 8.

In shapes 2 and 4 the marks are about the method an administrator opens to sign-up, since
that is what makes an auth one of those shapes. Other methods can be registered on the
same auth and stay invite only. The password's mark in shape 2 is the one exception: it
is about a password kept beside the directory's method for the same people, as that
shape's section says.

The marks are the same for Google and for Microsoft, so the table has one column for
each width.

| Shape | Password | One organization | A list of organizations | Any organization | Personal accounts too | Another identity provider |
| --- | --- | --- | --- | --- | --- | --- |
| 1 Invite only | Can be used | Can be used | Can be used | Can be used | Can be used | Can be used |
| 2 Directory members | Works, not typical | Can be used | Can be used | Cannot be used | Cannot be used | Can be used |
| 3 Directory roles | Cannot be used | Can be used | Cannot be used | Cannot be used | Cannot be used | Can be used |
| 4 Open sign-up | Can be used | Cannot be used | Cannot be used | Can be used | Can be used | Cannot be used |
| 5 Tenant-owned users, invite only | Can be used | Can be used | Can be used | Can be used | Can be used | Can be used |
| 6 Tenant-owned users, by directory | Cannot be used | Works, not typical | Works, not typical | Can be used | Works, not typical | Works, not typical |
| 7 Global users, invite only | Can be used | Can be used | Can be used | Can be used | Can be used | Can be used |
| 8 Global users, each tenant chooses | Can be used | Can be used | Can be used | Can be used | Can be used | Can be used |

In shapes 5 and 7 every method is a way for an invited person to sign in and nothing
more: no tenant has a directory, so no method brings a tenant's people in without an
invitation. In shapes 6 and 8 a tenant can take a directory, and the marks say how well
each method serves that.

Two shapes have no password sign-in, shapes 3 and 6. In shape 3 the directory gives every
role, and a password user would have none. In shape 6 every person of a tenant signs in
through the tenant's own directory, so a password could enter no tenant. An application
started from either has no password method: its login page shows the directory's sign-in
and no password form, and a person's account page has no password to change.

Shapes 6 and 8 have one more method, single sign-on through a tenant's own identity
provider. It is **not released yet**: the method is in the session library, the
framework's package `session`, as an internal package (`session/internal/workos`), which
an application cannot import. It is not the `oidc` method of the last column: that one
names a single provider in a setting, so it serves one organization.

One thing differs between Google and Microsoft: invitations. An invitation is accepted
only with the invited email address: the address of the sign-in must be the invited one.
A Google sign-in carries the person's address. A Microsoft sign-in carries none unless
the application's registration at Microsoft is set up to send it, and without it an
invited person who signs in with Microsoft is refused. (A person who is not accepting an
invitation and signs in with Microsoft confirms their address with a mailed code the first
time, as described under [In every shape](#in-every-shape).) A person can still accept with a
password where the auth has the password method. A new person chooses a password from the
link, and the account is made with the invited address. An account that has no address
yet accepts by the link alone and takes the invited address. This is described under
"Invitations" and "The first administrator's invitation" in
[session/README.md](../session/README.md).

The persona method, the sign-in of the test environments, is not part of any shape.
Given `--shape`, `impulse add auth` and `impulse add method` accept only `invite-only`
for it, and `impulse audit` prints it as left invite only. Where an auth has it beside
the methods that make a shape, `impulse audit` still names the shape, and says that the
persona method is registered in the test environments alone. It is described with
`add method` in
[cmd/impulse/README.md](../cmd/impulse/README.md).

## Shapes and the impulse command

The `impulse` command reads a shape and starts from one. It never stores one.

### Reading a shape: impulse audit

`impulse audit`, run in the application's directory, ends with a section named `sign-in`.
This is the section for a new application without tenants. `N` stands here for a line
number, which the command prints in its place:

```text
sign-in
  staff (pkg/auth/staff)
    password (pkg/auth/staff/password.go:N): admits anyone with an email address; the application gives the roles; invite only until an administrator opens it to sign-up on the Sign-in page, which is chosen while the application runs
    shape: 1 Invite only. It starts so, and stays so until an administrator opens a method to sign-up on the Sign-in page, which is chosen while the application runs: opening password makes it 4 Open sign-up.
  the application
    tenants: none
```

- `staff (pkg/auth/staff)` is an auth: its name and its package. Every auth has its own
  block.
- `password (pkg/auth/staff/password.go:N)` is a sign-in method of that auth: the name
  it is registered under, then the file and the line where it is registered. The rest of
  the line gives the method's three choices: how wide it is (`admits ...`), who gives the
  roles, and open to sign-up or invite only.
- Open to sign-up or invite only is chosen while the application runs, so the code does
  not say it. The command says where it is chosen and never guesses it.
- `shape:` is the shape the auth's choices add up to, by number and name.
- `the application` gives the application's own choices: tenants or none, and, with
  tenants, whether a tenant can have a directory of its own and through which auth a
  tenant is made. With tenants each auth's block also has a line, above its `shape:` line,
  that says whether a person belongs to one tenant or can be in several.

Shapes 1, 2 and 4 are one auth at different moments. Every method starts invite only, so
the command always prints such an auth as `1 Invite only`. It then says what opening each
method would make of it: `2 Directory members` for a method limited to its organizations,
`4 Open sign-up` for a method anyone can use. So the command never prints shape 2 or 4 as
an auth's own, and only the auth's Sign-in page shows which methods are open now.

The command reads each choice from the place the `impulse` commands write it. A choice
written some other way is printed as `not read here`, and is not guessed either.

### Starting from a shape: impulse new and impulse add

Four commands take `--shape`, with the shape's word or its number.

| Command | Shapes it takes | What the shape fills in |
| --- | --- | --- |
| `impulse add auth`, `impulse add method` | 1 to 4 | For each method: who gives the roles (the flag `--authority`), and how wide the method is (the flag `--slot` of `impulse add method`) |
| `impulse new`, `impulse add tenancy` | 5 to 8 | Tenants (the flag `--tenancy` of `impulse new`), whether a person belongs to one tenant (the flag `--one-tenant-per-person`), and whether a tenant can have a directory of its own (the flag `--tenant-directories`). In shape 6, the sign-in methods too |
| `impulse new` | 3 | The first auth's one sign-in method: the directory the flag `--method` names, for the one organization the flag `--organization` names, with the directory giving the roles |

`--authority` takes `application` or `directory`. For a Google, Microsoft or other
provider's method it has no default: the command asks when neither it nor a shape is
given.

The command prints what the shape filled in, under the heading `Starting point:`. A flag
given with `--shape` that contradicts the shape is refused before anything is written.
The refusal names both and says what to take instead. A shape the command does not take
is refused too, and the refusal lists the shapes it takes.

A shape only fills in the separate choices. Nothing remembers it afterward: the
application keeps the choices, and `impulse audit` reads the shape back from them. No
command opens a method to sign-up, since that is chosen while the application runs.

Shapes 5 and 6 differ, as shapes 7 and 8 do, by whether a tenant can have a directory
of its own, and so whether its people can come without an invitation. They do not differ
by which sign-in methods the application has.

Given shape 5 or 7, `impulse new` and `impulse add tenancy` write an application whose
tenants have no directories (`--tenant-directories=false`), whatever sign-in methods it
has or gets later. `impulse add tenancy` takes those shapes for an application that
already has a Google, Microsoft or other provider's method, and names the method as a way
for an invited person to sign in. `impulse audit` reads such an application as shape 5
or 7 with such a method as without one, where the application gives the method's roles.
A method whose directory gives the roles is outside the eight shapes in any application
with tenants, as [Outside the eight shapes](#outside-the-eight-shapes) describes.

Given shape 6 or 8 they write the tenant directories.

Given shape 8 they write no sign-in method that goes through an organization's
directory: they say which `impulse add method` adds it, and until it is added
`impulse audit` reads the application as shape 7, since no tenant's people can come
through a directory yet.

Given shape 6 they write the sign-in methods too, since every person of a tenant signs in
through the tenant's own directory. They write a Google method open to any Google
Workspace and a Microsoft method open to any Microsoft Entra tenant, neither with
personal accounts, with the application giving the roles. That is the one way the shape
starts: nothing is asked about a provider, and no flag takes one provider or another
width. `impulse new` writes the two in the password's place, so the new application has
no password method. `impulse add tenancy` writes the two on each auth whose roles the
application gives that has neither a Google nor a Microsoft method, and leaves an auth
that has either as it is. It removes no method: where the auth has the password method,
it says that the password keeps the application outside shape 6 and names the files to
change by hand, and `impulse audit` says the same until the method is gone.

Given shape 3, `impulse new` writes the first auth with one method, the directory's, in
the password's place, as [3. Directory roles](#3-directory-roles) describes. Shape 3 has
no tenants, so `impulse new` refuses it beside `--tenancy`.

Without `--shape`, `impulse new --tenancy` and `impulse add tenancy` write the tenant
directories, unless `--tenant-directories=false` is given.

Going from shape 5 to 6, or from 7 to 8, is the author's act: laying the tenant
directories into the application. No `impulse add method` does it by itself, and no
command does it yet.

In an application with tenants, `impulse add auth` and `impulse add method` still take
shapes 1 to 4, for the choices of the method they write. `impulse audit` then names the
auth by one of shapes 5 to 8, or prints `no named shape` where a directory gives the
roles. It names every auth of such an application so, whether or not a tenant is made
through it.

Every `impulse add` starts from a clean git working tree and leaves its change staged.
Where checks of `impulse check` still fail after its change, it hands them off in a
brief, as the wiring left to do. Finish what a command hands off and commit its change
(`git commit`) before running the next. `impulse new` commits the new application, but
leaves what `--shape` or `--tenancy` added staged in the same way. The examples below
show each commit.

Every command and flag is described in [cmd/impulse/README.md](../cmd/impulse/README.md).

## In every shape

These things hold for all eight shapes, wherever the auth sends mail.

### The email address on an account

An account's email address counts once the person has shown that the mailbox is theirs.
The address is then confirmed. It gets confirmed in one of these ways:

- The person accepted an invitation mailed to it, or signed up from a link mailed to it.
- Google vouched for it at a sign-in.
- The person entered a code mailed to it. Microsoft and other identity providers do not
  vouch for an address. So the first time a person signs in through one of them to an
  account that holds no confirmed address, the login page stops before the application
  opens: it asks for the address where the account has none, mails it a code, and the code
  confirms the address. Later sign-ins are not stopped for it.
- The person followed a link mailed to it: their own change on the Your account page, or
  a change an administrator asked for.

An administrator does not write an address onto an account. They ask for the change. The
new address is mailed a link, nothing changes until somebody follows it, and the address
the account held before is then told that it was replaced. The address of a person who
signs in through an organization's directory is the directory's: it is not changed in the
application, by the person or by an administrator.

### The way back in: a password reset link

Where an auth has the password method, a person who cannot sign in gets back in with a
link mailed to the confirmed address of their account.

- An administrator sends the link from the person's row: the application's administrators
  to any account, a tenant's administrator to a person of the tenant. The message says
  whose administrator asked.
- A person asks for their own on the login page, under "Forgot your password?", only while
  the application lets them. That is a switch on the auth's Sign-in page, which an
  administrator turns on and off while the application runs. It starts off. The switch is
  there where the Sign-in page carries the choice of who may sign up. An auth without that
  choice (its directory gives the roles, or it has tenants and its people do not create
  one) has no switch yet, and its people ask an administrator.
- The login page answers a person's own request the same way whether or not the address
  has an account. An address with no account is mailed nothing.
- Sending a link changes nothing. The password the account has still signs in, and the
  person's sessions stay open, until the link is used.
- The link works once, and for a day unless the application sets another time. Using it
  sets the new password and ends every session of the person. It signs nobody in: the
  person signs in with the new password.
- One address is sent at most five reset links in fifteen minutes, whoever asks. The next
  is refused for fifteen minutes, and an administrator who asked is told why. Sign-in
  codes are counted apart, so reset links never use up a person's codes.
- No link is sent to an account that has no password or no confirmed address. The
  administrator who asked is told so.
- Turning the switch off stops new requests. A link a person asked for while it was on
  still works until it is used or runs out.

### The mail an account gets without asking

- The address an account held is told when the account's address was changed.
- A person is told when an administrator forgets the browsers they trusted, so that their
  next sign-in asks for the code again.
- A person is told when an administrator removes them from a tenant: when the last role
  they held there is taken, whether it was their own or one they had through a group. The
  message says so when the account was deleted with it.

Nothing is mailed when an account is disabled or enabled, when a role ends by itself, when
a person leaves a tenant themselves, or when an administrator deletes the account
outright.

## Without tenants

An auth's users are one set for the whole application: they are not divided among
tenants. Its administrators manage every account. Think of one box that holds all the
people.

"The application's administrators" below are the people whose roles carry the grants on
the auth's accounts: the lists of the accounts and the operations that change them. The
skeletons give those grants to a role named `Administrator`. An application names its own
roles in its role file. A Super Administrator can give any role to a person. In the
skeletons the first administrator's invitation, described under
[1. Invite only](#1-invite-only), gives the `Administrator` role beside the Super
Administrator role, so the first person in can invite the others and open a method.

### 1. Invite only

`--shape invite-only`, taken by `impulse add auth` and `impulse add method`.

- **Gets an account**: only people an administrator invites, or creates an account for.
  Everyone else is turned away, and nothing is written for them.
- **Starts with**: the roles the administrator gives with the invitation.
- **Managed by**: the application's administrators.

| Sign-in method | In this shape |
| --- | --- |
| Password | Can be used |
| Google or Microsoft, at any of the four widths | Can be used |
| Another identity provider | Can be used |

Any method can be used, at any width. The width does not change the shape while the
method stays invite only. It still limits who can sign in through the method: an invited
person needs an account the method admits.

In an application without tenants, every auth whose roles the application gives starts as
this shape: each sign-in method is invite only until an administrator opens it.
`--shape invite-only` has the application give the roles (`--authority application`) and
changes nothing else. A new application is this shape as it is created:

```sh
impulse new ./harbor --module example.com/harbor --auth staff
```

- `./harbor` is the directory the application is created in.
- `--module example.com/harbor` is the application's Go module path.
- `--auth staff` names the application's first auth, the package `pkg/auth/staff`. The
  first auth signs in with a password.

`impulse audit` then prints the section shown under
[Reading a shape](#reading-a-shape-impulse-audit). The auth's block ends with:

```text
    shape: 1 Invite only. It starts so, and stays so until an administrator opens a method to sign-up on the Sign-in page, which is chosen while the application runs: opening password makes it 4 Open sign-up.
```

The first administrator of a deployed environment is invited too. While nobody in the
auth holds the Super Administrator role, the deployment mails an invitation that gives
the role to the address in the setting `APP_<AUTH>_ADMINISTRATOR`
(`APP_STAFF_ADMINISTRATOR` for an auth named `staff`). This holds for every auth whose
roles the application gives, so in every shape but
[Directory roles](#3-directory-roles). It is described with `add auth` in
[cmd/impulse/README.md](../cmd/impulse/README.md).

### 2. Directory members

`--shape directory-members`, taken by `impulse add auth` and `impulse add method`.

- **Gets an account**: anyone in the organization's directory. No invitation is needed.
  The account is created the first time the person signs in.
- **Starts with**: the default role set for the sign-in method that goes through the
  directory. An administrator chooses open to sign-up or invite only for each sign-in
  method while the application runs, and names the default role when opening one. Open
  cannot be saved without a default role, and invite only is shape 1.
- **Managed by**: the application's administrators. The directory decides who can sign
  in through it, and nothing more: the application reads no roles from it, and a person
  who leaves the directory keeps their account and their roles here until an
  administrator takes them away.

| Sign-in method | In this shape |
| --- | --- |
| Password | Works, not typical |
| Google or Microsoft, one organization | Can be used |
| Google or Microsoft, a list of organizations | Can be used |
| Google or Microsoft, any organization | Cannot be used |
| Google or Microsoft, personal accounts too | Cannot be used |
| Another identity provider | Can be used |

Any organization or personal accounts would let in the whole world. Such a method can be
registered, but once it is opened the auth is [Open sign-up](#4-open-sign-up). Given this
shape and such a width together, `impulse add method` refuses.

A new application's first auth has the password method, and no command removes a sign-in
method. Where the directory's method is added to that auth, as in the example below, the
password method is beside it and starts invite only. Kept for other people, who are
invited, it is ordinary: see
[Shapes that make sense together](#shapes-that-make-sense-together). Offered to the same
people as the directory, it is not typical: that is for moving an application from invite
only with passwords to a directory. `impulse add method` leaves the password method as it
is, and an administrator who opened it to sign-up would make the auth Open sign-up. The
shape needs a method that goes through a directory: given this shape and the password
method alone, the command refuses.

The default role is a role held for the whole application that the administrator could
give a person directly. It is never the Super Administrator role. A new application's
role file has two roles held for the whole application, `Administrator` and
`FeatureAdministrator`, so the role ordinary people start with is one the application
adds to its role file.

In the directory of an application made as under [1. Invite only](#1-invite-only):

```sh
impulse add method staff google workspace --shape directory-members
```

- `staff` is the auth the method is added to.
- `google` is the kind of method: Google.
- `workspace` is the name the method is registered under. It is a segment of the method's
  URL paths (`user/workspace/login`), and the name `impulse audit` lists the method by.
- `--shape directory-members` has the application give the roles. With no `--slot` the
  method is one organization's, and which Workspace that is comes from the setting
  `APP_STAFF_WORKSPACE_HOSTED_DOMAIN`. `--slot listed` can be given with the shape, for
  a list of organizations.

The command prints:

```text
Starting point: 2 Directory members.
  - The workspace method admits the accounts of one Google Workspace and nobody else, and the application gives the roles. It starts invite only: Open is chosen on the application's Sign-in page while the application runs, by whoever holds the grant on it, and from then everyone in the organization's directory gets an account at their first sign-in, with the role the administrator names.
```

`impulse audit` still prints `1 Invite only` for the auth, since every method starts
invite only. Where the `staff` auth also has its password method, the auth's block ends
with this line, in which `workspace` and `password` are the names of its two methods:

```text
    shape: 1 Invite only. It starts so, and stays so until an administrator opens a method to sign-up on the Sign-in page, which is chosen while the application runs: opening workspace makes it 2 Directory members, and opening password makes it 4 Open sign-up.
```

An administrator then opens the `workspace` method on the auth's Sign-in page and names
the default role.

### 3. Directory roles

`--shape directory-roles`, taken by `impulse new` and `impulse add auth`.
`impulse add method` refuses it on an auth that already has a method.

- **Gets an account**: anyone in the organization's directory who has a role there. A
  user with no role in the directory is turned away.
- **Starts with**: roles that come from the directory and are managed there by the
  directory's administrator. The application syncs the roles every time the user signs
  in, and takes away a role the directory no longer names.
- **Managed by**: the directory's administrator decides who has which role. Nobody gives
  roles inside the application: its pages show each person's roles as the directory's and
  never write them.

| Sign-in method | In this shape |
| --- | --- |
| Password | Cannot be used |
| Google or Microsoft, one organization | Can be used |
| Google or Microsoft, a list of organizations | Cannot be used |
| Google or Microsoft, any organization | Cannot be used |
| Google or Microsoft, personal accounts too | Cannot be used |
| Another identity provider | Can be used |

The auth has exactly one directory method and no other sign-in method. The roles are one
organization's, so the method is one organization's: the application refuses to start
with a wider method whose directory gives the roles.

A password user would have no role sync. So the application refuses to start with a
password, or any second method, on the same auth as a method whose directory gives the
roles. The commands refuse it first, and `impulse check`, the command that verifies an
application, reports it.

The method is neither opened to sign-up nor made invite only: the directory decides who
signs in. The directory gives the Super Administrator role too, like any other role, so
no first administrator is invited.

The application still sends mail, though it invites nobody. Through Microsoft, each
person's first sign-in stops for a code mailed to the address they give, as
[The email address on an account](#the-email-address-on-an-account) says. Through either
directory, a sign-in that carries no second factor of the directory's own stops for a
code mailed to the person's address. So each deployed environment needs its mailbox,
although no release asks for one: without it the code is written to the server's log,
and the person stays on the login page.

Where the roles come from depends on the method.

- A Microsoft method reads the application roles in the token of the sign-in.
- A Google method reads the person's Google Groups. A group named
  `<prefix><role>@<domain>` gives the role `<role>`, and the prefix is the setting
  `APP_<AUTH>_<METHOD>_GROUP_PREFIX`.
- Another identity provider sends a `roles` claim in its token.

Each is described with `add method` in
[cmd/impulse/README.md](../cmd/impulse/README.md).

An application starts in this shape, or gets it as a second auth.

A new application starts in it with `impulse new`:

```sh
impulse new ./fleet --module example.com/fleet --auth staff --shape directory-roles --method google --organization fleet.example
```

- `--shape directory-roles` writes the first auth, `staff`, with one sign-in method and
  no password method. The directory gives every role, so no first administrator is
  invited, and the application has no page or route that gives a role.
- `--method google` names the directory: `google` for a Google Workspace, `azure` for a
  Microsoft Entra tenant. `--method google:workspace` registers the method under the name
  `workspace`; without a name it is registered under `google` or `azure`.
- `--organization fleet.example` names the one organization: a Google Workspace by its
  domain, a Microsoft Entra tenant by its tenant id. It is written as the development
  value of the method's setting, and each deployment names its own.
- The command asks for the directory and the organization when a flag is missing. Where
  nobody is there to answer, it refuses and names the flag.
- Another identity provider's directory is not a start of `impulse new`: it is an auth of
  its own, added with `impulse add auth`, as below.

A development run of such an application signs a person in through a simulated
directory: the person the setting `APP_USERNAME` names, with the roles the setting
`APP_ROLES` names, both in the environment template. The development logins hold no
password account.

An application that already has a first auth gets this shape as an auth of its own, on
an outlet of its own. The first auth stays beside it, with its password method, on the
application's first outlet: no command removes an auth or a sign-in method. In the
application's directory:

```sh
impulse add outlet portal --prefix portal/api --auth staff
git commit -m "Add the portal outlet"
impulse add auth members --method google:workspace --shape directory-roles --outlet portal
```

- The first line adds a second outlet, named `portal`. `--prefix portal/api` is the URL
  prefix of the outlet's API. `--auth staff` binds the new outlet to the auth the
  application already has. The last line then binds it to the new auth instead.
- The second line commits the first line's change, once what that command handed off is
  done: every `impulse add` starts from a clean working tree.
- In the last line, `members` is the new auth's name, the package `pkg/auth/members`.
- `--method google:workspace` gives the auth one Google method, registered under the name
  `workspace`.
- `--shape directory-roles` has the directory give the roles (`--authority directory`)
  and keeps the method one organization's.
- `--outlet portal` names the outlet the new auth serves.

Either way, `impulse audit` ends the auth's block with:

```text
    shape: 3 Directory roles
```

### 4. Open sign-up

`--shape open-sign-up`, taken by `impulse add auth` and `impulse add method`.

- **Gets an account**: anyone. People create their own accounts.
- **Starts with**: the default role. An administrator chooses open to sign-up or invite
  only while the application runs, for each sign-in method, and names the default role
  when opening one. Open cannot be saved without a default role, and invite only is
  shape 1.
- **Managed by**: the application's administrators.

| Sign-in method | In this shape |
| --- | --- |
| Password | Can be used |
| Google or Microsoft, one organization | Cannot be used |
| Google or Microsoft, a list of organizations | Cannot be used |
| Google or Microsoft, any organization | Can be used |
| Google or Microsoft, personal accounts too | Can be used |
| Another identity provider | Cannot be used |

Open sign-up is for the general public, so it cannot be used when sign-in is limited to
named organizations. A method limited to its organizations can be registered, but once it
is opened the auth is [Directory members](#2-directory-members). Another identity provider
is always one organization's. Given this shape and such a method together, the command
refuses.

With the password method, a person gives their email address on the login page, follows
the link mailed to it and chooses a password. With Google or Microsoft, the account is
created at the person's first sign-in.

A new application needs no command for this shape: its password method is one anyone can
use. An administrator opens a method on the auth's Sign-in page. The page warns first: it
says who will be admitted and what role each of them gets, and the administrator confirms
by typing the word the page asks for. The same page makes the method invite only again,
and the people who have accounts keep them.

The default role is a role held for the whole application that the administrator could
give a person directly. It is never the Super Administrator role. As in shape 2, the
role ordinary people start with is one the application adds to its role file.

To add a Google method anyone can use, in the directory of an application made as under
[1. Invite only](#1-invite-only):

```sh
impulse add method staff google --shape open-sign-up
```

- `staff` is the auth the method is added to.
- `google` is the kind of method: Google. No name follows it, so the method is registered
  under the name `google`.
- `--shape open-sign-up` has the application give the roles and registers the method as
  wide as any Google account (`--slot any`). A Microsoft method gets `--slot any-personal`.

The command prints:

```text
Starting point: 4 Open sign-up.
  - The google method admits any Google account, and the application gives the roles. It starts invite only: Open is chosen on the application's Sign-in page while the application runs, by whoever holds the grant on it, and from then anyone can sign up, and gets the role the administrator names.
```

`impulse audit` still prints `1 Invite only` for the auth, and names each method whose
opening makes the auth `4 Open sign-up`. Here they are the two methods of the `staff`
auth, `google` and `password`:

```text
    shape: 1 Invite only. It starts so, and stays so until an administrator opens a method to sign-up on the Sign-in page, which is chosen while the application runs: opening google or password makes it 4 Open sign-up.
```

### Shapes that make sense together

Shapes 1, 2 and 4 are the same auth. Which of the three it is depends on which of its
methods are open to sign-up.

**1 and 2 go together.** The choice is made for each sign-in method. For example,
employees sign in with the organization's Google Workspace directory, which is open to
sign-up, and outside partners are invited and sign in with a password, Google or
Microsoft, which stay invite only.

**4 does not go with 1 or 2.** When anyone can sign up, a directory's members are already
covered, and invite only is the opposite of open sign-up. The framework does not refuse an
auth with both kinds of method open: each method keeps its own choice and its own default
role. Such an auth admits anyone, so it is open sign-up whatever else is open. Sending an
invitation still works in an open sign-up application: the invited person gets the roles
the invitation carries.

**3 stands alone.** It cannot share an auth with any other shape. An application that
needs it together with another shape gives it an auth of its own.

## In every shape with tenants

These things hold for shapes 5 to 8.

A new application with tenants has one auth, and it holds everyone: the application's own
staff and every tenant's people have their accounts in that auth. What tells them apart is
where each holds a role. The examples below name that auth `staff`, as the first auth of
a new application is often named.

The auth's Sign-in page decides who may create a tenant. Who joins a tenant is the
tenant's to decide. In shapes 5 and 7 a tenant's people are the people it invites and
nobody else, and the commands write no Sign-in page for a tenant, since no tenant has a
directory to set there. In shapes 6 and 8 each tenant's Sign-in page decides who joins
the tenant beside the people it invites.

### Staff

The application's own people hold roles for the whole application or in every tenant. A
role held in every tenant counts in each tenant, including the tenants made later. Staff
belong to no one tenant, and the rule of one tenant per person never applies to them.
They get their accounts as in [shape 1](#1-invite-only): the deployment invites the first
administrator, and an administrator invites the others.

### Who can create a tenant

Nobody creates a tenant directly. A tenant comes to exist in one of two ways.

**By invitation.** Staff do not create tenants. They invite a person, and that person's
first sign-in creates the tenant and makes them its first administrator. This is how to
control who is added. In the skeletons the invitation is sent from the Invited tenants
page, in the System menu, by a holder of the `Administrator` role. The invitation carries
the tenant's name. Where a tenant can have a directory, it can also say that the tenant
is entered through its directory only, and name the role the directory's people start
with.

**By sign-up.** Anyone can sign up and create their own tenant, and becomes its first
administrator. This works while an administrator has a sign-in method open to sign-up on
the auth's Sign-in page. In an application with tenants, opening a method names no default
role: a person who signs up holds no tenant, and the application offers to let them
create one. A person who already has a tenant creates no second one this way, in any
shape.

Whether its tenants are made by invitation, by sign-up or both does not change an
application's shape. With tenants, a shape's name says how a tenant's people come to the
tenant, not how the tenant came to exist.

Where a tenant can have a directory, and the person who makes the tenant is signed in
through an organization's directory, that directory becomes the tenant's own, in the same
step. A person whose organization's directory is already another tenant's makes no second
tenant: they are refused, and told to ask that tenant's administrator for an invitation.
A person who is signed in with a password makes a tenant with no directory, and an
administrator of the tenant can set one later, as
[A tenant's directory](#a-tenants-directory) describes.

In an application whose tenants have no directories, the person makes the tenant however
they signed in, and nothing is taken from the sign-in.

One auth of the application is the one a tenant is made through: the auth of the outlets
that serve the tenant record. `impulse audit` names it. The Sign-in page of any other auth
of the same application offers nothing to open: a person who signed up to it could be
given no tenant. Where the application gives such an auth's roles, `impulse audit` names
it by one of shapes 5 to 8 all the same.

### A tenant's directory

A tenant has at most one directory, and a directory belongs to one tenant only.

A tenant can have a directory where two things are in the code. The application has a
directories record, the table that holds each tenant's directory, which
`impulse add tenancy` and `impulse new --tenancy` write unless they are given
`--tenant-directories=false`, or `--shape` with shape 5 or 7. And the auth registers a
Google, Microsoft or other provider's method whose roles the application gives. Whether
a tenant then has one is settled while the application runs.

An application without the directories record has no tenant directories, and that is
shapes 5 and 7. No tenant takes a directory, none is opened to one and none is made
directory only. A Google, Microsoft or other provider's method is then a way for an
invited person to sign in and nothing more: a person from an organization's directory
who has no account and no invitation joins no tenant, whichever methods the auth has.
They are refused while the method is invite only, as every method starts. Where an
administrator has opened the method to sign-up, they get an account that holds no
tenant, and can create a tenant of their own, as
[Who can create a tenant](#who-can-create-a-tenant) describes.

A person an administrator created an account for is not such a person. Their sign-in
under the account's sign-in name is joined to the account, through any of the auth's
methods. An account that has a password asks for it first. An account that has none, and
has never signed in, is joined to the first sign-in under its name, as in an application
without tenants.

The rest of this section, and the next, describe an application whose tenants can have
directories.

Nobody types a directory in. It is set from a sign-in, in one of two ways:

- when the tenant is made, by a person who is signed in through an organization's
  directory;
- later, by an administrator of the tenant who is signed in through their organization's
  directory, on the tenant's Sign-in page.

The directory is a Google Workspace, a Microsoft Entra tenant, or an organization's
identity provider that the application registers with the `oidc` method. A personal
account belongs to no organization, so it is never a tenant's directory.

### What a tenant's administrator sets

A tenant's administrator is a person who holds, in that tenant, a role the application
names as an administrator's role of a tenant. In the skeletons that role is
`TenantAdministrator`, and the person who makes a tenant is given it.

Where a tenant can have a directory, the tenant's administrator makes three choices, on
the tenant's Sign-in page.

| Choice | What it does |
| --- | --- |
| The tenant's directory | Makes the directory the administrator is signed in through the tenant's own, or removes it. |
| Open to the directory, or invite only | Open names the role each person from the directory is given at their first sign-in through it, with no invitation. The role is one of the tenant's roles that the administrator could give directly. Invite only clears it: the directory's people then need an invitation. A tenant starts invite only unless the invitation that made it named a role. |
| Directory only | Closes the tenant to every sign-in but one through its directory: no password, no personal account, no other organization's directory. That keeps out the application's own staff too. Two kinds of people still get in: an administrator of the tenant itself, with a password and a code mailed to their email address, and a Super Administrator. |

A person from a tenant's directory gets in by the tenant's choice, whether or not the
sign-in method is open to sign-up on the auth's Sign-in page.

Opening the tenant to its directory and making it directory only each warn first, with
who is affected, and are confirmed by typing a word (`open`, `close`).

A tenant's administrator can turn directory only on once the tenant has its directory,
and must be signed in through that directory to do it. Where the auth has the password
method, the administrator must also have a password of their own, which is their way back
in if the directory stops working. A person who has only ever signed in through a
directory chooses a password on the Your account page first. A tenant can also be
directory only from the start: the invitation that makes it says so, and is then accepted
through an organization's directory and no other way. The password rule applies only
when directory only is turned on from the page: the first administrator of a tenant made
directory only may have no password until they choose one on the Your account page. The
directory of a tenant that is directory only cannot be removed.

A tenant's choices restrict who enters that tenant and no other. A tenant that is not
directory only admits everyone it has given a role, however they signed in.

Deleting a tenant takes its directory with it, whether the tenant is directory only or
not, along with the roles held in the tenant, the invitations into it that still stand
and its rows. The directory can then be made another tenant's.

### Who manages an account

A person has one account in the auth, whatever the number of tenants they are in: a
person who is in three tenants has one account, one password and one email address. A
tenant's administrator who changed that account would change it for the other tenants
too. So the application's role file says what a tenant's administrator may do to the
account of a person who also holds a role somewhere else.

The application's management of an auth's accounts has two sides.

- **The whole application's side** lists every account of the auth and changes any of
  them. The skeletons grant it to the `Administrator` role, held for the whole
  application.
- **A tenant's side** lists the tenant's people, the roles they hold there and the
  tenant's invitations, and changes them. The skeletons grant it to the
  `TenantAdministrator` role, held in a tenant.

Each person in a tenant's list carries two facts that a grant's condition can test.
`heldElsewhere` says the person also holds a role in every tenant, or holds a role or
belongs to a group in another tenant. `heldGlobally` says the person holds a role for the
whole application, or belongs to a group kept for the whole application. (The "globally"
in that name is the role file's word for the whole application. It is not the "Global" of
shapes 7 and 8, which is about being in several tenants.) With the skeletons' role file,
a tenant's administrator can do this much:

| A tenant's administrator can | For whom |
| --- | --- |
| Invite a person, create an account in the tenant | Anyone. Where a person belongs to one tenant, anyone who is in no other tenant. |
| Give a role in the tenant | A person who already holds a role in the tenant. A person new to the tenant is invited or has an account created there, with a role from the start. |
| Take away a role held in the tenant | Anyone in the tenant. A role a person holds in every tenant is not one tenant's to take. |
| Withdraw an invitation | An invitation of the tenant that still stands |
| See the name a person signs in with and their name, whether the account is disabled, and when it was created and last signed in | Everyone in the tenant |
| See a person's email address, and whether they must choose a new password | A person who holds no role in another tenant or in every tenant (`heldElsewhere = false`) |
| Send a password reset link | Everyone in the tenant who has a password and a confirmed email address |
| Disable and enable the account, delete it, rename it, set its name, ask for a change of its email address (which takes effect once the new address confirms), set a temporary password, require a new password, sign the person out everywhere, forget the browsers they trusted, unlink a sign-in identity, remove the password | A person the tenant holds alone (`heldElsewhere = false AND heldGlobally = false`) |

A browser a person trusted is one they asked the application to remember, after they
entered the code the application mails at a sign-in that asks for a second step.

An application that `impulse new` makes with tenants, or that `impulse add tenancy` gives
them, starts with this role in its own names (`<Record>Administrator`): each of these
grants on what the application serves, under the same condition. The command writes
`test/integration/tenant_people_test.go` with it, the test that proves each condition
on the served application, because `impulse check` refuses a conditional grant that no
test proves (`conditions-proven`). Where the auth has no password sign-in, the role and
its test leave out the rows about a password, which such an application does not serve,
and the test signs people in through the simulated directory (`-tags skipAuth`).

`impulse check` has a check for the last row, named `tenant-accounts`. It fails when a
grant lets a role held in a tenant run one of those operations on an account with no
condition that tests the person's row. Which condition is the application's to choose.

Some rules hold whatever the role file says. Among them:

- Taking away the last role a person holds anywhere, their own or through a group,
  deletes the account, with its sign-in identities and its sessions. Where a directory's
  people get an account without an invitation, such a person's next sign-in through the
  directory is a first sign-in again, and makes a new account with the default role.
- Only a Super Administrator gives or takes the Super Administrator role, or changes how
  another Super Administrator signs in. The role is never taken from the last holder who
  can sign in.
- A person does not give up their administrator's role in a tenant where that would
  leave the tenant with no administrator.
- The administrators of a tenant that is directory only are changed only by someone that
  tenant admits, or by a Super Administrator.

A person who may give themselves a role in a tenant can also take that role for a set
time, on the Break glass page of the whole application's side, but only when they also
hold the grant of that page. It is for an emergency, such as a tenant whose only
administrator can no longer sign in. The person picks the tenant, a role that exists in
every tenant, and how long: 1 hour, 4 hours, 8 hours or 1 day. They say why in one line.
The role goes to that person and nobody else, and ends by itself when the time is over.
The reason is kept with the role and shown wherever the person's roles are listed. With
the skeletons' role file, the tenant's administrator reads it beside the role and can
take the role away before its time is over. Taking a role this way gives nobody a power
they did not have: beside the grant of the page, the person needs whatever giving
themselves that role in that tenant needs, which a Super Administrator always has. The
skeletons grant the page to the `Administrator` role and to no role held in a tenant: a
tenant's administrator may give themselves a role in their own tenant, and still takes
none for a set time. In an application without tenants the page is the same without the
choice of tenant, and the role is one held for the whole application. The Super
Administrator role is never taken for a set time. An auth whose directory gives the
roles ([shape 3](#3-directory-roles)) has no Break glass page: nobody gives a role
inside the application there.

A person changes their own account on the Your account page: their name, their password,
the browser they asked to be remembered, and their email address, which changes once they
follow a link mailed to the new one. The switcher next to the application's name lists
their tenants. A tenant they cannot enter with the way they signed in is listed as
closed, with the reason.

## With tenants, one tenant per person

Every tenant follows the same rule. A person belongs to exactly one tenant, and that
tenant's administrator manages their account. Think of three tenants as three boxes,
each holding its own people.

The option `identity.WithOneTenantPerPerson()` holds a person to one tenant. It is an
option of the framework's package `identity`. An auth's package passes it where it builds
its identity client, the object that carries out every change to the auth's accounts.
`--one-tenant-per-person` on `impulse new` and `impulse add tenancy` writes it, and so
does `--shape` with shape 5 or 6. With the option:

- a role in a tenant is refused for a person who holds a role in another tenant, however
  it would be given: directly, by an accepted invitation, or by a directory's default
  role;
- an invitation into a tenant is refused as soon as it is sent, if the invited email
  address is the confirmed address of an account that belongs to another tenant;
- a refusal never says which tenant holds the person.

Since each person is in one tenant, every person in a tenant's list except the
application's own staff is the tenant's alone. Its administrator manages each such account
in full, as [Who manages an account](#who-manages-an-account) describes. Staff are outside
the rule, and manage any account from the whole application's side.

### 5. Tenant-owned users, invite only

`--shape tenant-owned-users-invite-only`, taken by `impulse new` and
`impulse add tenancy`.

- **Gets an account**: only people the tenant's administrator invites, or creates an
  account for. A person belongs to one tenant, so an invitation from a second tenant is
  refused, when it is sent or when it is accepted.
- **Starts with**: the roles the tenant's administrator gives with the invitation.
- **Managed by**: the tenant's administrator.

| Sign-in method | In this shape |
| --- | --- |
| Password | Can be used |
| Google or Microsoft, at any of the four widths | Can be used |
| Another identity provider | Can be used |

Any method can be used, and the methods are the same for every tenant. No tenant has a
directory: an application made with this shape has tenants and no tenant directories, so
a Google, Microsoft or other provider's method is a way for an invited person to sign in
and nothing more. An invited person accepts by signing in with any of the auth's methods,
with the invited email address (with Microsoft, only where the sign-in carries the
person's address, as
[Which sign-in methods each shape can use](#which-sign-in-methods-each-shape-can-use)
describes). A person from an organization's directory who has no account and no
invitation joins no tenant, whichever methods the auth has, as
[A tenant's directory](#a-tenants-directory) describes, and nothing can be opened to a
directory or made directory only.

What separates this shape from [shape 6](#6-tenant-owned-users-by-directory) is not which
methods the auth has. It is whether a tenant can take a directory, and so whether its
people can come without an invitation. Going to shape 6 is the author's act: laying the
tenant directories into the application. No `impulse add method` does it by itself.

```sh
impulse new ./depot --module example.com/depot --auth staff --shape tenant-owned-users-invite-only
cd depot
git commit -m "Tenants, one for each person"
impulse add method staff google --slot any --authority application
```

- The first line creates the application in the directory `./depot`, with the Go module
  path `example.com/depot` and a first auth named `staff` (the package `pkg/auth/staff`),
  which signs in with a password. `--shape tenant-owned-users-invite-only` gives the
  application tenants, holds a person to one tenant and leaves the tenants without
  directories. It is the same as
  `--tenancy --one-tenant-per-person --tenant-directories=false`.
- The second and third lines move into the application's directory and commit what
  `--shape` added, which `impulse new` leaves staged, once what the command handed off is
  done: every `impulse add` starts from a clean working tree.
- The last line adds a Google method to the `staff` auth, registered under the name
  `google`, for any Google account, with the application giving the roles. The
  application stays this shape with it, and without it.

In the auth's block, `impulse audit` prints a line that starts
`a person belongs to one tenant (identity.WithOneTenantPerPerson,` and ends with the file
and the line where the option is passed. Then it prints:

```text
    shape: 5 Tenant-owned users, invite only. The tenants have no directories (the tenant record declares none), so a tenant's people come by invitation alone, and the google method is a way for an invited person to sign in and nothing more.
```

Before the method is added, the line ends after `come by invitation alone`.

### 6. Tenant-owned users, by directory

`--shape tenant-owned-users-by-directory`, taken by `impulse new` and
`impulse add tenancy`.

- **Gets an account**: anyone in the tenant's directory, once the tenant has opened
  itself to its directory. They land in that tenant. A person belongs to one tenant, so
  an invitation from a second tenant is refused, when it is sent or when it is accepted.
- **Starts with**: the default role the tenant set for its directory. A tenant that sets
  no default role invites its people instead.
- **Managed by**: the tenant's administrator.

| Sign-in method | In this shape |
| --- | --- |
| Password | Cannot be used |
| Google or Microsoft, one organization | Works, not typical |
| Google or Microsoft, a list of organizations | Works, not typical |
| Google or Microsoft, any organization | Can be used |
| Google or Microsoft, personal accounts too | Works, not typical |
| Another identity provider | Works, not typical |
| Single sign-on | Not released yet |

A tenant has at most one directory, and a directory belongs to one tenant only.

Google and Microsoft accept a work account from any organization. That alone lets nobody
into a tenant: a person gets into one only if their organization has a tenant that is
open to its directory, or they carry an invitation. Whether a person with no tenant can
make one is a separate choice: see [Who can create a tenant](#who-can-create-a-tenant).

Naming the approved organizations, with the list width, also works. It is not typical: no
tenant could then take a directory without a deployment that puts its organization on the
list. A method that is one organization's, or another identity provider, can be the
directory of one tenant only.

A gmail.com or outlook.com address belongs to no organization, so personal accounts
cannot be a tenant's own directory. A method that admits them too still works: a person
with a personal account comes by invitation.

The shape has no password sign-in. Every person of a tenant signs in through the tenant's
own directory, so a password could enter no tenant, and an application started in this
shape has no password method. Nothing refuses one: an application made before the shape
lost its password has it still, and `impulse audit` then prints `no named shape` for the
auth, says that the password method keeps it outside this shape, and names the files to
change by hand. No command removes a sign-in method.

The framework treats the other sign-in methods of shapes 6 and 8 alike. The marks differ
because in this shape a tenant's people are meant to come through its directory, while in
shape 8 a tenant without a directory is ordinary.

```sh
impulse new ./depot --module example.com/depot --auth staff --shape tenant-owned-users-by-directory
```

- The line creates the application in the directory `./depot`, with the Go module path
  `example.com/depot` and a first auth named `staff` (the package `pkg/auth/staff`).
- `--shape tenant-owned-users-by-directory` gives it tenants, holds a person to one
  tenant, and writes the `staff` auth with two sign-in methods and no password method: a
  Google method, registered under the name `google`, open to any Google Workspace, and a
  Microsoft method, registered under the name `azure`, open to any Microsoft Entra
  tenant. Neither admits personal accounts, and the application gives the roles of both.
- The command asks nothing about a provider and takes no flag for one provider or for
  another width. A tenant is then the directory of whichever organization its founder
  signs in from, at Google or at Microsoft. Another width is the author's change to what
  the command wrote: it works, as the marks above say, and `impulse audit` names it in a
  sentence.
- Before the application deploys, its author registers it at both providers, since each
  method needs a client id and a secret of its own.
- The application's own people come by invitation and accept it through either method.
  The first administrator's invitation is accepted the same way. An invitation is
  accepted with Microsoft only where the sign-in carries the person's address, as
  [Which sign-in methods each shape can use](#which-sign-in-methods-each-shape-can-use)
  describes.
- The command also registers the persona method, the sign-in of the test environments,
  and writes a cast of one persona, `admin`. A development run signs its administrator in
  as that persona. The development logins hold no password account.

`impulse add tenancy --shape tenant-owned-users-by-directory` gives an application that
has no tenants yet the same shape. On an auth with neither a Google nor a Microsoft
method it writes the same two methods. It leaves the password method where the auth has
one, and says what `impulse audit` then says of it.

`impulse audit` prints, in the auth's block, the line that says a person belongs to one
tenant, as in shape 5, and:

```text
    shape: 6 Tenant-owned users, by directory. A tenant's people can come through its organization's directory (the azure and google methods). Whether a tenant takes a directory, and whether that directory's people get an account without an invitation, is each tenant's choice, made while the application runs on the tenant's Sign-in page. The persona method is registered in the test environments alone, where the persona provider is configured, and is part of no shape.
```

Staff then invite each tenant's first administrator. That person signs in with their
organization's Google or Microsoft account, which makes the tenant and makes their
organization its directory. They then open the tenant to its directory on the tenant's
Sign-in page.

## With tenants, several tenants per person

A person has one account for the whole application. They can be in several tenants, with
a different role in each. Think of three tenants as three boxes, with two people who
each sit in two boxes at once.

This is an application with tenants as `impulse new --tenancy` and `impulse add tenancy`
write it: no auth passes `identity.WithOneTenantPerPerson()`.

Who manages an account is the role file's rule, as
[Who manages an account](#who-manages-an-account) describes. With the skeletons' role
file, a tenant's administrator manages the account of a person who is in that tenant
alone. Once the person has a role in a second tenant, or a role for the whole
application, the account is managed from the whole application's side. In each tenant a
person is in, that tenant's administrator still controls the person's role there and can
remove them from the tenant.

### 7. Global users, invite only

`--shape global-users-invite-only`, taken by `impulse new` and `impulse add tenancy`.

- **Gets an account**: anyone a tenant invites.
- **Starts with**: the roles given with the invitation, separately in each tenant.
- **Managed by**: the tenant manages the account while the person is in that tenant
  alone. See [Who manages an account](#who-manages-an-account).

| Sign-in method | In this shape |
| --- | --- |
| Password | Can be used |
| Google or Microsoft, at any of the four widths | Can be used |
| Another identity provider | Can be used |

Any method can be used, and the methods are the same for every tenant: an auth registers
them in code.

No tenant has a directory, as in [shape 5](#5-tenant-owned-users-invite-only): an
application made with this shape has tenants and no tenant directories. A Google,
Microsoft or other provider's method is a way for an invited person to sign in and
nothing more, and a person from an organization's directory who has no account and no
invitation joins no tenant. Going to [shape 8](#8-global-users-each-tenant-chooses) is
the author's act, laying the tenant directories into the application, and no
`impulse add method` does it by itself.

```sh
impulse new ./depot --module example.com/depot --auth staff --shape global-users-invite-only
```

- `./depot` is the directory the application is created in.
- `--module example.com/depot` is the application's Go module path.
- `--auth staff` names the application's first auth, the package `pkg/auth/staff`. It
  signs in with a password.
- `--shape global-users-invite-only` gives the application tenants and leaves them
  without directories. It is the same as `--tenancy --tenant-directories=false`.

`impulse audit` prints, in the auth's block:

```text
    a person can be in several tenants (the auth's package hands its identity client no identity.WithOneTenantPerPerson)
    shape: 7 Global users, invite only. The tenants have no directories (the tenant record declares none), so a tenant's people come by invitation alone.
```

A Google, Microsoft or other provider's method added afterward, as in shape 5, leaves
the application this shape. The line then goes on to name the method as a way for an
invited person to sign in.

### 8. Global users, each tenant chooses

`--shape global-users-each-tenant-chooses`, taken by `impulse new` and
`impulse add tenancy`.

- **Gets an account**: anyone in a directory a tenant has taken, with no invitation
  needed, where the tenant set a default role for it. Everyone else comes by invitation.
  A tenant that takes no directory, or takes one with no default role, is invite only.
- **Starts with**: the default role the tenant set for its directory, or the roles given
  with the invitation.
- **Managed by**: as in shape 7. See [Who manages an account](#who-manages-an-account).

| Sign-in method | In this shape |
| --- | --- |
| Password | Can be used |
| Google or Microsoft, at any of the four widths | Can be used |
| Another identity provider | Can be used |
| Single sign-on | Not released yet |

Any method can be used, and the methods are the same for every tenant.

A tenant has at most one directory, and a directory belongs to one tenant only. So a
method that is one organization's, or another identity provider, lets one tenant have a
directory, and the list width lets the organizations on the list have one. A method as
wide as any organization lets every tenant choose. A tenant that has no directory is
invite only, which is ordinary in this shape.

A person is given the directory's default role at their first sign-in through the
directory, whether their account is created then or existed before, unless they already
hold a role in the tenant. A later sign-in gives nothing: a role the tenant's
administrator took away afterward is not given back while the person still has their
account. Taking away the last role a person holds anywhere deletes the account, and
their next sign-in through the directory is then a first one again.

```sh
impulse add tenancy --shape global-users-each-tenant-chooses
git commit -m "Tenants"
impulse add method staff google --slot any-workspace --authority application
```

- The first line, run in the directory of an application without tenants, gives it
  tenants. A person can be in several.
- The second line commits the first line's change, once what that command handed off is
  done: every `impulse add` starts from a clean working tree.
- The last line adds a Google method to the auth named `staff`, registered under the
  name `google`. `--slot any-workspace` registers it as wide as any organization, so
  every tenant's organization can sign in through it. `--authority application` has the
  application give the roles. For Microsoft the line is
  `impulse add method staff azure --slot any --authority application`, with the same
  note on invitations as in shape 6.

`impulse audit` prints, in the auth's block, the line that says a person can be in
several tenants, as in shape 7, and:

```text
    shape: 8 Global users, each tenant chooses. A tenant's people can come through its organization's directory (the google method). Whether a tenant takes a directory, and whether that directory's people get an account without an invitation, is each tenant's choice, made while the application runs on the tenant's Sign-in page.
```

## Outside the eight shapes

An auth whose choices add up to none of the eight reads `no named shape` in
`impulse audit`, followed by the reason. That alone is not an error: `impulse check`
fails only on what cannot work.

| The auth | The reason `impulse audit` gives, in short |
| --- | --- |
| A directory gives the roles, in an application with tenants | No shape has both tenants and roles from a directory. |
| A directory gives the roles, on an auth with another method | A method whose roles come from the directory stands alone in its auth. This is the one that cannot work: the application refuses to start, and `impulse check` reports it. Where the other method is the password method, the reason names the two changes to make by hand: take the password method out, so the directory gives every role, or have the application give the roles, so the password stays. |
| A password method, on an auth that would otherwise be shape 6 | The password method keeps it outside shape 6, where every person of a tenant signs in through the tenant's own directory. The reason names the files to change by hand. |
| Who gives a method's roles is written where the command does not read it | Who gives the roles is not read, and the shape depends on it. |
| No sign-in method is read on the auth | No sign-in method is read on its Auth. |
