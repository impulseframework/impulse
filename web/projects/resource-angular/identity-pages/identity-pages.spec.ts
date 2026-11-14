import { ApiDescriptor, IdentityDescriptor, Resource } from '@impulseframework/resource';
import { FieldMeta, ListViewConfig, MethodPresentation, RecordData, ResourceMeta, RootConfig } from '@impulseframework/resource-angular/types';
import { identityPages, identityRoutes } from './identity-pages';

// The pages over an Auth's people are built from the API's description alone: which
// pages, their routes and words, the columns the Auth's schema carries, the methods its
// sign-in methods give, and how each method is presented and filled.

/** A resource's metadata with the named fields; `id` is its key where it has one. */
const fields = (...names: string[]): ResourceMeta =>
  ({
    route: 'r',
    fields: names.map(
      (fieldName) =>
        ({
          fieldName,
          displayType: 'string',
          required: false,
          isIndex: false,
          ...(fieldName === 'id' ? { primaryKey: { ordinalPosition: 0 } } : {}),
        }) as FieldMeta,
    ),
  }) as ResourceMeta;

const metas: Record<string, ResourceMeta> = {
  CrewSessionUsers: fields('id', 'username', 'email', 'emailVerifiedAt', 'disabled', 'passwordChangeRequired', 'createdAt', 'lastSignInAt'),
  CrewUserRoles: fields('kind', 'axis', 'domain', 'role', 'user', 'createdAt'),
  CrewSessionIdentities: fields('id', 'userId', 'method', 'connection', 'emailAtLink', 'createdAt', 'lastUsedAt'),
  CrewSessionMailLinks: fields('id', 'kind', 'email', 'tenant', 'roleNames', 'createdAt', 'expiresAt', 'consumedAt'),
  CrewSessions: fields('id', 'userId', 'username', 'expired', 'authenticatedAt', 'updatedAt'),
  CrewSessionSignInAttempts: fields('name', 'failures', 'lockedUntil', 'updatedAt'),
  CrewSectorUsers: fields('id', 'domain', 'username', 'email', 'disabled', 'passwordChangeRequired', 'lastSignInAt', 'heldElsewhere', 'heldGlobally'),
  CrewSectorUserRoles: fields('domain', 'userId', 'username', 'role', 'everyDomain', 'createdAt'),
  CrewSectorIdentities: fields('id', 'userId', 'username', 'method', 'connection', 'emailAtLink', 'createdAt', 'lastUsedAt'),
  CrewSectorInvitations: fields('id', 'email', 'roleNames', 'createdAt', 'expiresAt', 'pending'),
  // An Auth whose username is the address carries no email column, and whose directory admits its people no password.
  MembersSessionUsers: fields('id', 'username', 'disabled', 'passwordChangeRequired', 'lastSignInAt'),
  MembersUserRoles: fields('kind', 'axis', 'domain', 'role', 'user', 'createdAt'),
};
const resourceMeta = (resource: Resource): ResourceMeta => metas[resource];

