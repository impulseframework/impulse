import { Route } from '@angular/router';
import { ApiDescriptor, Method, Resource } from '@impulseframework/resource';
import { Page, page, pageRoute } from '@impulseframework/resource-angular/auth-frame';
import { ListPermission, nameInLowerWords } from '@impulseframework/resource-angular/types';
import { TenantSignIn, tenantSignInKey } from './tenant-sign-in-says';

export { tenantSignInKey, tenantStandsLine, tenantPeopleLine, tenantShareLine } from './tenant-sign-in-says';
export type { TenantSignIn, TenantStands } from './tenant-sign-in-says';

// A tenant's Sign-in settings page, built from the API's own description.
//
// A tenant whose record declares a directory has one: the organization its people sign in
// through, a row of the tenant's directories record, set from a sign-in through it. The
// tenant's side of an Auth's generated management names that record and the tenant's
// methods that say how the directory's people enter: the role a person gets the first
// time they sign in through it, whether the tenant is directory only, and whether the
// tenant vouches for its directory's own second factor. These functions place the page
// that shows and sets them; the page reads the rest at run time, from the read of the
// tenant's sign-in settings and the digest of the tenant.

/** Which Auth's tenant side the Sign-in settings page is for. */
export interface TenantSignInOptions {
  /** The Auth, by the name the description carries (Staff). */
  auth: string;
}

/** The page and what its route carries, made once for each description and Auth. */
interface BuiltSignIn {
  page: Page;
  signIn: TenantSignIn;
}

// The pages made for each description and Auth, so the route and the menu file's page of
// one request are the same page.
const made = new WeakMap<ApiDescriptor, Map<string, BuiltSignIn>>();

/**
 * The Sign-in settings page of a tenant, for the application's menu file to place: under
 * the API's tenant segment, needing List on the tenant's directories. The same
 * description and Auth answer the same page. Refused, naming what is missing: an Auth the
 * description does not carry, an Auth with no tenant's side, and a tenant's side that
 * names no directories record.
 */
export function tenantSignInPage(descriptor: ApiDescriptor, options: TenantSignInOptions): Page {
  return builtSignIn(descriptor, options).page;
}

/**
 * The route of a tenant's Sign-in settings page, carrying the page (tenantSignInPage) and
 * what the page reads. Put it among the children of the tenant segment's route; it has no
 * guard of its own, since the page says what a person's role does not cover.
 */
export function tenantSignInRoute(descriptor: ApiDescriptor, options: TenantSignInOptions): Route {
  const built = builtSignIn(descriptor, options);
  return pageRoute(built.page, {
    path: 'sign-in',
    data: { [tenantSignInKey]: built.signIn },
    loadComponent: () => import('./tenant-sign-in-page.component').then((mod) => mod.TenantSignInPageComponent),
  });
}

/** The page of one request, made once (see `made`). */
function builtSignIn(descriptor: ApiDescriptor, options: TenantSignInOptions): BuiltSignIn {
  const requests = made.get(descriptor) ?? new Map<string, BuiltSignIn>();
  made.set(descriptor, requests);
  const known = requests.get(options.auth);
  if (known) {
    return known;
  }
  const built = buildSignIn(descriptor, options);
  requests.set(options.auth, built);
  return built;
}

function buildSignIn(descriptor: ApiDescriptor, options: TenantSignInOptions): BuiltSignIn {
  const identity = (descriptor.identity ?? []).find((candidate) => candidate.auth === options.auth);
  if (!identity) {
    const known = (descriptor.identity ?? []).map((candidate) => candidate.auth).join(', ') || 'none';
    throw new Error(
      `tenantSignInPage: the API description carries no generated management of the auth ${options.auth} (it carries: ${known}); the auth is named as GenerateIdentity names it, and its management is served on the outlets that declare or manage it`,
    );
  }
  const side = identity.sides.find((candidate) => candidate.scope === 'domain');
  if (!side) {
    throw new Error(`tenantSignInPage: the generated management of ${options.auth} has no tenant's side on this API`);
  }
  const directories = side.lists.directories;
  if (!directories) {
    throw new Error(
      `tenantSignInPage: the description names no directories record on the tenant's side of ${options.auth}: this application's tenants have no directories, so there is no Sign-in settings page to place`,
    );
  }
  const route = descriptor.domainRoute;
  if (!route) {
    throw new Error(
      `tenantSignInPage: a tenant's pages of ${options.auth} live under the API's tenant segment, and the API description declares none (domainRoute)`,
    );
  }
  const {
    openToDirectory,
    makeDirectoryInviteOnly,
    makeDirectoryOnly,
    endDirectoryOnly,
    vouchForDirectorySecondFactor,
    stopVouchingForDirectorySecondFactor,
  } = side.methods;
  return {
    page: page({
      label: 'Sign-in settings',
      icon: 'login',
      route: [`${route.segment}/:${route.param}`, 'sign-in'],
      permission: { resource: directories as Resource, permission: ListPermission },
    }),
    signIn: {
      word: identity.tenant ? nameInLowerWords(identity.tenant) : 'tenant',
      directories: directories as Resource,
      ...(side.lists.users ? { people: side.lists.users as Resource } : {}),
      ...(identity.tenantResource && identity.tenantDirectoryOnlyField
        ? {
            tenantRecord: {
              resource: identity.tenantResource as Resource,
              directoryOnlyField: identity.tenantDirectoryOnlyField,
            },
          }
        : {}),
      methods: {
        ...(openToDirectory ? { openToDirectory: openToDirectory as Method } : {}),
        ...(makeDirectoryInviteOnly ? { makeDirectoryInviteOnly: makeDirectoryInviteOnly as Method } : {}),
        ...(makeDirectoryOnly ? { makeDirectoryOnly: makeDirectoryOnly as Method } : {}),
        ...(endDirectoryOnly ? { endDirectoryOnly: endDirectoryOnly as Method } : {}),
        ...(vouchForDirectorySecondFactor
          ? { vouchForDirectorySecondFactor: vouchForDirectorySecondFactor as Method }
          : {}),
        ...(stopVouchingForDirectorySecondFactor
          ? { stopVouchingForDirectorySecondFactor: stopVouchingForDirectorySecondFactor as Method }
          : {}),
      },
    },
  };
}
