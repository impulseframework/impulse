import { ApiDescriptor, DirectoryOnlyState, Method, Resource } from '@impulseframework/resource';
import { pageDataKey } from '@impulseframework/resource-angular/auth-frame';
import {
  TenantSignIn,
  tenantSignInKey,
  tenantPeopleLine,
  tenantSignInPage,
  tenantSignInRoute,
  tenantStandsLine,
} from './tenant-sign-in';

// A tenant's Sign-in settings page is placed from the API's description alone: the
// tenant's side of an Auth names the directories record and the tenant's methods that
// set how its people enter. The page's first line and its line on the people the
// directory-only switch keeps out are said from the read of the switch, as two tables.

const side = (scope: 'global' | 'domain', lists: Record<string, string>, methods: Record<string, string> = {}) => ({
  scope,
  lists: { users: `Staff${scope === 'domain' ? 'Team' : 'Session'}Users`, ...lists },
  methods,
});

const methods = {
  openToDirectory: 'OpenTeamToDirectory',
  makeDirectoryInviteOnly: 'MakeTeamInviteOnly',
  makeDirectoryOnly: 'MakeTeamDirectoryOnly',
  endDirectoryOnly: 'EndTeamDirectoryOnly',
};

/** A description with a tenant whose record declares a directory, an Auth without a tenant's side, and one whose tenants have none. */
const descriptor: ApiDescriptor = {
  resources: {},
  methods: {},
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  domainRoute: { segment: 'teams', param: 'teamID' },
  identity: [
    {
      auth: 'Staff',
      label: 'Staff',
      tenant: 'Team',
      tenantResource: 'Teams',
      sides: [side('global', {}), side('domain', { directories: 'TeamDirectories' }, methods)],
    },
    { auth: 'Members', label: 'Members', sides: [side('global', {})] },
    { auth: 'Guests', label: 'Guests', tenant: 'Team', sides: [side('global', {}), side('domain', {})] },
  ],
};

describe('tenantSignInPage and tenantSignInRoute', () => {
  it("place the page under the tenant segment, needing List on the tenant's directories", () => {
    const placed = tenantSignInPage(descriptor, { auth: 'Staff' });

    expect(placed).toEqual({
      label: 'Sign-in settings',
      icon: 'login',
      route: ['teams/:teamID', 'sign-in'],
      permission: { resource: 'TeamDirectories', permission: 'List' },
    });
  });

  it("answer the same page object for the same description and Auth, and the route carries it with the record the tenant's people are rows of", () => {
    const placed = tenantSignInPage(descriptor, { auth: 'Staff' });
    const route = tenantSignInRoute(descriptor, { auth: 'Staff' });

    expect(tenantSignInPage(descriptor, { auth: 'Staff' })).toBe(placed);
    expect(route.path).toBe('sign-in');
    expect(route.canActivate).toBeUndefined();
    expect(route.data?.[pageDataKey]).toBe(placed);
    const carried: TenantSignIn = {
      word: 'team',
      directories: 'TeamDirectories' as Resource,
      people: 'StaffTeamUsers' as Resource,
      methods: {
        openToDirectory: 'OpenTeamToDirectory' as Method,
        makeDirectoryInviteOnly: 'MakeTeamInviteOnly' as Method,
        makeDirectoryOnly: 'MakeTeamDirectoryOnly' as Method,
        endDirectoryOnly: 'EndTeamDirectoryOnly' as Method,
      },
    };
    expect(route.data?.[tenantSignInKey]).toEqual(carried);
  });

  it('refuse an Auth the description does not carry', () => {
    expect(() => tenantSignInPage(descriptor, { auth: 'Visitors' })).toThrowError(
      'tenantSignInPage: the API description carries no generated management of the auth Visitors (it carries: Staff, Members, Guests); the auth is named as GenerateIdentity names it, and its management is served on the outlets that declare or manage it',
    );
    expect(() => tenantSignInRoute(descriptor, { auth: 'Visitors' })).toThrowError(/^tenantSignInPage: /);
  });

  it("refuse an Auth with no tenant's side", () => {
    expect(() => tenantSignInPage(descriptor, { auth: 'Members' })).toThrowError(
      "tenantSignInPage: the generated management of Members has no tenant's side on this API",
    );
  });

  it("refuse a tenant's side that names no directories record", () => {
    expect(() => tenantSignInRoute(descriptor, { auth: 'Guests' })).toThrowError(
      "tenantSignInPage: the description names no directories record on the tenant's side of Guests: this application's tenants have no directories, so there is no Sign-in settings page to place",
    );
  });

  it('refuse a description that declares no tenant segment', () => {
    const withoutSegment: ApiDescriptor = { ...descriptor, domainRoute: undefined };

    expect(() => tenantSignInPage(withoutSegment, { auth: 'Staff' })).toThrowError(
      "tenantSignInPage: a tenant's pages of Staff live under the API's tenant segment, and the API description declares none (domainRoute)",
    );
  });
});

describe('tenantStandsLine', () => {
  const said = (facts: DirectoryOnlyState | undefined, lists = false, row = false): string =>
    tenantStandsLine({ word: 'team', name: 'Riverside', facts, lists, row });
  const off = { directoryOnly: false };
  const workspace = 'Google Workspace riverside.example';

  it('says every row of the table', () => {
    expect(said({ ...off, passwords: true })).toBe(
      'People enter Riverside with a password or by invitation. No directory is set.',
    );
    expect(said(off)).toBe('People enter Riverside by invitation. No directory is set.');
    expect(said({ ...off, directory: workspace, passwords: true })).toBe(
      'People enter Riverside through Google Workspace riverside.example, with a password or by invitation.',
    );
    expect(said({ ...off, directory: workspace })).toBe(
      'People enter Riverside through Google Workspace riverside.example or by invitation.',
    );
    expect(said({ directoryOnly: true, directory: workspace, passwords: true })).toBe(
      'People enter Riverside through Google Workspace riverside.example only.',
    );
    expect(said(undefined, true, true)).toBe('This team has a directory.');
    expect(said(undefined, true, false)).toBe('No directory is set.');
    expect(said(undefined)).toBe('Your role in this team does not cover its sign-in settings.');
  });
});

describe('tenantPeopleLine', () => {
  it('says every row of the table, with the switch off and on', () => {
    expect(tenantPeopleLine({ seated: 4, outside: 0 }, false)).toBe('Nobody here would be kept out.');
    expect(tenantPeopleLine({ seated: 4, outside: 0 }, true)).toBe('Nobody here is kept out.');
    expect(tenantPeopleLine({ seated: 1, outside: 1 }, false)).toBe(
      'The one person here has never signed in through the directory and would be kept out.',
    );
    expect(tenantPeopleLine({ seated: 1, outside: 1 }, true)).toBe(
      'The one person here has never signed in through the directory and is kept out.',
    );
    expect(tenantPeopleLine({ seated: 4, outside: 1 }, false)).toBe(
      '1 of 4 people here has never signed in through the directory and would be kept out.',
    );
    expect(tenantPeopleLine({ seated: 4, outside: 1 }, true)).toBe(
      '1 of 4 people here has never signed in through the directory and is kept out.',
    );
    expect(tenantPeopleLine({ seated: 4, outside: 3 }, false)).toBe(
      '3 of 4 people here have never signed in through the directory and would be kept out.',
    );
    expect(tenantPeopleLine({ seated: 4, outside: 3 }, true)).toBe(
      '3 of 4 people here have never signed in through the directory and are kept out.',
    );
  });
});