const crew: IdentityDescriptor = {
  auth: 'Crew',
  label: 'Crew',
  tenant: 'Sector',
  sides: [
    {
      scope: 'global',
      lists: {
        users: 'CrewSessionUsers',
        roles: 'CrewUserRoles',
        identities: 'CrewSessionIdentities',
        invitations: 'CrewSessionMailLinks',
        sessions: 'CrewSessions',
        signInAttempts: 'CrewSessionSignInAttempts',
      },
      methods: {
        create: 'CreateCrewUser',
        invite: 'InviteCrewUser',
        withdrawInvitation: 'WithdrawCrewInvitation',
        assignRole: 'AssignCrewRole',
        removeRole: 'RemoveCrewRole',
        disable: 'DisableCrewUser',
        enable: 'EnableCrewUser',
        delete: 'DeleteCrewUser',
        rename: 'RenameCrewUser',
        setEmail: 'SetCrewUserEmail',
        setTemporaryPassword: 'SetCrewTemporaryPassword',
        requirePasswordChange: 'RequireCrewPasswordChange',
        endUserSessions: 'EndCrewUserSessions',
        revokeTrustedDevices: 'RevokeCrewUserTrustedDevices',
        unlinkIdentity: 'UnlinkCrewSessionIdentity',
        endSession: 'EndCrewSession',
        unlockSignIn: 'UnlockCrewSignIn',
      },
    },
    {
      scope: 'domain',
      lists: {
        users: 'CrewSectorUsers',
        roles: 'CrewSectorUserRoles',
        identities: 'CrewSectorIdentities',
        invitations: 'CrewSectorInvitations',
      },
      methods: {
        create: 'CreateCrewSectorUser',
        invite: 'InviteCrewSectorUser',
        withdrawInvitation: 'WithdrawCrewSectorInvitation',
        assignRole: 'AssignCrewSectorRole',
        removeRole: 'RemoveCrewSectorRole',
        disable: 'DisableCrewSectorUser',
        enable: 'EnableCrewSectorUser',
        delete: 'DeleteCrewSectorUser',
        unlinkIdentity: 'UnlinkCrewSectorSessionIdentity',
      },
    },
  ],
};
// A directory writes this Auth's roles and admits its people: nobody is created, invited or given a role by hand.
const members: IdentityDescriptor = {
  auth: 'Members',
  label: 'Members',
  sides: [
    {
      scope: 'global',
      lists: { users: 'MembersSessionUsers', roles: 'MembersUserRoles' },
      methods: { disable: 'DisableMembersUser', enable: 'EnableMembersUser', endUserSessions: 'EndMembersUserSessions' },
    },
  ],
};
const descriptor = {
  resources: {},
  methods: {},
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  domainRoute: { segment: 'sectors', param: 'sectorID' },
  identity: [crew, members],
} as ApiDescriptor;

const list = (config: RootConfig): ListViewConfig => config.parentConfig as ListViewConfig;
const columns = (config: ListViewConfig): [string, string | undefined][] => config.listColumns.map((column) => [column.id, column.header]);
const methodsOf = (presentations: false | MethodPresentation[] | undefined): [string, string | undefined][] =>
  (presentations || []).map((p) => [p.method, p.label]);
const presentation = (presentations: false | MethodPresentation[] | undefined, method: string): MethodPresentation => {
  const found = (presentations || []).find((p) => p.method === method);
  if (!found) {
    throw new Error(`no presentation of ${method}`);
  }
  return found;
};
const children = (config: RootConfig): ListViewConfig[] => config.relatedConfigs as ListViewConfig[];

