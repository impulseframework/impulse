import { ApiDescriptor, IdentityDescriptor, Resource } from '@impulseframework/resource';
import {
  FieldMeta,
  ListViewConfig,
  MethodPresentation,
  MultiColumnConfig,
  RecordData,
  ResourceMeta,
  RootConfig,
} from '@impulseframework/resource-angular/types';
import { pageDataKey } from '@impulseframework/resource-angular/auth-frame';
import { identityMenuPages, identityPages, identityRoutes } from './identity-pages';

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
  CrewSessionUsers: fields('id', 'username', 'name', 'email', 'emailVerifiedAt', 'disabled', 'passwordChangeRequired', 'createdAt', 'lastSignInAt'),
  CrewUserRoles: fields('kind', 'axis', 'domainId', 'role', 'userId', 'createdAt'),
  CrewSessionIdentities: fields('id', 'userId', 'method', 'connection', 'emailAtLink', 'createdAt', 'lastUsedAt'),
  CrewSessionMailLinks: fields('id', 'kind', 'email', 'name', 'tenantId', 'roleNames', 'createdAt', 'expiresAt', 'consumedAt'),
  CrewSessions: fields('id', 'userId', 'username', 'expired', 'authenticatedAt', 'updatedAt'),
  CrewSessionSignInAttempts: fields('name', 'failures', 'lockedUntil', 'updatedAt'),
  CrewSignInMethods: fields('name', 'label', 'admits', 'openSignUp', 'defaultRole', 'directoryGivesRoles', 'says'),
  CrewSectorUsers: fields('id', 'domainId', 'username', 'name', 'email', 'disabled', 'passwordChangeRequired', 'lastSignInAt', 'heldElsewhere', 'heldGlobally'),
  CrewSectorUserRoles: fields('domainId', 'userId', 'username', 'role', 'everyDomain', 'createdAt'),
  CrewSectorIdentities: fields('id', 'userId', 'username', 'method', 'connection', 'emailAtLink', 'createdAt', 'lastUsedAt'),
  CrewSectorInvitations: fields('id', 'email', 'name', 'roleNames', 'createdAt', 'expiresAt', 'pending'),
  // An Auth whose username is the address carries no email column, and whose directory admits its people no password.
  MembersSessionUsers: fields('id', 'username', 'disabled', 'passwordChangeRequired', 'lastSignInAt'),
  MembersUserRoles: fields('kind', 'axis', 'domainId', 'role', 'userId', 'createdAt'),
  // An application without tenants: a person who signs up is given a role, picked from the roles it lists.
  StaffSessionUsers: fields('id', 'username', 'disabled'),
  StaffSignInMethods: fields('name', 'label', 'admits', 'openSignUp', 'defaultRole', 'directoryGivesRoles', 'says'),
  StaffSignUpRoles: { ...fields('name', 'description'), nameField: 'name' },
  // The tenant record: keyed by an id, and it says which field a sector is called by.
  Sectors: { ...fields('id', 'name', 'region'), nameField: 'name' },
};
const resourceMeta = (resource: Resource): ResourceMeta => metas[resource];

