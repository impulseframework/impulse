# persona

The persona sign-in for test environments, as a Go module a host application mounts.

A test environment of an application (a pull-request environment, or the test
environment itself) seeds a cast of personas: accounts with names, addresses and roles
that exercise the application's screens. Nobody can sign in as them through a real
identity provider, because they are not real people. This module is the OpenID Connect
provider that signs a person in as one of them, after the host has signed the person in
as themselves and said they may enter that application.

The host is the origin of record. It knows the applications, their deploy identities and
who may enter them. The module owns everything else: the registrations the deploys send,
the protocol's state between requests, the picker page, and the log of who signed in as
which persona into which environment.

## How a sign-in runs

1. A deploy of the application registers its environment: the callback addresses of the
   sites it serves and the cast it seeded. It authenticates with a Google identity token
   for the host's issuer, so the deploy identity is the token's verified service account.
2. The application's login page offers the persona method. The session library's
   generic OpenID Connect method (`github.com/impulseframework/impulse/session/oidc`) sends the browser to
   the host's authorize endpoint as a public client, with PKCE.
3. The host's session middleware runs ahead of the module. A browser with no session goes
   to the host's login page and comes back. The module asks the host whether this person
   may enter this application; a no is the end of it.
4. The picker shows the cast; a pick, or a `login_hint` naming a persona, completes the
   request. The browser returns to the application with a code, the application exchanges
   it for an ID token, and the token's subject is the persona's login. The application
   links or creates its account from there as it does for any provider.

## Mounting it in the host

```go
store := spannerstore.New(spannerClient)
deployers, err := persona.NewGoogleDeployers(ctx)
if err != nil {
	return err
}
svc, err := persona.New(ctx, host, store, &persona.Config{
	Issuer:    "https://control.example.com/persona",
	Actor:     actorFromRequest,
	LoginURL:  "/login",
	Deployers: deployers,
})
if err != nil {
	return err
}
router.Mount("/persona", svc.Handler())
```

- `Issuer` is the provider's URL: the host's base URL plus the mount path. Applications
  configure it as the method's issuer, and the discovery document is served under it.
- `host` implements `persona.Host`: `ApplicationByClient` resolves the client ID root
  the host minted when it created an application to the application and the deploy
  identities allowed to register for it; `MayEnter` says whether the signed-in person may
  enter that application's test environments.
- `Actor` reads the signed-in person from a request. The handler must be mounted under
  the middleware that starts the host's session; the picker sends a browser without one
  to `LoginURL` with a `returnUrl` query parameter and expects to be returned there.
- `Deployers` verifies the bearer token a deploy presents. `GoogleDeployers` accepts the
  identity tokens Google issues to service accounts, with the issuer as the audience.
- `Expiry` (default seven days) is how long a registration stands without a refresh; a
  redeploy refreshes it, and `Sweep` removes what has lapsed. Run `Sweep` on a schedule.
- `AllowInsecureIssuer` admits an http issuer and http callback addresses, for local
  development only.

### The store

The Spanner store needs the tables under `schema/spanner/migrations`. Copy the
migrations into the host's migration set as they are; the names are
`PersonaRegistrations`, `PersonaRequests`, `PersonaTokens`, `PersonaKeys` and
`PersonaSignIns`. Another store implements `persona.Store`.

The signing keys live in the store so every instance signs with the same key and
publishes the same key set. The access tokens are encrypted under a key derived from
the newest signing key, for the same reason. `RotateSigningKey` adds a new key; the
older ones stay published so tokens signed before the rotation still verify.

## The registration endpoint

`PUT {issuer}/registrations/{clientID}` with `Authorization: Bearer {identity token}`:

```json
{
  "applicationClientId": "lanterns",
  "environment": "tst",
  "pullRequest": 12,
  "redirectUris": ["https://lanterns-pr12.example.com/api/user/persona/callback"],
  "cast": [
    {"login": "cadet", "name": "Cadet Example", "email": "cadet@example.com", "roles": ["Cadet"]},
    {"login": "navigator", "name": "Navigator Example", "email": "navigator@example.com", "roles": ["Navigator", "Crew"]}
  ]
}
```

The client ID in the path is the deployment's: the application's root with the pull
request number appended (`lanterns-pr12`), or with the environment (`lanterns-tst`) for
an environment's own deployment; `persona.ClientIDFor` spells it. The deploy identity
must be one of the application's, and `applicationClientId` must be a root the host
minted. The answer carries the client ID and the registration's expiry.

`DELETE {issuer}/registrations/{clientID}` removes a registration when an environment is
torn down, for the same identity. `GET` answers a signed-in person of the host with the
registration, for the host's own pages.

## What the application configures

The application registers the session library's generic method with the host's issuer,
its deployment's client ID, no client secret, and `InvitedUsersOnly` or whichever
account rule fits: the cast is seeded, so the personas already have accounts to link.
The ID token carries `sub` and `preferred_username` (the login), `name`, `email` and
`email_verified` under the `profile` and `email` scopes. The provider asserts identity
alone; the roles a persona holds are the application's seed, not the token's.

## Development

The tests run the Spanner emulator in a container through `github.com/impulseframework/impulse/db`
and drive the whole round trip with the session library's verifier as the application.

```sh
go test ./...
golangci-lint run ./...
```