describe("identityPages, the whole application's side", () => {
  const pages = identityPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'global' });
  const [people, invitations, sessions, attempts] = pages;

  it('builds the accounts, the mailed links, the sessions and the failed sign-ins, each at a route named for the auth, in one group', () => {
    expect(pages.map((page) => [page.routeData.route, page.nav.navItem.label, page.nav.group])).toEqual([
      ['crew-accounts', 'Accounts', 'Crew accounts'],
      ['crew-mailed-links', 'Mailed links', 'Crew accounts'],
      ['crew-sessions', 'Sessions', 'Crew accounts'],
      ['crew-failed-sign-ins', 'Failed sign-ins', 'Crew accounts'],
    ]);
    expect(pages.map((page) => page.routeData.hasViewRoute)).toEqual([true, false, false, false]);
    expect(pages.map((page) => list(page).primaryResource)).toEqual(['CrewSessionUsers', 'CrewSessionMailLinks', 'CrewSessions', 'CrewSessionSignInAttempts']);
  });

  it('lists the accounts by the columns the schema carries and offers each method under plain words', () => {
    expect(columns(list(people))).toEqual([
      ['username', 'Username'],
      ['email', 'Email'],
      ['disabled', 'Disabled'],
      ['passwordChangeRequired', 'Must change password'],
      ['lastSignInAt', 'Last sign-in'],
    ]);
    expect(methodsOf(list(people).rowMethods)).toEqual([
      ['AssignCrewRole', 'Give a role'],
      ['RenameCrewUser', 'Rename'],
      ['SetCrewUserEmail', 'Change the email address'],
      ['SetCrewTemporaryPassword', 'Set a temporary password'],
      ['RequireCrewPasswordChange', 'Require a new password'],
      ['EndCrewUserSessions', 'Sign out everywhere'],
      ['RevokeCrewUserTrustedDevices', 'Forget trusted devices'],
      ['DisableCrewUser', 'Disable'],
      ['EnableCrewUser', 'Enable'],
      ['DeleteCrewUser', 'Delete'],
    ]);
    expect(methodsOf(list(people).listMethods)).toEqual([
      ['CreateCrewUser', 'Add an account'],
      ['InviteCrewUser', 'Invite'],
    ]);
  });

  it('holds Enable to a disabled account and Disable to an enabled one, and asks before each', () => {
    const disable = presentation(list(people).rowMethods, 'DisableCrewUser');
    const enable = presentation(list(people).rowMethods, 'EnableCrewUser');
    const ada: RecordData = { username: 'ada', disabled: false };
    expect([disable.when?.(ada), enable.when?.(ada)]).toEqual([true, false]);
    expect([disable.when?.({ ...ada, disabled: true }), enable.when?.({ ...ada, disabled: true })]).toEqual([false, true]);
    expect((disable.confirm as (row: RecordData) => string)(ada)).toBe('Disable ada? They cannot sign in until the account is enabled again.');
  });

  it("fills the person's name in the method that gives a role, which names no target row, and names the tenant in the application's word", () => {
    const give = presentation(list(people).rowMethods, 'AssignCrewRole');
    expect(give.fill?.({ username: 'ada', id: 'u-1' })).toEqual({ username: 'ada' });
    expect(give.labels).toEqual({ domain: 'Sector', everyDomain: 'In every sector' });
    expect(presentation(list(people).listMethods, 'CreateCrewUser').labels).toEqual({ domain: 'Sector', everyDomain: 'In every sector' });
  });

  it("draws a person's row page from the account's fields, with the roles, the identities and the sessions under it", () => {
    const section = list(people).elements[0] as unknown as { label: string; children: { name: string; label: string }[] };
    expect(section.label).toBe('Account');
    expect(section.children.map((child) => [child.name, child.label])).toEqual([
      ['username', 'Username'],
      ['email', 'Email'],
      ['disabled', 'Disabled'],
      ['passwordChangeRequired', 'Must change password'],
      ['lastSignInAt', 'Last sign-in'],
      ['emailVerifiedAt', 'Email verified'],
      ['createdAt', 'Created'],
    ]);
    expect(children(people).map((child) => [child.title, child.primaryResource, child.parentRelation, child.showViewButton])).toEqual([
      ['Roles held', 'CrewUserRoles', { parentKey: 'username', childKey: 'user' }, false],
      ['Sign-in identities', 'CrewSessionIdentities', { parentKey: 'id', childKey: 'userId' }, false],
      ['Sessions', 'CrewSessions', { parentKey: 'id', childKey: 'userId' }, false],
    ]);
  });

  it('says where each role is held and fills the method that takes it away from the row', () => {
    const roles = children(people)[0];
    expect(columns(roles)).toEqual([
      ['role', 'Role'],
      ['kind', 'Held in'],
      ['createdAt', 'Given'],
    ]);
    const heldIn = roles.listColumns[1].valueGetter as (row: RecordData) => string;
    const remove = presentation(roles.rowMethods, 'RemoveCrewRole');
    const cases: { row: RecordData; where: string; body: Record<string, unknown> }[] = [
      {
        row: { user: 'ada', role: 'Clerk', kind: 'global', domain: '' },
        where: 'The whole application',
        body: { username: 'ada', role: 'Clerk', domain: undefined, everyDomain: false },
      },
      {
        row: { user: 'ada', role: 'Cadet', kind: 'domain', domain: 'anvil' },
        where: 'Sector anvil',
        body: { username: 'ada', role: 'Cadet', domain: 'anvil', everyDomain: false },
      },
      {
        row: { user: 'ada', role: 'Marshal', kind: 'every', domain: '' },
        where: 'Every sector',
        body: { username: 'ada', role: 'Marshal', domain: undefined, everyDomain: true },
      },
    ];
    for (const tt of cases) {
      expect(heldIn(tt.row)).toBe(tt.where);
      expect(remove.fill?.(tt.row)).toEqual(tt.body);
    }
    expect((remove.confirm as (row: RecordData) => string)(cases[1].row)).toBe('Take Cadet (sector anvil) away from ada?');
  });

  it('offers Withdraw on a mailed link that was not used, End the session on a session, and Unlock on a counted name', () => {
    const withdraw = presentation(list(invitations).rowMethods, 'WithdrawCrewInvitation');
    expect([withdraw.when?.({ consumedAt: null }), withdraw.when?.({ consumedAt: '2026-01-01T00:00:00Z' })]).toEqual([true, false]);
    expect(list(invitations).listMethods).toEqual([]);
    expect(columns(list(invitations)).map(([, header]) => header)).toEqual(['Kind', 'Email', 'Sector', 'Roles', 'Sent', 'Expires', 'Used']);
    expect(methodsOf(list(sessions).rowMethods)).toEqual([['EndCrewSession', 'End the session']]);
    expect(columns(list(sessions)).map(([id]) => id)).toEqual(['username', 'authenticatedAt', 'updatedAt', 'expired']);
    expect(methodsOf(list(attempts).rowMethods)).toEqual([['UnlockCrewSignIn', 'Unlock']]);
    expect(methodsOf(children(people)[2].rowMethods)).toEqual([['EndCrewSession', 'End the session']]);
  });
});