const crew: IdentityDescriptor = {
  auth: 'Crew',
  label: 'Crew',
  tenant: 'Sector',
  tenantResource: 'Sectors',
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
        signInMethods: 'CrewSignInMethods',
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
        setName: 'SetCrewUserName',
        setEmail: 'SetCrewUserEmail',
        setTemporaryPassword: 'SetCrewTemporaryPassword',
        requirePasswordChange: 'RequireCrewPasswordChange',
        endUserSessions: 'EndCrewUserSessions',
        revokeTrustedDevices: 'RevokeCrewUserTrustedDevices',
        unlinkIdentity: 'UnlinkCrewSessionIdentity',
        endSession: 'EndCrewSession',
        unlockSignIn: 'UnlockCrewSignIn',
        openSignUp: 'OpenCrewSignUp',
        makeInviteOnly: 'MakeCrewInviteOnly',
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
        setName: 'SetCrewSectorUserName',
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
// An application without tenants: the whole application's side alone, with the roles a default role is picked from.
const staff: IdentityDescriptor = {
  auth: 'Staff',
  label: 'Staff',
  sides: [
    {
      scope: 'global',
      lists: { users: 'StaffSessionUsers', signInMethods: 'StaffSignInMethods', signUpRoles: 'StaffSignUpRoles' },
      methods: { disable: 'DisableStaffUser', openSignUp: 'OpenStaffSignUp', makeInviteOnly: 'MakeStaffInviteOnly' },
    },
  ],
};
const descriptor = {
  resources: {},
  methods: {},
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  domainRoute: { segment: 'sectors', param: 'sectorID' },
  identity: [crew, members, staff],
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
  const [people, invitations, sessions, attempts, signIn] = pages;

  it('builds the accounts, the mailed links, the sessions, the failed sign-ins and the sign-in methods, each at a route named for the auth', () => {
    expect(pages.map((page) => [page.routeData.route, page.page?.label, page.page?.icon])).toEqual([
      ['crew-accounts', 'Accounts', 'manage_accounts'],
      ['crew-mailed-links', 'Mailed links', 'forward_to_inbox'],
      ['crew-sessions', 'Sessions', 'devices'],
      ['crew-failed-sign-ins', 'Failed sign-ins', 'gpp_bad'],
      ['crew-sign-in', 'Sign-in', 'login'],
    ]);
    expect(pages.map((page) => page.routeData.hasViewRoute)).toEqual([true, false, false, false, false]);
    expect(pages.map((page) => list(page).primaryResource)).toEqual([
      'CrewSessionUsers',
      'CrewSessionMailLinks',
      'CrewSessions',
      'CrewSessionSignInAttempts',
      'CrewSignInMethods',
    ]);
  });

  describe('the Sign-in page', () => {
    // What the server lists: one row for each method the Auth registers, with what was chosen for it.
    const password: RecordData = {
      name: 'password',
      label: 'Password',
      admits: 'Anyone with an email address',
      openSignUp: true,
      defaultRole: '',
      directoryGivesRoles: false,
      says: 'Open: Anyone with an email address will be able to sign up and create a sector of their own, without an invitation.',
    };
    const google: RecordData = {
      name: 'google',
      label: 'Google',
      admits: 'Anyone with a Google account',
      openSignUp: false,
      defaultRole: '',
      directoryGivesRoles: false,
      says: 'Invite only: a person signs in once they are invited.',
    };
    const entra: RecordData = {
      name: 'entra',
      label: 'Microsoft',
      admits: 'Anyone with an account at acme.example',
      openSignUp: false,
      defaultRole: '',
      directoryGivesRoles: true,
      says: 'The directory gives the roles, so it decides who may sign in: a person it gives a role signs in, and nobody else.',
    };

    it('lists each method by who can sign in with it and what was chosen, and reads what the actions need without drawing it', () => {
      expect(list(signIn).title).toBe('Crew sign-in');
      expect(list(signIn).listColumns.map((column) => [column.id, column.header, column.hidden === true])).toEqual([
        ['label', 'Sign-in method', false],
        ['admits', 'Who can sign in with it', false],
        ['openSignUp', 'Sign-up', false],
        // With tenants a person who signs up creates a sector of their own: no role is chosen here.
        ['says', 'A person with no invitation', false],
        ['name', 'Name', true],
        ['directoryGivesRoles', 'Directory gives roles', true],
      ]);
      const choice = list(signIn).listColumns.find((column) => column.id === 'openSignUp')?.valueGetter;
      expect([password, google, entra].map((row) => choice?.(row))).toEqual(['Open', 'Invite only', 'Set by the directory']);
    });

    it('offers Open sign-up on a method that is invite only, Make invite only on one that is open, and neither where the directory gives the roles', () => {
      const open = presentation(list(signIn).rowMethods, 'OpenCrewSignUp');
      const close = presentation(list(signIn).rowMethods, 'MakeCrewInviteOnly');
      expect(methodsOf(list(signIn).rowMethods)).toEqual([
        ['OpenCrewSignUp', 'Open sign-up'],
        ['MakeCrewInviteOnly', 'Make invite only'],
      ]);
      expect([password, google, entra].map((row) => [open.when?.(row), close.when?.(row)])).toEqual([
        [false, true],
        [true, false],
        [false, false],
      ]);
      // Neither method names a target row: the page fills the method's name from the row.
      expect(open.fill?.(google)).toEqual({ name: 'google' });
      expect(close.fill?.(password)).toEqual({ name: 'password' });
    });

    it('asks a plain question before a method is made invite only, and leaves the question of opening one to the warning the server writes', () => {
      const open = presentation(list(signIn).rowMethods, 'OpenCrewSignUp');
      const close = presentation(list(signIn).rowMethods, 'MakeCrewInviteOnly');
      expect(close.confirm).toBe('People who already have accounts keep them. New people will need an invitation.');
      expect(open.confirm).toBeUndefined();
      // The role each new person gets is picked with its description, where the method asks for one.
      expect(open.labels).toEqual({ defaultRole: 'Role each new person gets' });
      expect(open.shows).toEqual({ defaultRole: ['name', 'description'] });
    });

    it('keeps a notice above the list while a method is open, naming it and saying what a new person gets in the words the server wrote', () => {
      const notice = list(signIn).notice;
      expect(notice?.([google, entra])).toBeUndefined();
      expect(notice?.([password, google, entra])).toBe(
        'Password is open to sign-up. Anyone with an email address will be able to sign up and create a sector of their own, without an invitation.',
      );
      const member = { ...google, openSignUp: true, defaultRole: 'Member', says: 'Open: Anyone with a Google account will be able to sign in without an invitation. Each of them gets the role Member.' };
      expect(notice?.([password, member])).toBe(
        'Password is open to sign-up. Anyone with an email address will be able to sign up and create a sector of their own, without an invitation.\n' +
          'Google is open to sign-up. Anyone with a Google account will be able to sign in without an invitation. Each of them gets the role Member.',
      );
    });

    it('shows the role a new person gets where the application has no tenants', () => {
      const [, staffSignIn] = identityPages(descriptor, resourceMeta, { auth: 'Staff', scope: 'global' });
      expect(staffSignIn.routeData.route).toBe('staff-sign-in');
      expect(columns(list(staffSignIn)).filter(([id]) => id === 'defaultRole')).toEqual([['defaultRole', 'Role a new person gets']]);
    });

    it('is no page of an auth whose description names no sign-in methods: a directory that gives the roles has nothing to choose', () => {
      const built = identityPages(descriptor, resourceMeta, { auth: 'Members', scope: 'global' });
      expect(built.map((page) => page.page?.label)).toEqual(['Accounts']);
    });
  });

  it('lists the accounts by the columns the schema carries and offers each method under plain words', () => {
    expect(columns(list(people))).toEqual([
      ['username', 'Username'],
      ['name', 'Name'],
      ['email', 'Email'],
      ['disabled', 'Disabled'],
      ['passwordChangeRequired', 'Must change password'],
      ['lastSignInAt', 'Last sign-in'],
    ]);
    expect(methodsOf(list(people).rowMethods)).toEqual([
      ['AssignCrewRole', 'Give a role'],
      ['SetCrewUserName', 'Change the name'],
      ['RenameCrewUser', 'Change the username'],
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

  it("fills the id of the person's account in the method that gives a role, which names no target row, and names the tenant in the application's word", () => {
    const give = presentation(list(people).rowMethods, 'AssignCrewRole');
    expect(give.fill?.({ username: 'ada', id: 'u-1' })).toEqual({ userId: 'u-1' });
    // A role is given for a period: the dialog asks for its two days in plain words.
    expect(give.labels).toEqual({ domain: 'Sector', everyDomain: 'In every sector', startsAt: 'From', endsAt: 'Until' });
    expect(presentation(list(people).listMethods, 'CreateCrewUser').labels).toEqual({ domain: 'Sector', everyDomain: 'In every sector' });
  });

  it("draws a person's row page from the account's fields, with the roles, the identities and the sessions under it", () => {
    const section = list(people).elements[0] as unknown as { label: string; children: { name: string; label: string }[] };
    expect(section.label).toBe('Account');
    expect(section.children.map((child) => [child.name, child.label])).toEqual([
      ['username', 'Username'],
      ['name', 'Name'],
      ['email', 'Email'],
      ['disabled', 'Disabled'],
      ['passwordChangeRequired', 'Must change password'],
      ['lastSignInAt', 'Last sign-in'],
      ['emailVerifiedAt', 'Email verified'],
      ['createdAt', 'Created'],
    ]);
    expect(children(people).map((child) => [child.title, child.primaryResource, child.parentRelation, child.showViewButton])).toEqual([
      ['Roles held', 'CrewUserRoles', { parentKey: 'id', childKey: 'userId' }, false],
      ['Sign-in identities', 'CrewSessionIdentities', { parentKey: 'id', childKey: 'userId' }, false],
      ['Sessions', 'CrewSessions', { parentKey: 'id', childKey: 'userId' }, false],
    ]);
  });

  it('says where each role is held and fills the method that takes it away from the row', () => {
    const roles = children(people)[0];
    expect(columns(roles)).toEqual([
      ['role', 'Role'],
      ['domainId', 'Held in'],
      ['kind', undefined],
      ['createdAt', 'Given'],
    ]);
    // The sector a role is held in is named: the list reads the sector record's row by the
    // key the membership carries and shows its name. The words below are what a row says
    // that names no sector, or whose sector the caller may not read.
    const where = roles.listColumns[1] as MultiColumnConfig;
    expect(where.additionalIds).toEqual([{ resource: 'Sectors', id: 'id', field: 'name' }]);
    expect(where.filterable).toBe(false);
    expect(roles.listColumns[2].hidden).toBe(true);
    const heldIn = where.fallback as (row: RecordData) => string;
    const remove = presentation(roles.rowMethods, 'RemoveCrewRole');
    const cases: { row: RecordData; where: string; body: Record<string, unknown> }[] = [
      {
        row: { userId: 'u-1', role: 'Clerk', kind: 'global', domainId: '' },
        where: 'The whole application',
        body: { userId: 'u-1', role: 'Clerk', domain: null, everyDomain: false },
      },
      {
        row: { userId: 'u-1', role: 'Cadet', kind: 'domain', domainId: 'a1111111-1111-4111-8111-111111111111' },
        where: 'Sector a1111111-1111-4111-8111-111111111111',
        body: { userId: 'u-1', role: 'Cadet', domain: 'a1111111-1111-4111-8111-111111111111', everyDomain: false },
      },
      {
        row: { userId: 'u-1', role: 'Marshal', kind: 'every', domainId: '' },
        where: 'Every sector',
        body: { userId: 'u-1', role: 'Marshal', domain: null, everyDomain: true },
      },
    ];
    for (const tt of cases) {
      expect(heldIn(tt.row)).toBe(tt.where);
      expect(remove.fill?.(tt.row)).toEqual(tt.body);
    }
    // The question names no key: the row the person clicked names the sector, and the
    // page the list sits on names the person, whom a role row knows by id alone.
    const confirm = remove.confirm as (row: RecordData) => string;
    expect(cases.map((tt) => confirm(tt.row))).toEqual([
      'Take Clerk away from this person?',
      'Take Cadet in this sector away from this person?',
      'Take Marshal in every sector away from this person?',
    ]);
  });

  it('offers Withdraw on a mailed link that was not used, End the session on a session, and Unlock on a counted name', () => {
    const withdraw = presentation(list(invitations).rowMethods, 'WithdrawCrewInvitation');
    expect([withdraw.when?.({ consumedAt: null }), withdraw.when?.({ consumedAt: '2026-01-01T00:00:00Z' })]).toEqual([true, false]);
    expect(list(invitations).listMethods).toEqual([]);
    expect(columns(list(invitations)).map(([, header]) => header)).toEqual(['Kind', 'Email', 'Name', 'Sector', 'Roles', 'Sent', 'Expires', 'Used']);
    // The sector a link invites into is named the same way, and shows its key where the sector cannot be read.
    const into = list(invitations).listColumns[3] as MultiColumnConfig;
    expect(into.additionalIds).toEqual([{ resource: 'Sectors', id: 'id', field: 'name' }]);
    expect(into.fallback?.({ tenantId: 'a1111111-1111-4111-8111-111111111111' })).toBe('a1111111-1111-4111-8111-111111111111');
    expect(into.fallback?.({ tenantId: null })).toBe('');
    expect(methodsOf(list(sessions).rowMethods)).toEqual([['EndCrewSession', 'End the session']]);
    expect(columns(list(sessions)).map(([id]) => id)).toEqual(['username', 'authenticatedAt', 'updatedAt', 'expired']);
    expect(methodsOf(list(attempts).rowMethods)).toEqual([['UnlockCrewSignIn', 'Unlock']]);
    expect(methodsOf(children(people)[2].rowMethods)).toEqual([['EndCrewSession', 'End the session']]);
  });
});

describe("identityPages, a tenant's side", () => {
  const pages = identityPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'domain' });
  const [people, invitations] = pages;

  it("builds the people and the invitations under the API's tenant segment", () => {
    expect(pages.map((page) => [page.routeData.route, page.page?.label, page.page?.icon])).toEqual([
      ['sectors/:sectorID/crew-people', 'People', 'group'],
      ['sectors/:sectorID/crew-invitations', 'Invitations', 'mail'],
    ]);
  });

  it('names the tenant record in its words and leaves out what the side does not have', () => {
    expect(columns(list(people)).at(-1)).toEqual(['heldElsewhere', 'Also in another sector']);
    // No password method on this side of the description: no column for it.
    expect(columns(list(people)).map(([id]) => id)).toEqual(['username', 'name', 'email', 'disabled', 'lastSignInAt', 'heldElsewhere']);
    expect(methodsOf(list(people).rowMethods)).toEqual([
      ['AssignCrewSectorRole', 'Give a role'],
      ['SetCrewSectorUserName', 'Change the name'],
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

describe("identityPages, a person's password", () => {
  // An Auth with a password method: each side names the method that removes a person's password.
  const withPassword = {
    ...descriptor,
    identity: [
      {
        ...crew,
        sides: [
          { ...crew.sides[0], methods: { ...crew.sides[0].methods, removePassword: 'RemoveCrewPassword' } },
          { ...crew.sides[1], methods: { ...crew.sides[1].methods, removePassword: 'RemoveCrewSectorPassword' } },
        ],
      },
      members,
    ],
  } as ApiDescriptor;
  const identitiesOf = (pages: RootConfig[]): ListViewConfig => {
    const found = children(pages[0]).find((child) => child.title === 'Sign-in identities');
    if (!found) {
      throw new Error('no list of sign-in identities');
    }
    return found;
  };

  for (const tt of [
    {
      name: "the whole application's side",
      scope: 'global' as const,
      remove: 'RemoveCrewPassword',
      unlink: 'UnlinkCrewSessionIdentity',
    },
    {
      name: "a tenant's side",
      scope: 'domain' as const,
      remove: 'RemoveCrewSectorPassword',
      unlink: 'UnlinkCrewSectorSessionIdentity',
    },
  ]) {
    it(`offers Remove password above the identities a person signs in with, where Unlink is offered, on ${tt.name}`, () => {
      const pages = identityPages(withPassword, resourceMeta, { auth: 'Crew', scope: tt.scope });
      const identities = identitiesOf(pages);
      expect(methodsOf(identities.rowMethods)).toEqual([[tt.unlink, 'Unlink']]);
      expect(methodsOf(identities.listMethods)).toEqual([[tt.remove, 'Remove password']]);
      const remove = presentation(identities.listMethods, tt.remove);
      // The method runs against the person the list sits under: the page fills nothing,
      // and the question, asked of that person, says what is left to them. This
      // description names no sign-in by its button, so the list under the question does.
      expect(remove.fill).toBeUndefined();
      const ada = { id: 'u-1', username: 'ada' };
      const question = remove.confirm as (row: RecordData, listed?: readonly RecordData[]) => string;
      expect(question(ada, [{ method: 'google' }])).toBe(
        'Remove the password of ada? They will sign in only through the sign-in identities listed here.',
      );
      // It is offered while an identity is linked to the person: the server refuses the
      // removal of a password that is the person's only way in.
      expect([remove.when?.(ada, [{ method: 'google' }]), remove.when?.(ada, []), remove.when?.(ada)]).toEqual([
        true,
        false,
        false,
      ]);
      // It is offered there alone, not a second time among the methods of the person's row.
      expect(presentation(list(pages[0]).rowMethods, tt.remove).hidden).toBe(true);
    });
  }

  it('names what the removal leaves by the words the sign-in buttons of the linked identities carry', () => {
    // The login page offers Fleet Google, and two Entra registrations: an identity
    // names its method and not its registration, so an Entra link names both.
    const signIns = [
      { method: 'google', label: 'Fleet Google' },
      { method: 'azure', label: 'Lakeside' },
      { method: 'azure', label: 'Riverside' },
    ];
    const named = { ...withPassword, identity: [{ ...withPassword.identity![0], signIns }, members] } as ApiDescriptor;
    const ada = { id: 'u-1', username: 'ada' };
    for (const [scope, method] of [
      ['global', 'RemoveCrewPassword'],
      ['domain', 'RemoveCrewSectorPassword'],
    ] as const) {
      const remove = presentation(
        identitiesOf(identityPages(named, resourceMeta, { auth: 'Crew', scope })).listMethods,
        method,
      );
      const question = remove.confirm as (row: RecordData, listed?: readonly RecordData[]) => string;
      expect(question(ada, [{ method: 'google' }, { method: 'google' }])).toBe(
        'Remove the password of ada? They will sign in through Fleet Google only.',
      );
      expect(question(ada, [{ method: 'azure' }])).toBe(
        'Remove the password of ada? They will sign in through Lakeside or Riverside only.',
      );
      expect(question(ada, [{ method: 'azure' }, { method: 'google' }])).toBe(
        'Remove the password of ada? They will sign in through Fleet Google, Lakeside or Riverside only.',
      );
      // A link through a method the login page offers no button for is named by the list.
      expect(question(ada, [{ method: 'workos' }])).toBe(
        'Remove the password of ada? They will sign in only through the sign-in identities listed here.',
      );
    }
  });

  it('offers none where the description names no such method', () => {
    for (const scope of ['global', 'domain'] as const) {
      const pages = identityPages(descriptor, resourceMeta, { auth: 'Crew', scope });
      expect(identitiesOf(pages).listMethods).toEqual([]);
      expect(methodsOf(list(pages[0]).rowMethods).map(([method]) => method)).not.toContain(
        scope === 'global' ? 'RemoveCrewPassword' : 'RemoveCrewSectorPassword',
      );
    }
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
    // An application with no tenant record names no tenant: where a role is held is said in words alone.
    const where = children(pages[0])[0].listColumns[1];
    expect('additionalIds' in where).toBe(false);
    expect(where.valueGetter?.({ kind: 'global', domainId: '' })).toBe('The whole application');
  });

  it('takes the routes the application gives', () => {
    const pages = identityPages(descriptor, resourceMeta, {
      auth: 'Crew',
      scope: 'domain',
      routes: { users: 'sectors/:sectorID/people' },
    });
    expect(pages.map((page) => page.routeData.route)).toEqual([
      'sectors/:sectorID/people',
      'sectors/:sectorID/crew-invitations',
    ]);
  });

  it('answers the same request with the same pages, and another request with its own', () => {
    const pages = identityPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'domain' });
    expect(identityPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'domain' })).toBe(pages);
    expect(identityPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'global' })).not.toBe(pages);
  });

  const refusals: { name: string; descriptor: ApiDescriptor; auth: string; scope: 'global' | 'domain'; want: string }[] = [
    {
      name: 'an auth the description does not carry',
      descriptor,
      auth: 'Partners',
      scope: 'global',
      want: 'identityPages: the API description carries no generated management of the auth Partners (it carries: Crew, Members, Staff); the auth is named as GenerateIdentity names it, and its management is served on the outlets that declare or manage it',
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
    const routes = identityRoutes(descriptor, resourceMeta, { auth: 'Crew', scope: 'global' });
    expect(routes.map((route) => [route.path, (route.children ?? []).map((child) => child.path)])).toEqual([
      ['crew-accounts', ['', ':uuid']],
      ['crew-mailed-links', ['']],
      ['crew-sessions', ['']],
      ['crew-failed-sign-ins', ['']],
      ['crew-sign-in', ['']],
    ]);
  });

  it('carries on each route the page the menu file places', () => {
    const routes = identityRoutes(descriptor, resourceMeta, { auth: 'Crew', scope: 'global' });
    const pages = identityMenuPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'global' });
    expect(routes.map((route) => route.data?.[pageDataKey])).toEqual(pages);
    routes.forEach((route, i) => expect(route.data?.[pageDataKey]).toBe(pages[i]));
    expect(pages.map((page) => [page.label, page.route, page.permission])).toEqual([
      ['Accounts', ['crew-accounts'], { resource: 'CrewSessionUsers', permission: 'List' }],
      ['Mailed links', ['crew-mailed-links'], { resource: 'CrewSessionMailLinks', permission: 'List' }],
      ['Sessions', ['crew-sessions'], { resource: 'CrewSessions', permission: 'List' }],
      ['Failed sign-ins', ['crew-failed-sign-ins'], { resource: 'CrewSessionSignInAttempts', permission: 'List' }],
      ['Sign-in', ['crew-sign-in'], { resource: 'CrewSignInMethods', permission: 'List' }],
    ]);
  });
});

