package crew

import (
	"context"
	_ "embed" // the cast rides in the binary
	"fmt"
	"io"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/cast"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session"
)

// castFile is the cast of the persona sign-in: the personas the test environments offer,
// declared beside this package in cast.json (login, name, address and roles, each role
// one the role file defines). The pipeline registers the cast with the persona provider
// after traffic (bedrock deploy persona register), and the application seeds its
// accounts at start where the method is registered (SeedCast). The logins are on no
// manifest: hq holds Headquarters, the fleet's global administration; warden holds
// SectorMarshal in every sector; rookie holds Cadet in every sector.
//
// Demonstrates: auth.persona.
//
//go:embed cast.json
var castFile []byte

// Cast reads the cast file.
func Cast() (*cast.Cast, error) {
	read, err := cast.ReadCast(castFile)
	if err != nil {
		return nil, errors.Wrap(err, "cast.ReadCast(): cast.json")
	}

	return read, nil
}

// SeedCast seeds the cast file this package embeds (SeedCastFile over cast.json): the
// application calls it at start where the persona method is registered, and out is
// where it says each role it gives.
func (a *Auth) SeedCast(ctx context.Context, out io.Writer) (int, error) {
	return a.SeedCastFile(ctx, out, castFile)
}

// SeedCastFile seeds the cast a cast file declares, read and checked as cast.json is
// (cast.ReadCast): a password-less crew login under each persona's login, with the
// persona's name and address, holding the roles its entry names, a global role in the
// global partition and a domain role in every sector, as the administrator roles are; a
// persona its entry names a super administrator holds the framework's role
// (accesstypes.SuperAdministrator) in the global partition. A role is held by the id of
// an account, which is how the permission store names a person, so the seed chooses the
// id of a persona's account (session.SeedUserID, the same on every run and in every
// instance), writes the roles under it, and creates the login under it last: a run that
// stops between the two is repeated at the next start, and a login another instance made
// meanwhile carries the same id and the same roles.
//
// A persona seeded earlier is brought up to its entry: the account under the id the seed
// chose, whatever login it carries now, is given every role the entry names that it does
// not hold, so a role added to the cast reaches the environments seeded before it at the
// next start, and a persona added to the cast arrives then too. Nothing is taken: a role
// that left the cast stays on the account until somebody removes it. A role the account
// holds now, of its own or through a group, is left as it is, the period it is held for
// included, so every start seeds the cast again and a run over an unchanged cast writes
// nothing; a role it held for a period that has ended or has not begun is not held, and
// is given with no period. A login of another id under a persona's name is not the
// seed's: it is given nothing, since the seed never gives a role to an account because
// of the name it carries.
//
// It says each role it gives on out, one line each, and nothing where there is nothing to
// give, and answers how many logins it created; with no writer it refuses before it reads
// or writes anything (io.Discard says nothing). The persona sign-in links the provider's
// identity to the login of the same name at the first sign-in (the method is invite
// only, as every sign-in method starts, and declares seeded users), so a persona the
// seed never made is refused there. SeedCast is this over the package's own cast file; a
// suite that changes a cast between two runs hands it each.
func (a *Auth) SeedCastFile(ctx context.Context, out io.Writer, file []byte) (int, error) {
	if out == nil {
		return 0, errors.New("the cast's seed was handed no writer to say the roles it gives on: hand it the command's output, or io.Discard to say nothing")
	}
	declared, err := cast.ReadCast(file)
	if err != nil {
		return 0, errors.Wrap(err, "cast.ReadCast()")
	}
	roles, err := Roles().Parse()
	if err != nil {
		return 0, errors.Wrap(err, "access.RoleFile.Parse()")
	}
	api := a.handlers.API()
	created := 0
	for i := range declared.Personas {
		p := &declared.Personas[i]
		id := session.SeedUserID(Name, p.Login)
		seeded, taken, err := a.personaAccount(ctx, p.Login, id)
		if err != nil {
			return created, err
		}
		if taken {
			continue
		}
		if err := a.seatPersona(ctx, out, roles, p, id); err != nil {
			return created, err
		}
		if seeded {
			continue
		}
		req := &session.CreateUserRequest{ID: impulse.NullUUIDFromUUID(id), Username: p.Login}
		if p.Email != "" {
			req.Email = &p.Email
		}
		if p.Name != "" {
			req.Name = &p.Name
		}
		if _, err := api.CreateSessionUser(ctx, req); err != nil {
			// Another instance may have made the login between the read and the create,
			// under the same id and with the same roles: the persona is seeded. A
			// failure for any other reason is the seed's.
			if _, found := api.UserByUsername(ctx, p.Login); found != nil {
				return created, errors.Wrapf(err, "session.AuthAPI.CreateSessionUser(): %s", p.Login)
			}

			continue
		}
		created++
	}

	return created, nil
}