describe("identityPages, a tenant's side", () => {
  const pages = identityPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'domain' });
  const [people, invitations] = pages;

  it("builds the people and the invitations under the API's tenant segment, in a group named for the tenant record", () => {
    expect(pages.map((page) => [page.routeData.route, page.nav.navItem.label, page.nav.group])).toEqual([
      ['sectors/:sectorID/crew-people', 'People', 'Sector crew'],
      ['sectors/:sectorID/crew-invitations', 'Invitations', 'Sector crew'],
    ]);
  });

  it('names the tenant record in its words and leaves out what the side does not have', () => {
    expect(columns(list(people)).at(-1)).toEqual(['heldElsewhere', 'Also in another sector']);
    // No password method on this side of the description: no column for it.
    expect(columns(list(people)).map(([id]) => id)).toEqual(['username', 'email', 'disabled', 'lastSignInAt', 'heldElsewhere']);
    expect(methodsOf(list(people).rowMethods)).toEqual([
      ['AssignCrewSectorRole', 'Give a role'],
      ['DisableCrewSectorUser', 'Disable'],
      ['EnableCrewSectorUser', 'Enable'],
      ['DeleteCrewSectorUser', 'Delete'],
    ]);
    expect(methodsOf(list(people).listMethods)).toEqual([
      ['CreateCrewSectorUser', 'Add a person'],
      ['InviteCrewSectorUser', 'Invite'],
    ]);
  });

  it("leaves a tenant's methods to their target rows: nothing is filled", () => {
    expect(presentation(list(people).rowMethods, 'AssignCrewSectorRole').fill).toBeUndefined();
    const roles = children(people)[0];
    expect(presentation(roles.rowMethods, 'RemoveCrewSectorRole').fill).toBeUndefined();
    expect([roles.title, roles.parentRelation]).toEqual(['Roles held here', { parentKey: 'id', childKey: 'userId' }]);
    expect(roles.listColumns.map((column) => [column.id, column.header, column.hidden ?? false])).toEqual([
      ['role', 'Role', false],
      ['everyDomain', 'In every sector', false],
      ['createdAt', 'Given', false],
      // Read with the row for the question that names the person, and not drawn.
      ['username', 'Username', true],
    ]);
    const remove = presentation(roles.rowMethods, 'RemoveCrewSectorRole');
    expect((remove.confirm as (row: RecordData) => string)({ role: 'Cadet', username: 'ada' })).toBe('Take Cadet away from ada?');
    expect(children(people).map((child) => child.title)).toEqual(['Roles held here', 'Sign-in identities']);
  });

  it('offers Withdraw on an invitation that still stands, and Invite above the list', () => {
    const withdraw = presentation(list(invitations).rowMethods, 'WithdrawCrewSectorInvitation');
    expect([withdraw.when?.({ pending: true }), withdraw.when?.({ pending: false })]).toEqual([true, false]);
    expect(methodsOf(list(invitations).listMethods)).toEqual([['InviteCrewSectorUser', 'Invite']]);
  });
});