// An Auth that keeps groups of people: the description names the groups, the people in
// them, the roles they hold and the roles each person holds through one, on each side,
// with the methods that manage them.
describe('identityPages, an Auth that keeps groups', () => {
  const groupMetas: Record<string, ResourceMeta> = {
    ...metas,
    CrewGroups: fields('id', 'kind', 'axis', 'domainId', 'name', 'description', 'updatedAt'),
    CrewGroupUsers: fields('groupId', 'userId', 'username', 'name', 'groupName', 'createdAt'),
    CrewGroupRoles: fields('groupId', 'kind', 'axis', 'domainId', 'role', 'createdAt'),
    CrewUserGroupRoles: fields('userId', 'username', 'groupId', 'groupName', 'kind', 'axis', 'domainId', 'role'),
    CrewSectorGroups: fields('id', 'domainId', 'name', 'description', 'updatedAt'),
    CrewSectorGroupUsers: fields('domainId', 'groupId', 'userId', 'username', 'name', 'groupName', 'createdAt'),
    CrewSectorGroupRoles: fields('domainId', 'groupId', 'groupName', 'role', 'createdAt'),
    CrewSectorUserGroupRoles: fields('domainId', 'userId', 'username', 'groupId', 'groupName', 'role', 'globalGroup', 'everyDomain'),
  };
  const groupMeta = (resource: Resource): ResourceMeta => groupMetas[resource];
  const [wholeApplication, tenant] = crew.sides;
  const grouped: IdentityDescriptor = {
    ...crew,
    sides: [
      {
        ...wholeApplication,
        lists: {
          ...wholeApplication.lists,
          groups: 'CrewGroups',
          groupUsers: 'CrewGroupUsers',
          groupRoles: 'CrewGroupRoles',
          rolesThroughGroups: 'CrewUserGroupRoles',
        },
        methods: {
          ...wholeApplication.methods,
          createGroup: 'CreateCrewGroup',
          describeGroup: 'DescribeCrewGroup',
          deleteGroup: 'DeleteCrewGroup',
          addGroupMember: 'AddCrewGroupMember',
          removeGroupMember: 'RemoveCrewGroupMember',
          giveGroupRole: 'GiveCrewGroupRole',
          takeGroupRole: 'TakeCrewGroupRole',
        },
      },
      {
        ...tenant,
        lists: {
          ...tenant.lists,
          groups: 'CrewSectorGroups',
          groupUsers: 'CrewSectorGroupUsers',
          groupRoles: 'CrewSectorGroupRoles',
          rolesThroughGroups: 'CrewSectorUserGroupRoles',
        },
        methods: {
          ...tenant.methods,
          createGroup: 'CreateCrewSectorGroup',
          describeGroup: 'DescribeCrewSectorGroup',
          deleteGroup: 'DeleteCrewSectorGroup',
          addGroupMember: 'AddCrewSectorGroupMember',
          removeGroupMember: 'RemoveCrewSectorGroupMember',
          giveGroupRole: 'GiveCrewSectorGroupRole',
          takeGroupRole: 'TakeCrewSectorGroupRole',
        },
      },
    ],
  };
  const groupDescriptor = { ...descriptor, identity: [grouped, members] } as ApiDescriptor;
  const global = identityPages(groupDescriptor, groupMeta, { auth: 'Crew', scope: 'global' });
  const domain = identityPages(groupDescriptor, groupMeta, { auth: 'Crew', scope: 'domain' });

  it('builds a Groups page after the people on each side, with a row page for a group', () => {
    expect(global.map((page) => [page.routeData.route, page.page?.label, page.page?.icon, page.routeData.hasViewRoute])).toEqual([
      ['crew-accounts', 'Accounts', 'manage_accounts', true],
      ['crew-groups', 'Groups', 'groups', true],
      ['crew-mailed-links', 'Mailed links', 'forward_to_inbox', false],
      ['crew-sessions', 'Sessions', 'devices', false],
      ['crew-failed-sign-ins', 'Failed sign-ins', 'gpp_bad', false],
    ]);
    expect(domain.map((page) => [page.routeData.route, page.page?.label, list(page).primaryResource])).toEqual([
      ['sectors/:sectorID/crew-people', 'People', 'CrewSectorUsers'],
      ['sectors/:sectorID/crew-groups', 'Groups', 'CrewSectorGroups'],
      ['sectors/:sectorID/crew-invitations', 'Invitations', 'CrewSectorInvitations'],
    ]);
    expect(identityPages(groupDescriptor, groupMeta, { auth: 'Crew', scope: 'domain', routes: { groups: 'sectors/:sectorID/groups' } })[1].routeData.route).toBe(
      'sectors/:sectorID/groups',
    );
  });

  it('leaves the Groups page and the roles through groups out where the Auth keeps none', () => {
    const without = identityPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'global' });
    expect(without.map((page) => page.page?.label)).toEqual(['Accounts', 'Mailed links', 'Sessions', 'Failed sign-ins']);
    expect(children(without[0]).map((child) => child.title)).toEqual(['Roles held', 'Sign-in identities', 'Sessions']);
  });

  it("lists the whole application's groups with where each is kept, and offers the methods that run against a group", () => {
    const groups = list(global[1]);
    expect(groups.title).toBe('Crew groups');
    expect(columns(groups)).toEqual([
      ['name', 'Name'],
      ['description', 'Description'],
      ['domainId', 'Kept in'],
      ['kind', undefined],
    ]);
    const kept = groups.listColumns[2] as MultiColumnConfig;
    expect(kept.additionalIds).toEqual([{ resource: 'Sectors', id: 'id', field: 'name' }]);
    const keptIn = kept.fallback as (row: RecordData) => string;
    expect(keptIn({ kind: 'global', domainId: '' })).toBe('The whole application');
    expect(keptIn({ kind: 'domain', domainId: 's-1' })).toBe('Sector s-1');
    expect(methodsOf(groups.rowMethods)).toEqual([
      ['DescribeCrewGroup', 'Change the description'],
      ['AddCrewGroupMember', 'Add a person'],
      ['GiveCrewGroupRole', 'Give a role'],
      ['DeleteCrewGroup', 'Delete'],
    ]);
    expect(methodsOf(groups.listMethods)).toEqual([['CreateCrewGroup', 'Add a group']]);
    expect(presentation(groups.listMethods, 'CreateCrewGroup').labels).toEqual({ domain: 'Sector', everyDomain: 'In every sector' });
    expect((presentation(groups.rowMethods, 'DeleteCrewGroup').confirm as (row: RecordData) => string)({ name: 'Deck crew' })).toBe(
      'Delete Deck crew? A group that still has people in it or holds a role is not deleted: empty it first.',
    );
  });

  it("draws a group's row page with the people in it and the roles it holds, each list under the group", () => {
    expect(children(global[1]).map((child) => [child.title, child.primaryResource, child.parentRelation, child.showViewButton])).toEqual([
      ['People in the group', 'CrewGroupUsers', { parentKey: 'id', childKey: 'groupId' }, false],
      ['Roles the group holds', 'CrewGroupRoles', { parentKey: 'id', childKey: 'groupId' }, false],
    ]);
    const [people, roles] = children(global[1]);
    expect(columns(people)).toEqual([
      ['username', 'Username'],
      ['name', 'Name'],
      ['createdAt', 'Added'],
    ]);
    expect(methodsOf(people.rowMethods)).toEqual([['RemoveCrewGroupMember', 'Remove from the group']]);
    expect(methodsOf(people.listMethods)).toEqual([['AddCrewGroupMember', 'Add a person']]);
    expect((presentation(people.rowMethods, 'RemoveCrewGroupMember').confirm as (row: RecordData) => string)({ username: 'ada' })).toBe(
      "Remove ada from the group? They keep the roles they hold themselves and lose the group's.",
    );
    expect(columns(roles)).toEqual([
      ['role', 'Role'],
      ['domainId', 'Held in'],
      ['kind', undefined],
      ['createdAt', 'Given'],
    ]);
    expect(methodsOf(roles.listMethods)).toEqual([['GiveCrewGroupRole', 'Give a role']]);
  });

  it("fills the whole application's method that takes a group's role away from the row, which names where the role is held", () => {
    const roles = children(global[1])[1];
    const take = presentation(roles.rowMethods, 'TakeCrewGroupRole');
    const cases: { row: RecordData; body: Record<string, unknown>; question: string }[] = [
      {
        row: { groupId: 'g-1', role: 'Clerk', kind: 'global', domainId: '' },
        body: { groupId: 'g-1', role: 'Clerk', domain: null, everyDomain: false },
        question: 'Take Clerk away from the group? Everyone in the group loses it, unless they hold it some other way.',
      },
      {
        row: { groupId: 'g-1', role: 'Cadet', kind: 'domain', domainId: 's-1' },
        body: { groupId: 'g-1', role: 'Cadet', domain: 's-1', everyDomain: false },
        question: 'Take Cadet in this sector away from the group? Everyone in the group loses it, unless they hold it some other way.',
      },
      {
        row: { groupId: 'g-1', role: 'Archivist', kind: 'every', domainId: '' },
        body: { groupId: 'g-1', role: 'Archivist', domain: null, everyDomain: true },
        question: 'Take Archivist in every sector away from the group? Everyone in the group loses it, unless they hold it some other way.',
      },
    ];
    for (const tt of cases) {
      expect(take.fill?.(tt.row)).toEqual(tt.body);
      expect((take.confirm as (row: RecordData) => string)(tt.row)).toBe(tt.question);
    }
  });

  it("lists a tenant's own groups, whose roles are the tenant's alone, and runs each method against the row it names", () => {
    const groups = list(domain[1]);
    expect(groups.title).toBe('Groups');
    expect(columns(groups)).toEqual([
      ['name', 'Name'],
      ['description', 'Description'],
    ]);
    expect(methodsOf(groups.rowMethods)).toEqual([
      ['DescribeCrewSectorGroup', 'Change the description'],
      ['AddCrewSectorGroupMember', 'Add a person'],
      ['GiveCrewSectorGroupRole', 'Give a role'],
      ['DeleteCrewSectorGroup', 'Delete'],
    ]);
    const [people, roles] = children(domain[1]);
    expect([people.primaryResource, roles.primaryResource]).toEqual(['CrewSectorGroupUsers', 'CrewSectorGroupRoles']);
    expect(columns(roles)).toEqual([
      ['role', 'Role'],
      ['createdAt', 'Given'],
    ]);
    // The tenant's method names the row of the role it takes away: nothing is filled.
    const take = presentation(roles.rowMethods, 'TakeCrewSectorGroupRole');
    expect(take.fill).toBeUndefined();
    expect((take.confirm as (row: RecordData) => string)({ role: 'Cadet' })).toBe(
      'Take Cadet away from the group? Everyone in the group loses it, unless they hold it some other way.',
    );
  });

  it('says under a person which roles they hold themselves and which through which group', () => {
    expect(children(global[0]).map((child) => [child.title, child.primaryResource, child.parentRelation])).toEqual([
      ['Roles held', 'CrewUserRoles', { parentKey: 'id', childKey: 'userId' }],
      ['Roles held through a group', 'CrewUserGroupRoles', { parentKey: 'id', childKey: 'userId' }],
      ['Sign-in identities', 'CrewSessionIdentities', { parentKey: 'id', childKey: 'userId' }],
      ['Sessions', 'CrewSessions', { parentKey: 'id', childKey: 'userId' }],
    ]);
    const through = children(global[0])[1];
    expect(columns(through)).toEqual([
      ['role', 'Role'],
      ['groupName', 'Group'],
      ['domainId', 'Held in'],
      ['kind', undefined],
    ]);
    // A role held through a group is changed on the group's page: the list offers nothing.
    expect(through.rowMethods).toBe(false);

    const here = children(domain[0])[1];
    expect([here.title, here.primaryResource, here.rowMethods]).toEqual(['Roles held here through a group', 'CrewSectorUserGroupRoles', false]);
    expect(columns(here)).toEqual([
      ['role', 'Role'],
      ['groupName', 'Group'],
      ['globalGroup', 'Group of the whole application'],
      ['everyDomain', 'In every sector'],
    ]);
  });
});

