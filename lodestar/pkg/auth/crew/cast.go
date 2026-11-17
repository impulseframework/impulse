package crew

import (
	"context"
	_ "embed" // the cast rides in the binary

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
	cast, err := cast.ReadCast(castFile)
	if err != nil {
		return nil, errors.Wrap(err, "cast.ReadCast(): cast.json")
	}

	return cast, nil
}

// SeedCast seeds the cast: a password-less crew login under each persona's login, with
// the persona's name and address, seated in the roles the cast names, a global role in the global
// partition and a domain role in every sector, as the administrator roles are. A persona
// whose login exists is left as it is, whatever it holds, so every start seeds the cast
// again harmlessly and a persona added to the cast arrives at the next one. A role is
// held by the id of an account, which is how the permission store names a person, so the
// seed chooses the id of a persona's account (session.SeedUserID, the same on every run
// and in every instance), writes the roles under it, and creates the login under it
// last: a run that stops between the two is repeated at the next start, a login another
// instance made meanwhile carries the same id and the same roles, and the seed never
// gives a role to an account it did not create. The persona sign-in links the provider's
// identity to the login of the same name at the first sign-in (the method is invite only,
// as every sign-in method starts), so a persona the seed never made is refused there. It
// answers how many logins it created.
func (a *Auth) SeedCast(ctx context.Context) (int, error) {
	cast, err := Cast()
	if err != nil {
		return 0, err
	}
	roles, err := Roles().Parse()
	if err != nil {
		return 0, errors.Wrap(err, "access.RoleFile.Parse()")
	}
	api := a.handlers.API()
	created := 0
	for i := range cast.Personas {
		p := &cast.Personas[i]
		id := session.SeedUserID(Name, p.Login)
		seeded, err := a.personaSeeded(ctx, p.Login, id)
		if err != nil {
			return created, err
		}
		if seeded {
			continue
		}
		if err := a.seatPersona(ctx, roles, p, id); err != nil {
			return created, err
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

// personaSeeded reports whether a persona needs no seeding: a login holds its name, or
// the account the seed made for it exists under another name (a persona renamed since,
// which keeps the id the seed chose). Either is left as it is.
func (a *Auth) personaSeeded(ctx context.Context, login string, id impulse.UUID) (bool, error) {
	api := a.handlers.API()
	if _, err := api.UserByUsername(ctx, login); err == nil {
		return true, nil
	} else if !httpio.HasNotFound(err) {
		return false, errors.Wrapf(err, "session.AuthAPI.UserByUsername(): %s", login)
	}
	if _, err := api.User(ctx, id); err == nil {
		return true, nil
	} else if !httpio.HasNotFound(err) {
		return false, errors.Wrapf(err, "session.AuthAPI.User(): the account the seed chose for %s", login)
	}

	return false, nil
}

// seatPersona writes a persona's memberships, held by the id of its login's account,
// each role where the role file holds it: a global role in the global partition, a
// domain role in every sector. The Super Administrator role is the framework's, in no
// role file, and is held in the global partition. Any other role the file does not
// define is refused, since the seed names nothing the release does not.
func (a *Auth) seatPersona(ctx context.Context, roles *access.RoleConfig, p *cast.Persona, userID impulse.UUID) error {
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
	manager := a.access.UserManager()
	user := accesstypes.User(userID.String())
	if len(global) > 0 {
		if err := manager.AddUserRoles(ctx, accesstypes.GlobalPolicyScope(), user, global...); err != nil {
			return errors.Wrapf(err, "access.UserManager.AddUserRoles(): %s in the global partition", p.Login)
		}
	}
	if len(everyDomain) > 0 {
		if err := manager.AddUserRoles(ctx, accesstypes.EveryDomainPolicyScope(), user, everyDomain...); err != nil {
			return errors.Wrapf(err, "access.UserManager.AddUserRoles(): %s in every sector", p.Login)
		}
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