// personaAccount reports what stands where a persona's account goes: seeded, when the
// account the seed made for it exists, the one under the id the seed chose, under the
// persona's login or, for a persona renamed since, under another; and taken, when there
// is no such account and a login of another id holds the persona's name, an account the
// seed did not make and leaves as it is.
func (a *Auth) personaAccount(ctx context.Context, login string, id impulse.UUID) (seeded, taken bool, err error) {
	api := a.handlers.API()
	if _, err := api.User(ctx, id); err == nil {
		return true, false, nil
	} else if !httpio.HasNotFound(err) {
		return false, false, errors.Wrapf(err, "session.AuthAPI.User(): the account the seed chose for %s", login)
	}
	if _, err := api.UserByUsername(ctx, login); err == nil {
		return false, true, nil
	} else if !httpio.HasNotFound(err) {
		return false, false, errors.Wrapf(err, "session.AuthAPI.UserByUsername(): %s", login)
	}

	return false, false, nil
}

// seatPersona brings a persona's account up to the roles its entry names, each role
// where the role file holds it: a global role in the global partition, a domain role in
// every sector. The Super Administrator role is the framework's, in no role file, and is
// held in the global partition. Any other role the file does not define is refused,
// since the seed names nothing the release does not.
func (a *Auth) seatPersona(ctx context.Context, out io.Writer, roles *access.RoleConfig, p *cast.Persona, userID impulse.UUID) error {
	var global, everyDomain []accesstypes.Role
	for _, name := range p.Roles {
		role := accesstypes.Role(name)
		switch {
		case role == accesstypes.SuperAdministrator, definesRole(roles.Roles.Global, role):
			global = append(global, role)
		case definesRole(roles.Roles.Domain, role):
			everyDomain = append(everyDomain, role)
		default:
			return errors.Newf("cast.json: the persona %s holds the role %q, which the %s auth's role file does not define", p.Login, name, Name)
		}
	}
	user := accesstypes.User(userID.String())
	if err := a.givePersona(ctx, out, p.Login, user, accesstypes.GlobalPolicyScope(), global, ""); err != nil {
		return err
	}

	return a.givePersona(ctx, out, p.Login, user, accesstypes.EveryDomainPolicyScope(), everyDomain, " in every sector")
}

// givePersona gives a persona's account the roles its entry names in one place that the
// account does not hold there now, and says each on out, a domain role with the words
// that it is held in every sector. A role the account holds, of its own or through a
// group, is not written again, so it keeps the period it is held for; nothing the entry
// does not name is touched.
func (a *Auth) givePersona(ctx context.Context, out io.Writer, login string, user accesstypes.User, scope accesstypes.PolicyScope, named []accesstypes.Role, where string) error {
	if len(named) == 0 {
		return nil
	}
	manager := a.access.UserManager()
	held, err := manager.UserRoles(ctx, user, scope)
	if err != nil {
		return errors.Wrapf(err, "access.UserManager.UserRoles(): %s", login)
	}
	holds := held[scope]
	for _, role := range named {
		if slices.Contains(holds, role) {
			continue
		}
		if err := manager.AddUserRoles(ctx, scope, user, role); err != nil {
			return errors.Wrapf(err, "access.UserManager.AddUserRoles(): %s, the role %s%s", login, role, where)
		}
		holds = append(holds, role)
		fmt.Fprintf(out, "Gave the %s auth's persona %s the role %s%s\n", Name, login, role, where)
	}

	return nil
}

// definesRole reports whether the role file defines the role at the scope.
func definesRole(defined []*access.Role, role accesstypes.Role) bool {
	for _, r := range defined {
		if r.Name == role {
			return true
		}
	}

	return false
}