// A role, a place in a group and a group's role may each be given for a period. The lists
// whose rows carry one show it, from the first day it counts until the first day it no
// longer does, and the methods that give one ask for the two days under the same words.
// The lists of the suites above carry none, as an Auth whose schema holds none, and show
// neither column.
describe('identityPages, the period of an assignment', () => {
  const periodMetas: Record<string, ResourceMeta> = {
    ...metas,
    CrewUserRoles: fields('kind', 'axis', 'domainId', 'role', 'userId', 'createdAt', 'startsAt', 'endsAt'),
    CrewSectorUserRoles: fields('domainId', 'userId', 'username', 'role', 'everyDomain', 'createdAt', 'startsAt', 'endsAt'),
    CrewGroups: fields('id', 'kind', 'axis', 'domainId', 'name', 'description', 'updatedAt'),
    CrewGroupUsers: fields('groupId', 'userId', 'username', 'name', 'groupName', 'createdAt', 'startsAt', 'endsAt'),
    CrewGroupRoles: fields('groupId', 'kind', 'axis', 'domainId', 'role', 'createdAt', 'startsAt', 'endsAt'),
    CrewUserGroupRoles: fields('userId', 'username', 'groupId', 'groupName', 'kind', 'axis', 'domainId', 'role', 'startsAt', 'endsAt'),
    CrewSectorGroups: fields('id', 'domainId', 'name', 'description', 'updatedAt'),
    CrewSectorGroupUsers: fields('domainId', 'groupId', 'userId', 'username', 'name', 'groupName', 'createdAt', 'startsAt', 'endsAt'),
    CrewSectorGroupRoles: fields('domainId', 'groupId', 'groupName', 'role', 'createdAt', 'startsAt', 'endsAt'),
    CrewSectorUserGroupRoles: fields('domainId', 'userId', 'username', 'groupId', 'groupName', 'role', 'globalGroup', 'everyDomain', 'startsAt', 'endsAt'),
  };
  const periodMeta = (resource: Resource): ResourceMeta => periodMetas[resource];
  const [wholeApplication, tenant] = crew.sides;
  const timed: IdentityDescriptor = {
    ...crew,
    sides: [
      {
        ...wholeApplication,
        lists: { ...wholeApplication.lists, groups: 'CrewGroups', groupUsers: 'CrewGroupUsers', groupRoles: 'CrewGroupRoles', rolesThroughGroups: 'CrewUserGroupRoles' },
        methods: { ...wholeApplication.methods, addGroupMember: 'AddCrewGroupMember', giveGroupRole: 'GiveCrewGroupRole' },
      },
      {
        ...tenant,
        lists: {
          ...tenant.lists,
          groups: 'CrewSectorGroups',
          groupUsers: 'CrewSectorGroupUsers',
          groupRoles: 'CrewSectorGroupRoles',
          rolesThroughGroups: 'CrewSectorUserGroupRoles',
        },
        methods: { ...tenant.methods, addGroupMember: 'AddCrewSectorGroupMember', giveGroupRole: 'GiveCrewSectorGroupRole' },
      },
    ],
  };
  const timedDescriptor = { ...descriptor, identity: [timed, members] } as ApiDescriptor;
  const global = identityPages(timedDescriptor, periodMeta, { auth: 'Crew', scope: 'global' });
  const domain = identityPages(timedDescriptor, periodMeta, { auth: 'Crew', scope: 'domain' });
  const period: [string, string][] = [
    ['startsAt', 'From'],
    ['endsAt', 'Until'],
  ];
  const drawn = (config: ListViewConfig): [string, string | undefined][] =>
    config.listColumns.filter((column) => !column.hidden && column.header !== undefined).map((column) => [column.id, column.header]);

  it("shows the period of each role under a person, the person's own and the ones held through a group", () => {
    const [own, through] = children(global[0]);
    expect([own.title, through.title]).toEqual(['Roles held', 'Roles held through a group']);
    expect(drawn(own).slice(-3)).toEqual([['createdAt', 'Given'], ...period]);
    expect(drawn(through).slice(-2)).toEqual(period);

    const [here, hereThrough] = children(domain[0]);
    expect([here.title, hereThrough.title]).toEqual(['Roles held here', 'Roles held here through a group']);
    expect(drawn(here)).toEqual([['role', 'Role'], ['everyDomain', 'In every sector'], ['createdAt', 'Given'], ...period]);
    expect(drawn(hereThrough).slice(-2)).toEqual(period);
  });

  it("shows the period of each place in a group and of each role a group holds, on the group's page", () => {
    for (const side of [global, domain]) {
      const [people, roles] = children(side[1]);
      expect([people.title, roles.title]).toEqual(['People in the group', 'Roles the group holds']);
      expect(drawn(people)).toEqual([['username', 'Username'], ['name', 'Name'], ['createdAt', 'Added'], ...period]);
      expect(drawn(roles).slice(-3)).toEqual([['createdAt', 'Given'], ...period]);
    }
  });

  it('asks for the two days of a period under the same words, in each method that gives something for one', () => {
    const words = { startsAt: 'From', endsAt: 'Until' };
    const asked: [RootConfig, string, 'row' | 'list'][] = [
      [global[0], 'AssignCrewRole', 'row'],
      [domain[0], 'AssignCrewSectorRole', 'row'],
      [global[1], 'AddCrewGroupMember', 'row'],
      [global[1], 'GiveCrewGroupRole', 'row'],
      [domain[1], 'AddCrewSectorGroupMember', 'row'],
      [domain[1], 'GiveCrewSectorGroupRole', 'row'],
    ];
    for (const [page, method, where] of asked) {
      const offered = where === 'row' ? list(page).rowMethods : list(page).listMethods;
      expect(presentation(offered, method).labels, method).toEqual(expect.objectContaining(words));
    }
    // Above the lists under a group, where the same two methods are offered again.
    for (const [side, add, give] of [
      [global, 'AddCrewGroupMember', 'GiveCrewGroupRole'],
      [domain, 'AddCrewSectorGroupMember', 'GiveCrewSectorGroupRole'],
    ] as [RootConfig[], string, string][]) {
      const [people, roles] = children(side[1]);
      expect(presentation(people.listMethods, add).labels, add).toEqual(words);
      expect(presentation(roles.listMethods, give).labels, give).toEqual(expect.objectContaining(words));
    }
  });
});