describe('identityPages, what an auth and an application decide', () => {
  it("draws nothing for what the auth's sign-in methods do not give", () => {
    const pages = identityPages(descriptor, resourceMeta, { auth: 'Members', scope: 'global' });
    expect(pages.map((page) => page.routeData.route)).toEqual(['members-accounts']);
    const people = list(pages[0]);
    expect(columns(people).map(([id]) => id)).toEqual(['username', 'disabled', 'lastSignInAt']);
    expect(methodsOf(people.rowMethods)).toEqual([
      ['EndMembersUserSessions', 'Sign out everywhere'],
      ['DisableMembersUser', 'Disable'],
      ['EnableMembersUser', 'Enable'],
    ]);
    expect(people.listMethods).toEqual([]);
    // The roles are listed and nobody takes one away by hand.
    expect(children(pages[0]).map((child) => [child.title, child.rowMethods])).toEqual([['Roles held', []]]);
  });

  it('takes the group and the routes the application gives', () => {
    const pages = identityPages(descriptor, resourceMeta, {
      auth: 'Crew',
      scope: 'domain',
      group: 'Sector People',
      routes: { users: 'sectors/:sectorID/people' },
    });
    expect(pages.map((page) => [page.routeData.route, page.nav.group])).toEqual([
      ['sectors/:sectorID/people', 'Sector People'],
      ['sectors/:sectorID/crew-invitations', 'Sector People'],
    ]);
  });

  const refusals: { name: string; descriptor: ApiDescriptor; auth: string; scope: 'global' | 'domain'; want: string }[] = [
    {
      name: 'an auth the description does not carry',
      descriptor,
      auth: 'Staff',
      scope: 'global',
      want: 'identityPages: the API description carries no generated management of the auth Staff (it carries: Crew, Members); the auth is named as GenerateIdentity names it, and its management is served on the outlets that declare or manage it',
    },
    {
      name: 'a description with no generated management',
      descriptor: { ...descriptor, identity: undefined } as ApiDescriptor,
      auth: 'Crew',
      scope: 'global',
      want: 'identityPages: the API description carries no generated management of the auth Crew (it carries: none); the auth is named as GenerateIdentity names it, and its management is served on the outlets that declare or manage it',
    },
    {
      name: 'a side the auth does not have',
      descriptor,
      auth: 'Members',
      scope: 'domain',
      want: "identityPages: the generated management of Members has no tenant's side on this API",
    },
    {
      name: "a tenant's side on an API with no tenant segment",
      descriptor: { ...descriptor, domainRoute: undefined } as ApiDescriptor,
      auth: 'Crew',
      scope: 'domain',
      want: "identityPages: a tenant's pages of Crew live under the API's tenant segment, and the API description declares none (domainRoute)",
    },
  ];
  for (const tt of refusals) {
    it(`refuses ${tt.name}`, () => {
      expect(() => identityPages(tt.descriptor, resourceMeta, { auth: tt.auth, scope: tt.scope })).toThrow(tt.want);
    });
  }
});

describe('identityRoutes', () => {
  it('builds a route for each page, with a row route for the people alone', () => {
    const routes = identityRoutes(descriptor, resourceMeta, { auth: 'Crew', scope: 'global', group: 'Routes test' });
    expect(routes.map((route) => [route.path, (route.children ?? []).map((child) => child.path)])).toEqual([
      ['crew-accounts', ['', ':uuid']],
      ['crew-mailed-links', ['']],
      ['crew-sessions', ['']],
      ['crew-failed-sign-ins', ['']],
    ]);
  });
});
