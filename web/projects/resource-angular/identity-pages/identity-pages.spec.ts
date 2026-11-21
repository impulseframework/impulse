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
  CrewSessionUsers: fields(
    'id',
    'username',
    'name',
    'email',
    'emailVerifiedAt',
    'disabled',
    'passwordChangeRequired',
    'createdAt',
    'lastSignInAt',
  ),
  CrewUserRoles: fields('kind', 'axis', 'domainId', 'role', 'userId', 'createdAt'),
  // The lists a role is picked from: each answers for the method its filter names.
  CrewGivableRoles: fields('name', 'description', 'method', 'group', 'domain', 'everyDomain'),
  CrewSectorRoles: fields('name', 'description', 'method'),
  CrewSessionIdentities: fields('id', 'userId', 'method', 'connection', 'emailAtLink', 'createdAt', 'lastUsedAt'),
  CrewSessionMailLinks: fields(
    'id',
    'kind',
    'email',
    'name',
    'tenantId',
    'everyTenant',
    'roleNames',
    'createdAt',
    'expiresAt',
    'consumedAt',
  ),
  CrewSessions: fields('id', 'userId', 'username', 'expired', 'authenticatedAt', 'updatedAt'),
  CrewSessionSignInAttempts: fields('name', 'failures', 'lockedUntil', 'updatedAt'),
  CrewSignInMethods: fields(
    'name',
    'label',
    'admits',
    'openSignUp',
    'defaultRole',
    'directoryGivesRoles',
    'ownRule',
    'says',
    'selfServiceReset',
  ),
  CrewSectorUsers: fields(
    'id',
    'domainId',
    'username',
    'name',
    'email',
    'disabled',
    'passwordChangeRequired',
    'lastSignInAt',
    'heldElsewhere',
    'heldGlobally',
  ),
  CrewSectorUserRoles: fields('domainId', 'userId', 'username', 'role', 'everyDomain', 'createdAt'),
  CrewSectorIdentities: fields(
    'id',
    'userId',
    'username',
    'method',
    'connection',
    'emailAtLink',
    'createdAt',
    'lastUsedAt',
  ),
  CrewSectorInvitations: fields('id', 'email', 'name', 'roleNames', 'createdAt', 'expiresAt', 'pending'),
  // How a person signs in: the password and the identities that count, as the server judges it.
  CrewSignInWays: fields('userId', 'way', 'password', 'method', 'connection', 'through'),
  CrewSectorSignInWays: fields(
    'userId',
    'way',
    'password',
    'method',
    'connection',
    'through',
    'heldElsewhere',
    'heldGlobally',
  ),
  // An Auth whose username is the address carries no email column, and whose directory admits its people no password.
  MembersSessionUsers: fields('id', 'username', 'disabled', 'passwordChangeRequired', 'lastSignInAt'),
  MembersUserRoles: fields('kind', 'axis', 'domainId', 'role', 'userId', 'createdAt'),
  // An application without tenants: a person who signs up is given a role, picked from the roles it lists.
  StaffSessionUsers: fields('id', 'username', 'disabled'),
  StaffSignInMethods: fields(
    'name',
    'label',
    'admits',
    'openSignUp',
    'defaultRole',
    'directoryGivesRoles',
    'ownRule',
    'says',
    'selfServiceReset',
  ),
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
        rolesToGive: 'CrewGivableRoles',
        identities: 'CrewSessionIdentities',
        invitations: 'CrewSessionMailLinks',
        sessions: 'CrewSessions',
        signInAttempts: 'CrewSessionSignInAttempts',
        signInMethods: 'CrewSignInMethods',
      },
      methods: {
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
        sendPasswordResetLink: 'SendCrewUserPasswordResetLink',
        requirePasswordChange: 'RequireCrewPasswordChange',
        endUserSessions: 'EndCrewUserSessions',
        revokeTrustedDevices: 'RevokeCrewUserTrustedDevices',
        unlinkIdentity: 'UnlinkCrewSessionIdentity',
        endSession: 'EndCrewSession',
        unlockSignIn: 'UnlockCrewSignIn',
        openSignUp: 'OpenCrewSignUp',
        makeInviteOnly: 'MakeCrewInviteOnly',
        setSelfServiceReset: 'SetCrewSelfServiceReset',
      },
    },
    {
      scope: 'domain',
      lists: {
        users: 'CrewSectorUsers',
        roles: 'CrewSectorUserRoles',
        rolesToGive: 'CrewSectorRoles',
        identities: 'CrewSectorIdentities',
        invitations: 'CrewSectorInvitations',
      },
      methods: {
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
// A directory writes this Auth's roles and admits its people: nobody is invited or given a role by hand.
const members: IdentityDescriptor = {
  auth: 'Members',
  label: 'Members',
  sides: [
    {
      scope: 'global',
      lists: { users: 'MembersSessionUsers', roles: 'MembersUserRoles' },
      methods: {
        disable: 'DisableMembersUser',
        enable: 'EnableMembersUser',
        endUserSessions: 'EndMembersUserSessions',
      },
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
const columns = (config: ListViewConfig): [string, string | undefined][] =>
  config.listColumns.map((column) => [column.id, column.header]);
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
/** The filter a presentation asks a picker's list with, from the dialog's values; a picker it does not narrow fails the spec. */
const narrowed = (presented: MethodPresentation, field: string): ((values: RecordData) => string) => {
  const narrow = presented.narrows?.[field];
  if (!narrow) {
    throw new Error(`${presented.method} does not narrow ${field}`);
  }
  return narrow;
};

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
    // A registration that carries an account rule of its own: the application's code decides.
    const partners: RecordData = {
      name: 'partners',
      label: 'Partners',
      admits: 'Anyone with an account at Harbor',
      openSignUp: false,
      defaultRole: '',
      directoryGivesRoles: false,
      ownRule: true,
      says: "The application's own rule decides who may sign in through this method: it is neither opened to sign-up nor made invite only here.",
    };
    // A method opened when its registration admitted fewer people: invite only until it is opened again.
    const widened: RecordData = {
      name: 'workspace',
      label: 'Acme',
      admits: 'Anyone with an account at acme.example or lakeside.edu',
      openSignUp: false,
      defaultRole: '',
      directoryGivesRoles: false,
      ownRule: false,
      says: 'Invite only: the people this method admits have changed since it was opened, and it admits anyone with an account at acme.example or lakeside.edu now. Open it again to admit them.',
    };

    it('lists each method by who can sign in with it and what was chosen, and reads what the actions need without drawing it', () => {
      expect(list(signIn).title).toBe('Crew sign-in');
      expect(list(signIn).listColumns.map((column) => [column.id, column.header, column.hidden === true])).toEqual([
        ['label', 'Sign-in method', false],
        ['admits', 'Who can sign in with it', false],
        ['openSignUp', 'Sign-up', false],
        // With tenants a person who signs up creates a sector of their own: no role is chosen here.
        ['says', 'A person with no invitation', false],
        // The Auth has a password to reset, so the page says whether a person may ask for a link themselves.
        ['selfServiceReset', 'People can reset their own password', false],
        ['name', 'Name', true],
        ['directoryGivesRoles', 'Directory gives roles', true],
        ['ownRule', 'Decided by its own rule', true],
      ]);
      const choice = list(signIn).listColumns.find((column) => column.id === 'openSignUp')?.valueGetter;
      expect([password, google, entra, partners, widened].map((row) => choice?.(row))).toEqual([
        'Open',
        'Invite only',
        'Set by the directory',
        'Set by the application',
        'Invite only',
      ]);
    });

    it("offers Open sign-up on a method that is invite only, Make invite only on one that is open, and neither where the directory gives the roles or the registration's own rule decides", () => {
      const open = presentation(list(signIn).rowMethods, 'OpenCrewSignUp');
      const close = presentation(list(signIn).rowMethods, 'MakeCrewInviteOnly');
      expect(methodsOf(list(signIn).rowMethods)).toEqual([
        ['OpenCrewSignUp', 'Open sign-up'],
        ['MakeCrewInviteOnly', 'Make invite only'],
        ['SetCrewSelfServiceReset', 'Let people reset their own password'],
        ['SetCrewSelfServiceReset', 'Stop people resetting their own password'],
      ]);
      expect([password, google, entra, partners, widened].map((row) => [open.when?.(row), close.when?.(row)])).toEqual([
        [false, true],
        [true, false],
        [false, false],
        // The application's own rule decides: nothing to open, nothing to close.
        [false, false],
        // Opened for a narrower audience than the method has now: it is opened again.
        [true, false],
      ]);
      // Neither method names a target row: the page fills the method's name from the row.
      expect(open.fill?.(google)).toEqual({ name: 'google' });
      expect(close.fill?.(password)).toEqual({ name: 'password' });
    });

    it("offers the change that is left to make to a person's own password reset, on the password method alone, and says which way it stands", () => {
      const [allow, stop] = (list(signIn).rowMethods || []).filter((p) => p.method === 'SetCrewSelfServiceReset');
      const on: RecordData = { ...password, selfServiceReset: true };
      // Off, or never chosen: the page offers to let people ask. On: it offers to stop them.
      expect([password, on, google, entra].map((row) => [allow.when?.(row), stop.when?.(row)])).toEqual([
        [true, false],
        [false, true],
        [false, false],
        [false, false],
      ]);
      // One method says it either way, and names no row: each entry fills its own answer.
      expect(allow.fill?.(password)).toEqual({ on: true });
      expect(stop.fill?.(on)).toEqual({ on: false });
      expect(allow.confirm).toBe(
        'The sign-in page will offer "Forgot your password?". A person who asks is mailed a link to the email address on their account.',
      );
      expect(stop.confirm).toBe(
        'The sign-in page will no longer offer "Forgot your password?". A link already mailed still works, and an administrator can still send one.',
      );
      const stands = list(signIn).listColumns.find((column) => column.id === 'selfServiceReset')?.valueGetter;
      expect([password, on, google].map((row) => stands?.(row))).toEqual(['No', 'Yes', '']);
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
      const member = {
        ...google,
        openSignUp: true,
        defaultRole: 'Member',
        says: 'Open: Anyone with a Google account will be able to sign in without an invitation. Each of them gets the role Member.',
      };
      expect(notice?.([password, member])).toBe(
        'Password is open to sign-up. Anyone with an email address will be able to sign up and create a sector of their own, without an invitation.\n' +
          'Google is open to sign-up. Anyone with a Google account will be able to sign in without an invitation. Each of them gets the role Member.',
      );
    });

    it('shows the role a new person gets where the application has no tenants', () => {
      const [, staffSignIn] = identityPages(descriptor, resourceMeta, { auth: 'Staff', scope: 'global' });
      expect(staffSignIn.routeData.route).toBe('staff-sign-in');
      expect(columns(list(staffSignIn)).filter(([id]) => id === 'defaultRole')).toEqual([
        ['defaultRole', 'Role a new person gets'],
      ]);
    });

    it("draws no column for a person's own password reset where the description names no method that sets it", () => {
      const [, staffSignIn] = identityPages(descriptor, resourceMeta, { auth: 'Staff', scope: 'global' });
      expect(columns(list(staffSignIn)).map(([id]) => id)).not.toContain('selfServiceReset');
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
      ['SendCrewUserPasswordResetLink', 'Send a password reset link'],
      ['RequireCrewPasswordChange', 'Require a new password'],
      ['EndCrewUserSessions', 'Sign out everywhere'],
      ['RevokeCrewUserTrustedDevices', 'Forget trusted devices'],
      ['DisableCrewUser', 'Disable'],
      ['EnableCrewUser', 'Enable'],
      ['DeleteCrewUser', 'Delete'],
    ]);
    // An account is added by an invitation, and by nothing else on the page.
    expect(methodsOf(list(people).listMethods)).toEqual([['InviteCrewUser', 'Invite']]);
  });

  it('says whether the message of a method that mails a person went, in the words the server gives when it did not', () => {
    const reset = presentation(list(people).rowMethods, 'SendCrewUserPasswordResetLink');
    const address = presentation(list(people).rowMethods, 'SetCrewUserEmail');
    expect(reset.confirm).toBe(
      'A link to choose a new password is mailed to the email address on the account. Nothing changes until the link is used.',
    );
    expect(reset.answered?.({ sent: true, reason: '' })).toEqual({ message: 'The reset link is on its way.' });
    expect(
      reset.answered?.({ sent: false, reason: 'No link was sent: the account has no password to reset.' }),
    ).toEqual({
      message: 'No link was sent: the account has no password to reset.',
      failed: true,
    });
    expect(reset.answered?.({ sent: false, reason: '' })).toEqual({
      message: 'The message could not be sent.',
      failed: true,
    });
    expect(address.answered?.({ sent: true, reason: '' })?.message).toContain(
      'The account keeps its address until the link is used.',
    );
    expect(address.answered?.({ sent: false, reason: 'Change it in the directory.' })).toEqual({
      message: 'Change it in the directory.',
      failed: true,
    });
  });

  it('holds Enable to a disabled account and Disable to an enabled one, and asks before each', () => {
    const disable = presentation(list(people).rowMethods, 'DisableCrewUser');
    const enable = presentation(list(people).rowMethods, 'EnableCrewUser');
    const ada: RecordData = { username: 'ada', disabled: false };
    expect([disable.when?.(ada), enable.when?.(ada)]).toEqual([true, false]);
    expect([disable.when?.({ ...ada, disabled: true }), enable.when?.({ ...ada, disabled: true })]).toEqual([
      false,
      true,
    ]);
    expect((disable.confirm as (row: RecordData) => string)(ada)).toBe(
      'Disable ada? They cannot sign in until the account is enabled again.',
    );
  });

  it("fills the id of the person's account in the method that gives a role, which names no target row, and names the tenant in the application's word", () => {
    const give = presentation(list(people).rowMethods, 'AssignCrewRole');
    expect(give.fill?.({ username: 'ada', id: 'u-1' })).toEqual({ userId: 'u-1' });
    // A role is given for a period: the dialog asks for its two days in plain words.
    expect(give.labels).toEqual({
      domain: 'Sector',
      everyDomain: 'In every sector',
      startsAt: 'From',
      endsAt: 'Until',
    });
    expect(presentation(list(people).listMethods, 'InviteCrewUser').labels).toEqual({
      domain: 'Sector',
      everyDomain: 'In every sector',
    });
  });

  it("asks where a role is to be held and offers the roles the dialog's own method could give there: each dialog that gives one names its method and the place in its role picker's filter", () => {
    const invite = narrowed(presentation(list(people).listMethods, 'InviteCrewUser'), 'roles');
    const give = narrowed(presentation(list(people).rowMethods, 'AssignCrewRole'), 'role');
    for (const [where, method] of [
      [invite, 'InviteCrewUser'],
      [give, 'AssignCrewRole'],
    ] as const) {
      // The roles the dialog's own method takes from the caller: one sector's where a
      // sector is chosen; else the whole application's, or every sector's, as "In every
      // sector" says.
      expect(where({ domain: 'a1111111-1111-4111-8111-111111111111', everyDomain: false })).toBe(
        `method:eq:${method},domain:eq:a1111111-1111-4111-8111-111111111111`,
      );
      expect(where({ domain: '', everyDomain: false })).toBe(`method:eq:${method},everyDomain:eq:false`);
      expect(where({ domain: '', everyDomain: '' })).toBe(`method:eq:${method},everyDomain:eq:false`);
      expect(where({ domain: '', everyDomain: true })).toBe(`method:eq:${method},everyDomain:eq:true`);
    }
    // A sector's key is quoted where the filter would read it as more than one value.
    expect(invite({ domain: 'anvil, east' })).toBe('method:eq:InviteCrewUser,domain:eq:"anvil, east"');
    // With sectors, the dialog asks where: the page fills no place.
    expect(presentation(list(people).listMethods, 'InviteCrewUser').fill).toBeUndefined();
  });

  it('asks a list that carries no method field for none, and names no place where the application has no tenants', () => {
    // A list that names no method lists the same roles whichever dialog asks: the place alone is named.
    const plain = (resource: Resource): ResourceMeta =>
      resource === 'CrewGivableRoles' ? fields('name', 'description', 'domain', 'everyDomain') : resourceMeta(resource);
    const [unnamed] = identityPages({ ...descriptor }, plain, { auth: 'Crew', scope: 'global' });
    expect(
      narrowed(presentation(list(unnamed).listMethods, 'InviteCrewUser'), 'roles')({ domain: '', everyDomain: true }),
    ).toBe('everyDomain:eq:true');
    // A side whose description names no list of the roles to give is asked the same way.
    const [wholeApplication, tenant] = crew.sides;
    const lists = { ...wholeApplication.lists, rolesToGive: undefined };
    const unlisted = { ...descriptor, identity: [{ ...crew, sides: [{ ...wholeApplication, lists }, tenant] }] };
    const [without] = identityPages(unlisted as ApiDescriptor, resourceMeta, { auth: 'Crew', scope: 'global' });
    expect(
      narrowed(presentation(list(without).rowMethods, 'AssignCrewRole'), 'role')({ domain: '', everyDomain: false }),
    ).toBe('everyDomain:eq:false');
    // Without tenants every role is held for the whole application: the method alone is named.
    const office: IdentityDescriptor = {
      auth: 'Office',
      label: 'Office',
      sides: [
        {
          scope: 'global',
          lists: { users: 'CrewSessionUsers', roles: 'CrewUserRoles', rolesToGive: 'CrewSectorRoles' },
          methods: { invite: 'InviteOfficeUser', assignRole: 'AssignOfficeRole' },
        },
      ],
    };
    const [solo] = identityPages({ ...descriptor, identity: [office] } as ApiDescriptor, resourceMeta, {
      auth: 'Office',
      scope: 'global',
    });
    expect(narrowed(presentation(list(solo).listMethods, 'InviteOfficeUser'), 'roles')({})).toBe(
      'method:eq:InviteOfficeUser',
    );
    expect(narrowed(presentation(list(solo).rowMethods, 'AssignOfficeRole'), 'role')({})).toBe(
      'method:eq:AssignOfficeRole',
    );
  });

  it("draws a person's row page from the account's fields, with the roles, the identities and the sessions under it", () => {
    const section = list(people).elements[0] as unknown as {
      label: string;
      children: { name: string; label: string }[];
    };
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
    expect(
      children(people).map((child) => [child.title, child.primaryResource, child.parentRelation, child.showViewButton]),
    ).toEqual([
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
    // key the membership carries and shows its name after the sector's word, since the
    // heading does not say what the name is a name of. A reader who may not read the
    // record is shown the name their own list of sectors holds (tenantKey). The words
    // below are what a row says that names no sector, or whose sector's name the reader
    // may not read: words a person reads, and never the sector's key.
    const where = roles.listColumns[1] as MultiColumnConfig;
    expect(where.additionalIds).toEqual([{ resource: 'Sectors', id: 'id', field: 'name' }]);
    expect(where.tenantKey).toBe(true);
    expect(where.said?.('Anvil', {})).toBe('Sector Anvil');
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
        where: 'A sector you cannot see',
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
      expect(heldIn(tt.row)).not.toContain('a1111111');
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

  it('where the sector record marks no name there is none to read, and a row shows the key after the word', () => {
    // The key is all such an application has for a sector: nobody is refused a name here.
    const unnamed = (resource: Resource): ResourceMeta =>
      resource === 'Sectors' ? { ...metas[resource], nameField: undefined } : metas[resource];
    const [unnamedPeople] = identityPages({ ...descriptor }, unnamed, { auth: 'Crew', scope: 'global' });
    const where = children(unnamedPeople)[0].listColumns[1] as { valueGetter?: (row: RecordData) => string };
    expect(where.valueGetter?.({ kind: 'domain', domainId: 's-1' })).toBe('Sector s-1');
    expect(where.valueGetter?.({ kind: 'every', domainId: '' })).toBe('Every sector');
  });

  it("where the outlet serves no sector record there is none to read: a row names the sector from the person's own list of tenants, or says the page's words, and never the key", () => {
    const unserved = { ...descriptor, identity: [{ ...crew, tenantResource: undefined }] } as ApiDescriptor;
    const [unservedPeople] = identityPages(unserved, resourceMeta, { auth: 'Crew', scope: 'global' });
    const where = children(unservedPeople)[0].listColumns[1] as MultiColumnConfig;
    // No record is read, and the list asks the person's own tenants for the name.
    expect(where.additionalIds).toEqual([]);
    expect(where.tenantKey).toBe(true);
    expect(where.said?.('Anvil', {})).toBe('Sector Anvil');
    const said = where.fallback as (row: RecordData) => string;
    expect(said({ kind: 'domain', domainId: 'a1111111-1111-4111-8111-111111111111' })).toBe('A sector you cannot see');
    expect(said({ kind: 'global', domainId: '' })).toBe('The whole application');
  });

  // The mailed links as the generator writes them: who sent a link and when its mail
  // failed are fields a reader may be granted.
  const sentMetas = (resource: Resource): ResourceMeta =>
    resource === 'CrewSessionMailLinks'
      ? { ...metas[resource], fields: [...metas[resource].fields, ...fields('invitedByUserId', 'mailFailedAt').fields] }
      : metas[resource];
  const dates = ['createdAt', 'expiresAt', 'consumedAt'];
  const cell = (config: RootConfig, id: string): ((row: RecordData) => string) | undefined =>
    list(config).listColumns.find((column) => column.id === id)?.valueGetter;

  it('says a link whose message could not be mailed was not mailed and ended, and leaves its Used cell empty', () => {
    // The pages of a descriptor are made once, so pages over other fields take a descriptor of their own.
    const [, links] = identityPages({ ...descriptor }, sentMetas, { auth: 'Crew', scope: 'global' });
    const unmailed = {
      createdAt: '2026-11-20T10:00:00Z',
      expiresAt: '2026-11-27T10:00:00Z',
      consumedAt: '2026-11-20T10:00:01Z',
      mailFailedAt: '2026-11-20T10:00:01Z',
    };
    expect(dates.map((id) => cell(links, id)?.(unmailed))).toEqual(['Not mailed', 'Ended', '']);
    // A link that was used or withdrawn shows its dates, and one that stands has no Used.
    const used = { ...unmailed, mailFailedAt: null };
    expect(dates.map((id) => cell(links, id)?.(used))).toEqual([used.createdAt, used.expiresAt, used.consumedAt]);
    expect(cell(links, 'consumedAt')?.({ ...used, consumedAt: null })).toBe('');
    // When the mail failed is read with each row and not drawn, and the other columns are left alone.
    const unseen = list(links).listColumns.filter((column) => column.hidden);
    expect(unseen.map((column) => column.id)).toEqual(['everyTenant', 'invitedByUserId', 'mailFailedAt']);
    expect(cell(links, 'email')).toBeUndefined();
    // It has nothing left to withdraw.
    expect(presentation(list(links).rowMethods, 'WithdrawCrewInvitation').when?.(unmailed)).toBe(false);
    // Where the reader may not read when the mail failed, the cells are the fields' own.
    expect(dates.map((id) => cell(invitations, id))).toEqual([undefined, undefined, undefined]);
  });

  it("says the first administrator's own invitation gives roles in the whole application and in every sector", () => {
    // The pages of a descriptor are made once, so pages over other fields take a descriptor of their own.
    const [, links] = identityPages({ ...descriptor }, sentMetas, { auth: 'Crew', scope: 'global' });
    const into = (config: RootConfig): MultiColumnConfig => list(config).listColumns[3] as MultiColumnConfig;
    const first = {
      kind: 'invitation',
      tenantId: null,
      everyTenant: false,
      invitedByUserId: null,
      roleNames: ['SuperAdministrator'],
    };
    expect(into(links).fallback?.(first)).toBe('The whole application and every sector');
    // An invitation a person sent, and one that gives another role, say the place the row keeps.
    const sent = { ...first, invitedByUserId: 'f0000000-0000-4000-8000-000000000001' };
    expect(into(links).fallback?.(sent)).toBe('The whole application');
    expect(into(links).fallback?.({ ...first, roleNames: ['Clerk'] })).toBe('The whole application');
    expect(into(links).fallback?.({ ...first, everyTenant: true })).toBe('Every sector');
    expect(into(links).fallback?.({ ...first, roleNames: [] })).toBe('');
    // Where the reader may not read who sent a link, the row says the place it keeps.
    expect(into(invitations).fallback?.(first)).toBe('The whole application');
  });

  it('offers Withdraw on a mailed link that was not used, End the session on a session, and Unlock on a counted name', () => {
    const withdraw = presentation(list(invitations).rowMethods, 'WithdrawCrewInvitation');
    expect([withdraw.when?.({ consumedAt: null }), withdraw.when?.({ consumedAt: '2026-01-01T00:00:00Z' })]).toEqual([
      true,
      false,
    ]);
    expect(list(invitations).listMethods).toEqual([]);
    expect(columns(list(invitations)).map(([, header]) => header)).toEqual([
      'Kind',
      'Email',
      'Name',
      'Sector',
      'Roles',
      'Sent',
      'Expires',
      'Used',
      // Whether a link invites into every sector is read with each row and not drawn.
      undefined,
    ]);
    const unseen = list(invitations).listColumns.filter((column) => column.hidden);
    expect(unseen.map((column) => column.id)).toEqual(['everyTenant']);
    // The sector a link invites into is named the same way, under a heading that is the
    // sector's word already, so the name stands alone. Where the reader may not read the
    // sector's name the cell says so, and never shows the key.
    const into = list(invitations).listColumns[3] as MultiColumnConfig;
    expect(into.additionalIds).toEqual([{ resource: 'Sectors', id: 'id', field: 'name' }]);
    expect(into.tenantKey).toBe(true);
    expect(into.said).toBeUndefined();
    expect(into.fallback?.({ tenantId: 'a1111111-1111-4111-8111-111111111111' })).toBe('A sector you cannot see');
    expect(into.fallback?.({ tenantId: null })).toBe('');
    // A link that names no sector says where its roles are held: in every sector, or in
    // the whole application. One that gives no role is held nowhere and says nothing: a
    // verification, and an invitation that makes a sector.
    expect(into.fallback?.({ tenantId: null, everyTenant: true, roleNames: ['Pilot'] })).toBe('Every sector');
    expect(into.fallback?.({ tenantId: null, everyTenant: false, roleNames: ['Clerk'] })).toBe('The whole application');
    expect(into.fallback?.({ tenantId: null, everyTenant: false, roleNames: [] })).toBe('');
    expect(methodsOf(list(sessions).rowMethods)).toEqual([['EndCrewSession', 'End the session']]);
    expect(columns(list(sessions)).map(([id]) => id)).toEqual(['username', 'authenticatedAt', 'updatedAt', 'expired']);
    expect(methodsOf(list(attempts).rowMethods)).toEqual([['UnlockCrewSignIn', 'Unlock']]);
    expect(methodsOf(children(people)[2].rowMethods)).toEqual([['EndCrewSession', 'End the session']]);
  });

  it('says nothing of where a mailed link gives its roles where the reader may not read the every-sector mark', () => {
    // Without the mark an invitation into every sector reads like one into the whole
    // application, so the list says neither.
    const unmarked = (resource: Resource): ResourceMeta =>
      resource === 'CrewSessionMailLinks'
        ? fields('id', 'kind', 'email', 'name', 'tenantId', 'roleNames', 'createdAt', 'expiresAt', 'consumedAt')
        : metas[resource];
    const [, unmarkedLinks] = identityPages({ ...descriptor }, unmarked, { auth: 'Crew', scope: 'global' });
    expect(list(unmarkedLinks).listColumns.some((column) => column.hidden)).toBe(false);
    const into = list(unmarkedLinks).listColumns[3] as MultiColumnConfig;
    expect(into.fallback?.({ tenantId: null, roleNames: ['Clerk'] })).toBe('');
  });
});

describe("identityPages, a tenant's side", () => {
  const pages = identityPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'domain' });
  const [people, invitations] = pages;

  it('shows who a directory-only tenant keeps out, where the list of its people says it', () => {
    const withSwitch = (resource: Resource): ResourceMeta =>
      resource === 'CrewSectorUsers'
        ? fields('id', 'domainId', 'username', 'name', 'lastSignInAt', 'heldElsewhere', 'heldGlobally', 'cannotEnter')
        : resourceMeta(resource);
    // The pages are made once for a description, so this record's are asked of its own.
    const [kept] = identityPages({ ...descriptor }, withSwitch, { auth: 'Crew', scope: 'domain' });
    expect(columns(list(kept)).at(-1)).toEqual(['cannotEnter', 'Cannot enter']);
    // A tenant record with no switch carries no such fact, and the page no such column.
    expect(columns(list(people)).map(([id]) => id)).not.toContain('cannotEnter');
  });

  it("builds the people and the invitations under the API's tenant segment", () => {
    expect(pages.map((page) => [page.routeData.route, page.page?.label, page.page?.icon])).toEqual([
      ['sectors/:sectorID/crew-people', 'People', 'group'],
      ['sectors/:sectorID/crew-invitations', 'Invitations', 'mail'],
    ]);
  });

  it('names the tenant record in its words and leaves out what the side does not have', () => {
    expect(columns(list(people)).at(-1)).toEqual(['heldElsewhere', 'Also in another sector']);
    // No password method on this side of the description: no column for it.
    expect(columns(list(people)).map(([id]) => id)).toEqual([
      'username',
      'name',
      'email',
      'disabled',
      'lastSignInAt',
      'heldElsewhere',
    ]);
    expect(methodsOf(list(people).rowMethods)).toEqual([
      ['AssignCrewSectorRole', 'Give a role'],
      ['SetCrewSectorUserName', 'Change the name'],
      ['DisableCrewSectorUser', 'Disable'],
      ['EnableCrewSectorUser', 'Enable'],
      ['DeleteCrewSectorUser', 'Delete'],
    ]);
    expect(methodsOf(list(people).listMethods)).toEqual([['InviteCrewSectorUser', 'Invite']]);
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
    expect((remove.confirm as (row: RecordData) => string)({ role: 'Cadet', username: 'ada' })).toBe(
      'Take Cadet away from ada?',
    );
    expect(children(people).map((child) => child.title)).toEqual(['Roles held here', 'Sign-in identities']);
  });

  it('offers Withdraw on an invitation that still stands, and Invite above the list', () => {
    const withdraw = presentation(list(invitations).rowMethods, 'WithdrawCrewSectorInvitation');
    expect([withdraw.when?.({ pending: true }), withdraw.when?.({ pending: false })]).toEqual([true, false]);
    expect(methodsOf(list(invitations).listMethods)).toEqual([['InviteCrewSectorUser', 'Invite']]);
  });

  it("offers each of a tenant's dialogs the roles its own method could give: the picker names the method and no place", () => {
    const asks: [MethodPresentation, string][] = [
      [presentation(list(people).listMethods, 'InviteCrewSectorUser'), 'roles'],
      [presentation(list(invitations).listMethods, 'InviteCrewSectorUser'), 'roles'],
      [presentation(list(people).rowMethods, 'AssignCrewSectorRole'), 'role'],
    ];
    for (const [presented, field] of asks) {
      expect(narrowed(presented, field)({ domain: 'a1111111-1111-4111-8111-111111111111' })).toBe(
        `method:eq:${presented.method}`,
      );
    }
    // A tenant's list that names no method is every role of the tenant: nothing narrows it.
    const plain = (resource: Resource): ResourceMeta =>
      resource === 'CrewSectorRoles' ? fields('name', 'description') : resourceMeta(resource);
    const [unnamed] = identityPages({ ...descriptor }, plain, { auth: 'Crew', scope: 'domain' });
    expect(presentation(list(unnamed).rowMethods, 'AssignCrewSectorRole').narrows).toBeUndefined();
  });
});

describe("identityPages, a person's password", () => {
  // An Auth with a password method: each side names the method that removes a person's
  // password and the list of the ways a person signs in, which the removal is offered from.
  const withPassword = {
    ...descriptor,
    identity: [
      {
        ...crew,
        sides: [
          {
            ...crew.sides[0],
            lists: { ...crew.sides[0].lists, signInWays: 'CrewSignInWays' },
            methods: { ...crew.sides[0].methods, removePassword: 'RemoveCrewPassword' },
          },
          {
            ...crew.sides[1],
            lists: { ...crew.sides[1].lists, signInWays: 'CrewSectorSignInWays' },
            methods: { ...crew.sides[1].methods, removePassword: 'RemoveCrewSectorPassword' },
          },
        ],
      },
      members,
    ],
  } as ApiDescriptor;
  const childOf = (pages: RootConfig[], title: string): ListViewConfig | undefined =>
    children(pages[0]).find((child) => child.title === title);
  const waysOf = (pages: RootConfig[]): ListViewConfig => {
    const found = childOf(pages, 'Ways to sign in');
    if (!found) {
      throw new Error('no list of the ways a person signs in');
    }
    return found;
  };
  const identitiesOf = (pages: RootConfig[]): ListViewConfig => {
    const found = childOf(pages, 'Sign-in identities');
    if (!found) {
      throw new Error('no list of sign-in identities');
    }
    return found;
  };
  // The rows the server lists for a person: the password, and an identity that counts.
  const password = { userId: 'u-1', way: 'password', through: 'Password' };
  const google = { userId: 'u-1', way: 'i-1', through: 'Fleet Google', connection: 'fleet.example' };
  const ada = { id: 'u-1', username: 'ada' };

  for (const tt of [
    {
      name: "the whole application's side",
      scope: 'global' as const,
      ways: 'CrewSignInWays',
      remove: 'RemoveCrewPassword',
      unlink: 'UnlinkCrewSessionIdentity',
    },
    {
      name: "a tenant's side",
      scope: 'domain' as const,
      ways: 'CrewSectorSignInWays',
      remove: 'RemoveCrewSectorPassword',
      unlink: 'UnlinkCrewSectorSessionIdentity',
    },
  ]) {
    it(`lists the ways a person signs in under the person, above their identities, on ${tt.name}`, () => {
      const pages = identityPages(withPassword, resourceMeta, { auth: 'Crew', scope: tt.scope });
      const titles = children(pages[0]).map((child) => child.title);
      expect(titles.indexOf('Ways to sign in')).toBe(titles.indexOf('Sign-in identities') - 1);
      const ways = waysOf(pages);
      expect([ways.primaryResource, ways.parentRelation]).toEqual([tt.ways, { parentKey: 'id', childKey: 'userId' }]);
      expect(columns(ways)).toEqual([
        ['through', 'Signs in with'],
        ['connection', 'Connection'],
      ]);
      // No method runs against a way, and the list asks the server for none.
      expect(ways.rowMethods).toBe(false);
    });

    it(`offers Remove password above the ways a person signs in, only for a password and an identity that counts, on ${tt.name}`, () => {
      const pages = identityPages(withPassword, resourceMeta, { auth: 'Crew', scope: tt.scope });
      const ways = waysOf(pages);
      expect(methodsOf(ways.listMethods)).toEqual([[tt.remove, 'Remove password']]);
      const remove = presentation(ways.listMethods, tt.remove);
      // The method runs against the person the list sits under: the page fills nothing.
      expect(remove.fill).toBeUndefined();
      // The server lists the password a person holds and the identities that can sign
      // them in, so the offer is the server's judgment: a password and another way.
      expect(remove.when?.(ada, [password, google])).toBe(true);
      // A person the directory alone signs in holds no password to remove.
      expect(remove.when?.(ada, [google])).toBe(false);
      // A password that is the person's only way in is not removed: no identity, or
      // none the server counts (an identity that signs nobody in is no row).
      expect(remove.when?.(ada, [password])).toBe(false);
      // Nothing listed, or no list read yet: no offer.
      expect([remove.when?.(ada, []), remove.when?.(ada)]).toEqual([false, false]);
      // The list is read again once the removal ran (the library reloads the list a
      // method ran from): the password's row is gone, and the offer with it.
      expect(remove.when?.(ada, [google])).toBe(false);
      // It is offered there alone: not above the identities, where Unlink is, and not a
      // second time among the methods of the person's row.
      const identities = identitiesOf(pages);
      expect(methodsOf(identities.rowMethods)).toEqual([[tt.unlink, 'Unlink']]);
      expect(identities.listMethods ?? []).toEqual([]);
      expect(presentation(list(pages[0]).rowMethods, tt.remove).hidden).toBe(true);
    });

    it(`asks before the removal, naming what is left by the words of the ways the server listed, on ${tt.name}`, () => {
      const remove = presentation(
        waysOf(identityPages(withPassword, resourceMeta, { auth: 'Crew', scope: tt.scope })).listMethods,
        tt.remove,
      );
      const question = remove.confirm as (row: RecordData, listed?: readonly RecordData[]) => string;
      expect(question(ada, [password, google])).toBe(
        'Remove the password of ada? They will sign in through Fleet Google only.',
      );
      // Two identities through one sign-in are named once, and each other sign-in after it.
      const second = { userId: 'u-1', way: 'i-2', through: 'Fleet Google' };
      const lakeside = { userId: 'u-1', way: 'i-3', through: 'Lakeside' };
      const riverside = { userId: 'u-1', way: 'i-4', through: 'Riverside' };
      expect(question(ada, [password, google, second])).toBe(
        'Remove the password of ada? They will sign in through Fleet Google only.',
      );
      expect(question(ada, [password, google, lakeside])).toBe(
        'Remove the password of ada? They will sign in through Fleet Google or Lakeside only.',
      );
      expect(question(ada, [password, google, second, lakeside, riverside])).toBe(
        'Remove the password of ada? They will sign in through Fleet Google, Lakeside or Riverside only.',
      );
      // The password is never named as what is left.
      expect(question(ada, [password, lakeside])).not.toContain('Password');
      // A person the row does not name, and a way the caller is shown no words for.
      expect(question({ id: 'u-2' }, [password, { userId: 'u-2', way: 'i-9' }])).toBe(
        'Remove the password of this person? They will sign in only through the other ways listed here.',
      );
    });
  }

  it('offers no removal where the description names no list of the ways a person signs in: no answer is no offer', () => {
    // The method without the list: a server that does not say how a person signs in.
    const unlisted = {
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
    for (const [scope, method] of [
      ['global', 'RemoveCrewPassword'],
      ['domain', 'RemoveCrewSectorPassword'],
    ] as const) {
      const pages = identityPages(unlisted, resourceMeta, { auth: 'Crew', scope });
      expect(childOf(pages, 'Ways to sign in')).toBeUndefined();
      expect(identitiesOf(pages).listMethods ?? []).toEqual([]);
      for (const child of children(pages[0])) {
        expect(methodsOf(child.listMethods).map(([name]) => name)).not.toContain(method);
      }
      expect(presentation(list(pages[0]).rowMethods, method).hidden).toBe(true);
    }
  });

  it('lists the ways and offers no removal where the description names no such method', () => {
    const listed = {
      ...descriptor,
      identity: [
        {
          ...crew,
          sides: [
            { ...crew.sides[0], lists: { ...crew.sides[0].lists, signInWays: 'CrewSignInWays' } },
            { ...crew.sides[1], lists: { ...crew.sides[1].lists, signInWays: 'CrewSectorSignInWays' } },
          ],
        },
        members,
      ],
    } as ApiDescriptor;
    for (const scope of ['global', 'domain'] as const) {
      const pages = identityPages(listed, resourceMeta, { auth: 'Crew', scope });
      expect(waysOf(pages).listMethods).toEqual([]);
      expect(methodsOf(list(pages[0]).rowMethods).map(([method]) => method)).not.toContain(
        scope === 'global' ? 'RemoveCrewPassword' : 'RemoveCrewSectorPassword',
      );
    }
  });

  it('draws no list of ways where the description names none, as for an Auth with no password method', () => {
    for (const scope of ['global', 'domain'] as const) {
      const pages = identityPages(descriptor, resourceMeta, { auth: 'Crew', scope });
      expect(childOf(pages, 'Ways to sign in')).toBeUndefined();
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
    // An auth whose description names no tenant record holds every role in the one place:
    // the list says the role and not where, and reads nothing of a place.
    const held = children(pages[0])[0].listColumns;
    expect(held.map((column) => column.id)).toContain('role');
    expect(held.map((column) => column.header)).not.toContain('Held in');
    expect(held.map((column) => column.id)).not.toContain('kind');
    expect(held.map((column) => column.id)).not.toContain('domainId');
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

  const refusals: {
    name: string;
    descriptor: ApiDescriptor;
    auth: string;
    scope: 'global' | 'domain';
    want: string;
  }[] = [
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
    CrewSectorUserGroupRoles: fields(
      'domainId',
      'userId',
      'username',
      'groupId',
      'groupName',
      'role',
      'globalGroup',
      'everyDomain',
    ),
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
    expect(
      global.map((page) => [page.routeData.route, page.page?.label, page.page?.icon, page.routeData.hasViewRoute]),
    ).toEqual([
      ['crew-accounts', 'Accounts', 'manage_accounts', true],
      ['crew-groups', 'Groups', 'groups', true],
      ['crew-mailed-links', 'Mailed links', 'forward_to_inbox', false],
      ['crew-sessions', 'Sessions', 'devices', false],
      ['crew-failed-sign-ins', 'Failed sign-ins', 'gpp_bad', false],
      ['crew-sign-in', 'Sign-in', 'login', false],
    ]);
    expect(domain.map((page) => [page.routeData.route, page.page?.label, list(page).primaryResource])).toEqual([
      ['sectors/:sectorID/crew-people', 'People', 'CrewSectorUsers'],
      ['sectors/:sectorID/crew-groups', 'Groups', 'CrewSectorGroups'],
      ['sectors/:sectorID/crew-invitations', 'Invitations', 'CrewSectorInvitations'],
    ]);
    expect(
      identityPages(groupDescriptor, groupMeta, {
        auth: 'Crew',
        scope: 'domain',
        routes: { groups: 'sectors/:sectorID/groups' },
      })[1].routeData.route,
    ).toBe('sectors/:sectorID/groups');
  });

  it('leaves the Groups page and the roles through groups out where the Auth keeps none', () => {
    const without = identityPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'global' });
    expect(without.map((page) => page.page?.label)).toEqual([
      'Accounts',
      'Mailed links',
      'Sessions',
      'Failed sign-ins',
      'Sign-in',
    ]);
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
    expect(kept.tenantKey).toBe(true);
    expect(kept.said?.('Anvil', {})).toBe('Sector Anvil');
    const keptIn = kept.fallback as (row: RecordData) => string;
    expect(keptIn({ kind: 'global', domainId: '' })).toBe('The whole application');
    expect(keptIn({ kind: 'domain', domainId: 's-1' })).toBe('A sector you cannot see');
    expect(methodsOf(groups.rowMethods)).toEqual([
      ['DescribeCrewGroup', 'Change the description'],
      ['AddCrewGroupMember', 'Add a person'],
      ['GiveCrewGroupRole', 'Give a role'],
      ['DeleteCrewGroup', 'Delete'],
    ]);
    // A group is given a role where it is to be held, picked from the roles the method
    // could give there; the same dialog opened from the group's own page asks the same.
    const [, held] = children(global[1]);
    for (const give of [
      presentation(groups.rowMethods, 'GiveCrewGroupRole'),
      presentation(held.listMethods, 'GiveCrewGroupRole'),
    ]) {
      // The dialog was opened with the group's id, which names the group beside the
      // method: the server lists what that group may hold, by the rule its method keeps.
      expect(narrowed(give, 'role')({ groupId: 'g-1', domain: '', everyDomain: true })).toBe(
        'method:eq:GiveCrewGroupRole,group:eq:g-1,everyDomain:eq:true',
      );
      expect(narrowed(give, 'role')({ groupId: 'g-1', domain: 's-1', everyDomain: false })).toBe(
        'method:eq:GiveCrewGroupRole,group:eq:g-1,domain:eq:s-1',
      );
      // A group kept in one sector holds roles there alone, so its dialog is opened
      // with that sector and does not ask where; one kept for the whole application
      // is asked.
      expect(give.fill?.({ id: 'g-1', kind: 'domain', domainId: 's-1' })).toEqual({
        domain: 's-1',
        everyDomain: false,
      });
      expect(give.fill?.({ id: 'g-2', kind: 'global', domainId: '' })).toEqual({});
    }
    expect(methodsOf(groups.listMethods)).toEqual([['CreateCrewGroup', 'Add a group']]);
    expect(presentation(groups.listMethods, 'CreateCrewGroup').labels).toEqual({
      domain: 'Sector',
      everyDomain: 'In every sector',
    });
    expect(
      (presentation(groups.rowMethods, 'DeleteCrewGroup').confirm as (row: RecordData) => string)({
        name: 'Deck crew',
      }),
    ).toBe('Delete Deck crew? A group that still has people in it or holds a role is not deleted: empty it first.');
  });

  it("draws a group's row page with the people in it and the roles it holds, each list under the group", () => {
    expect(
      children(global[1]).map((child) => [
        child.title,
        child.primaryResource,
        child.parentRelation,
        child.showViewButton,
      ]),
    ).toEqual([
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
    expect(
      (presentation(people.rowMethods, 'RemoveCrewGroupMember').confirm as (row: RecordData) => string)({
        username: 'ada',
      }),
    ).toBe("Remove ada from the group? They keep the roles they hold themselves and lose the group's.");
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
        question:
          'Take Cadet in this sector away from the group? Everyone in the group loses it, unless they hold it some other way.',
      },
      {
        row: { groupId: 'g-1', role: 'Archivist', kind: 'every', domainId: '' },
        body: { groupId: 'g-1', role: 'Archivist', domain: null, everyDomain: true },
        question:
          'Take Archivist in every sector away from the group? Everyone in the group loses it, unless they hold it some other way.',
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
    // A tenant's group is given a role of the tenant, among the ones the method could give.
    for (const give of [
      presentation(groups.rowMethods, 'GiveCrewSectorGroupRole'),
      presentation(roles.listMethods, 'GiveCrewSectorGroupRole'),
    ]) {
      expect(narrowed(give, 'role')({})).toBe('method:eq:GiveCrewSectorGroupRole');
    }
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
    expect([here.title, here.primaryResource, here.rowMethods]).toEqual([
      'Roles held here through a group',
      'CrewSectorUserGroupRoles',
      false,
    ]);
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
    CrewSectorUserRoles: fields(
      'domainId',
      'userId',
      'username',
      'role',
      'everyDomain',
      'createdAt',
      'startsAt',
      'endsAt',
    ),
    CrewGroups: fields('id', 'kind', 'axis', 'domainId', 'name', 'description', 'updatedAt'),
    CrewGroupUsers: fields('groupId', 'userId', 'username', 'name', 'groupName', 'createdAt', 'startsAt', 'endsAt'),
    CrewGroupRoles: fields('groupId', 'kind', 'axis', 'domainId', 'role', 'createdAt', 'startsAt', 'endsAt'),
    CrewUserGroupRoles: fields(
      'userId',
      'username',
      'groupId',
      'groupName',
      'kind',
      'axis',
      'domainId',
      'role',
      'startsAt',
      'endsAt',
    ),
    CrewSectorGroups: fields('id', 'domainId', 'name', 'description', 'updatedAt'),
    CrewSectorGroupUsers: fields(
      'domainId',
      'groupId',
      'userId',
      'username',
      'name',
      'groupName',
      'createdAt',
      'startsAt',
      'endsAt',
    ),
    CrewSectorGroupRoles: fields('domainId', 'groupId', 'groupName', 'role', 'createdAt', 'startsAt', 'endsAt'),
    CrewSectorUserGroupRoles: fields(
      'domainId',
      'userId',
      'username',
      'groupId',
      'groupName',
      'role',
      'globalGroup',
      'everyDomain',
      'startsAt',
      'endsAt',
    ),
  };
  const periodMeta = (resource: Resource): ResourceMeta => periodMetas[resource];
  const [wholeApplication, tenant] = crew.sides;
  const timed: IdentityDescriptor = {
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
          addGroupMember: 'AddCrewGroupMember',
          giveGroupRole: 'GiveCrewGroupRole',
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
          addGroupMember: 'AddCrewSectorGroupMember',
          giveGroupRole: 'GiveCrewSectorGroupRole',
        },
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
    config.listColumns
      .filter((column) => !column.hidden && column.header !== undefined)
      .map((column) => [column.id, column.header]);

  it("shows the period of each role under a person, the person's own and the ones held through a group", () => {
    const [own, through] = children(global[0]);
    expect([own.title, through.title]).toEqual(['Roles held', 'Roles held through a group']);
    expect(drawn(own).slice(-3)).toEqual([['createdAt', 'Given'], ...period]);
    expect(drawn(through).slice(-2)).toEqual(period);

    const [here, hereThrough] = children(domain[0]);
    expect([here.title, hereThrough.title]).toEqual(['Roles held here', 'Roles held here through a group']);
    expect(drawn(here)).toEqual([
      ['role', 'Role'],
      ['everyDomain', 'In every sector'],
      ['createdAt', 'Given'],
      ...period,
    ]);
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

  it('shows the end of a period as its last day, the day a person picked, and nothing where there is none', () => {
    const shown = (config: ListViewConfig, id: string, row: RecordData): unknown => {
      const column = config.listColumns.find((candidate) => candidate.id === id);
      if (!column) {
        throw new Error(`no column ${id}`);
      }
      return column.valueGetter ? column.valueGetter(row) : row[id];
    };
    // "Until 1 December", picked in the viewer's time zone, is kept as the first instant
    // of 2 December there. The list reads the instant before it, which the table writes
    // as a calendar date: 1 December.
    const kept = new Date(2026, 11, 2).toISOString();
    // Under a person the roles they hold and the ones held through a group; under a group its people and its roles.
    const lists = [global[0], domain[0], global[1], domain[1]].flatMap((page) => children(page).slice(0, 2));
    expect(lists).toHaveLength(8);
    for (const config of lists) {
      const until = new Date(shown(config, 'endsAt', { endsAt: kept }) as string);
      expect([until.getFullYear(), until.getMonth(), until.getDate()], config.title).toEqual([2026, 11, 1]);
      // An end kept by another caller at any instant shows the last day it falls on.
      const afternoon = new Date(
        shown(config, 'endsAt', { endsAt: new Date(2026, 11, 1, 15, 30).toISOString() }) as string,
      );
      expect([afternoon.getMonth(), afternoon.getDate()], config.title).toEqual([11, 1]);
      expect(shown(config, 'endsAt', { endsAt: null }), config.title).toBe('');
      expect(shown(config, 'endsAt', {}), config.title).toBe('');
      // The first day is the instant it is kept as.
      expect(shown(config, 'startsAt', { startsAt: kept }), config.title).toBe(kept);
    }
  });

  it('says in each method that gives something for a period that the day picked for its end is its last day', () => {
    const asked: [RootConfig, string][] = [
      [global[0], 'AssignCrewRole'],
      [domain[0], 'AssignCrewSectorRole'],
      [global[1], 'AddCrewGroupMember'],
      [global[1], 'GiveCrewGroupRole'],
      [domain[1], 'AddCrewSectorGroupMember'],
      [domain[1], 'GiveCrewSectorGroupRole'],
    ];
    for (const [page, method] of asked) {
      expect(presentation(list(page).rowMethods, method).throughDay, method).toEqual(['endsAt']);
    }
    for (const [side, add, give] of [
      [global, 'AddCrewGroupMember', 'GiveCrewGroupRole'],
      [domain, 'AddCrewSectorGroupMember', 'GiveCrewSectorGroupRole'],
    ] as [RootConfig[], string, string][]) {
      const [people, roles] = children(side[1]);
      expect(presentation(people.listMethods, add).throughDay, add).toEqual(['endsAt']);
      expect(presentation(roles.listMethods, give).throughDay, give).toEqual(['endsAt']);
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

describe('identityPages, what the notice says once a method has run', () => {
  const [wholeApplication, tenant] = crew.sides;
  // Every method a side can name, each under a name of its own.
  const groupLists = { groups: 'CrewGroups', groupUsers: 'CrewGroupUsers', groupRoles: 'CrewGroupRoles' };
  const every: IdentityDescriptor = {
    ...crew,
    sides: [
      {
        ...wholeApplication,
        lists: { ...wholeApplication.lists, ...groupLists, signInWays: 'CrewSignInWays' },
        methods: {
          ...wholeApplication.methods,
          removePassword: 'RemoveCrewPassword',
          createGroup: 'CreateCrewGroup',
          describeGroup: 'DescribeCrewGroup',
          deleteGroup: 'DeleteCrewGroup',
          addGroupMember: 'AddCrewGroupMember',
          removeGroupMember: 'RemoveCrewGroupMember',
          giveGroupRole: 'GiveCrewGroupRole',
          takeGroupRole: 'TakeCrewGroupRole',
        },
      },
      tenant,
    ],
  };
  const everyMetas: Record<string, ResourceMeta> = {
    ...metas,
    CrewGroups: fields('id', 'kind', 'axis', 'domainId', 'name', 'description', 'updatedAt'),
    CrewGroupUsers: fields('groupId', 'userId', 'username', 'name', 'groupName', 'createdAt'),
    CrewGroupRoles: fields('groupId', 'kind', 'axis', 'domainId', 'role', 'createdAt'),
  };
  const everyDescriptor = { ...descriptor, identity: [every] } as ApiDescriptor;
  const sides = (['global', 'domain'] as const).map((scope) =>
    identityPages(everyDescriptor, (resource) => everyMetas[resource], { auth: 'Crew', scope }),
  );
  /** Every offer of a method the pages of a side make: on the rows and above the list, of each page and of each list under a row. */
  const offers = (pages: RootConfig[]): MethodPresentation[] =>
    pages
      .flatMap((page) => [list(page), ...((page.relatedConfigs ?? []) as ListViewConfig[])])
      .flatMap((config) => [...(config.rowMethods || []), ...(config.listMethods ?? [])]);

  it('says what happened in a sentence, for every method the pages offer: none is left to its label and "done"', () => {
    const [global, domain] = sides.map(offers);
    // The pages offer every method the two sides name.
    const named = [...Object.values(every.sides[0].methods), ...Object.values(every.sides[1].methods)];
    expect([...new Set([...global, ...domain].map((offer) => offer.method))].sort()).toEqual([...named].sort());
    for (const offer of [...global, ...domain]) {
      const said = offer.answered?.({ sent: true })?.message ?? offer.successMessage;
      expect(said, offer.method).toMatch(/^[A-Z].*\.$/);
      expect(said, offer.method).not.toContain(': done');
    }
    const words = (side: MethodPresentation[], method: string): string | undefined =>
      presentation(side, method).successMessage;
    expect(words(global, 'AssignCrewRole')).toBe('The role was given.');
    expect(words(global, 'RemoveCrewRole')).toBe('The role was taken away.');
    expect(words(global, 'GiveCrewGroupRole')).toBe('The role was given to the group.');
    expect(words(global, 'WithdrawCrewInvitation')).toBe('The invitation is withdrawn.');
    expect(words(global, 'DisableCrewUser')).toBe('The account is disabled.');
    // Words a page gives an offer of its own are kept.
    expect(words(global, 'MakeCrewInviteOnly')).toBe('The method is invite only.');
  });

  it('reads whether an invitation was mailed from its answer, and says a failed one was withdrawn and what to do', () => {
    for (const [side, method] of [
      [sides[0], 'InviteCrewUser'],
      [sides[1], 'InviteCrewSectorUser'],
    ] as const) {
      const invite = presentation(offers(side), method);
      expect(invite.successMessage, method).toBeUndefined();
      expect(invite.answered?.({ id: 'i-1', sent: true }), method).toEqual({
        message: 'The invitation is on its way.',
      });
      for (const unsent of [{ id: 'i-1', sent: false }, {}, undefined]) {
        expect(invite.answered?.(unsent), method).toEqual({
          message: 'The invitation could not be mailed, so it was withdrawn. Invite the person again.',
          failed: true,
        });
      }
    }
  });
});

describe('identityPages, an application without tenants', () => {
  // The generator writes the same lists for every application: a role row says its kind
  // and a mailed link carries a tenant. The methods that give a role, take one away or
  // keep a group have no field that says where, and the roles to give name no place.
  const officeMetas: Record<string, ResourceMeta> = {
    OfficeSessionUsers: fields('id', 'username', 'name', 'email', 'disabled', 'lastSignInAt'),
    OfficeUserRoles: fields('kind', 'axis', 'domainId', 'role', 'userId', 'createdAt'),
    OfficeGivableRoles: fields('name', 'description', 'method', 'group'),
    OfficeSessionMailLinks: fields(
      'id',
      'kind',
      'email',
      'name',
      'tenantId',
      'everyTenant',
      'roleNames',
      'createdAt',
      'expiresAt',
      'consumedAt',
    ),
    OfficeGroups: fields('id', 'kind', 'axis', 'domainId', 'name', 'description', 'updatedAt'),
    OfficeGroupUsers: fields('groupId', 'userId', 'username', 'name', 'groupName', 'createdAt'),
    OfficeGroupRoles: fields('groupId', 'kind', 'axis', 'domainId', 'role', 'createdAt'),
    OfficeUserGroupRoles: fields('userId', 'username', 'groupId', 'groupName', 'kind', 'axis', 'domainId', 'role'),
  };
  const office: IdentityDescriptor = {
    auth: 'Office',
    label: 'Office',
    sides: [
      {
        scope: 'global',
        lists: {
          users: 'OfficeSessionUsers',
          roles: 'OfficeUserRoles',
          rolesToGive: 'OfficeGivableRoles',
          invitations: 'OfficeSessionMailLinks',
          groups: 'OfficeGroups',
          groupUsers: 'OfficeGroupUsers',
          groupRoles: 'OfficeGroupRoles',
          rolesThroughGroups: 'OfficeUserGroupRoles',
        },
        methods: {
          invite: 'InviteOfficeUser',
          withdrawInvitation: 'WithdrawOfficeInvitation',
          assignRole: 'AssignOfficeRole',
          removeRole: 'RemoveOfficeRole',
          createGroup: 'CreateOfficeGroup',
          describeGroup: 'DescribeOfficeGroup',
          addGroupMember: 'AddOfficeGroupMember',
          giveGroupRole: 'GiveOfficeGroupRole',
          takeGroupRole: 'TakeOfficeGroupRole',
        },
      },
    ],
  };
  const officeDescriptor = { ...descriptor, domainRoute: undefined, identity: [office] } as ApiDescriptor;
  const [accounts, groups, mailed] = identityPages(officeDescriptor, (resource) => officeMetas[resource], {
    auth: 'Office',
    scope: 'global',
  });
  const [roles, throughGroups] = children(accounts);
  const [, groupRoles] = children(groups);
  /** What a page fills a method's request with from a row: nothing where it fills nothing. */
  const filled = (offer: MethodPresentation, row: RecordData): Record<string, unknown> => offer.fill?.(row) ?? {};

  it('sends no place: the methods have no field that says where, and a request is sent as filled', () => {
    // The server refuses a field a method does not have, so a page that filled "no
    // tenant" would have every one of these refused.
    expect(filled(presentation(list(accounts).listMethods, 'InviteOfficeUser'), {})).toEqual({});
    // The id of the person's account is filled, as with tenants, and nothing beside it.
    expect(filled(presentation(list(accounts).rowMethods, 'AssignOfficeRole'), { id: 'u-1' })).toEqual({
      userId: 'u-1',
    });
    expect(filled(presentation(list(groups).rowMethods, 'GiveOfficeGroupRole'), { id: 'g-1' })).toEqual({});
    expect(filled(presentation(groupRoles.listMethods, 'GiveOfficeGroupRole'), { id: 'g-1' })).toEqual({});
    expect(filled(presentation(list(groups).listMethods, 'CreateOfficeGroup'), {})).toEqual({});
    // The group's dialog names the group beside its method, and no place.
    for (const give of [
      presentation(list(groups).rowMethods, 'GiveOfficeGroupRole'),
      presentation(groupRoles.listMethods, 'GiveOfficeGroupRole'),
    ]) {
      expect(narrowed(give, 'role')({ groupId: 'g-1' })).toBe('method:eq:GiveOfficeGroupRole,group:eq:g-1');
    }
    // A method that never named a place is filled with none either.
    expect(presentation(list(groups).rowMethods, 'DescribeOfficeGroup').fill).toBeUndefined();
    expect(presentation(list(groups).rowMethods, 'AddOfficeGroupMember').fill).toBeUndefined();
  });

  it('names no place in a list: what a person or a group holds is held for the whole application, which is all there is', () => {
    expect(columns(roles)).toEqual([
      ['role', 'Role'],
      ['createdAt', 'Given'],
    ]);
    expect(columns(throughGroups)).toEqual([
      ['role', 'Role'],
      ['groupName', 'Group'],
    ]);
    expect(columns(list(groups))).toEqual([
      ['name', 'Name'],
      ['description', 'Description'],
    ]);
    expect(columns(groupRoles)).toEqual([
      ['role', 'Role'],
      ['createdAt', 'Given'],
    ]);
    expect(columns(list(mailed))).toEqual([
      ['kind', 'Kind'],
      ['email', 'Email'],
      ['name', 'Name'],
      ['roleNames', 'Roles'],
      ['createdAt', 'Sent'],
      ['expiresAt', 'Expires'],
      ['consumedAt', 'Used'],
    ]);
    // A role is taken away from where it is held, the whole application: the request and
    // the question name no place, whatever kind the row says.
    const remove = presentation(roles.rowMethods, 'RemoveOfficeRole');
    expect(remove.fill?.({ userId: 'u-1', role: 'Clerk', kind: 'global', domainId: '' })).toEqual({
      userId: 'u-1',
      role: 'Clerk',
    });
    expect((remove.confirm as (row: RecordData) => string)({ role: 'Clerk' })).toBe(
      'Take Clerk away from this person?',
    );
    const take = presentation(groupRoles.rowMethods, 'TakeOfficeGroupRole');
    expect(take.fill?.({ groupId: 'g-1', role: 'Clerk', kind: 'global', domainId: '' })).toEqual({
      groupId: 'g-1',
      role: 'Clerk',
    });
  });

  it('says "tenant" nowhere: no heading, no title, no button and no word above an input', () => {
    const lists = [accounts, groups, mailed].flatMap((page) => [
      list(page),
      ...((page.relatedConfigs ?? []) as ListViewConfig[]),
    ]);
    const drawn = [
      ...[accounts, groups, mailed].map((page) => page.page?.label),
      ...lists.flatMap((config) => [
        config.title,
        ...config.listColumns.filter((column) => !column.hidden).map((column) => column.header),
        ...[...(config.rowMethods || []), ...(config.listMethods ?? [])].flatMap((offer) => [
          offer.label,
          offer.successMessage,
          ...Object.values(offer.labels ?? {}),
        ]),
      ]),
    ].filter((words): words is string => typeof words === 'string');
    expect(drawn).toContain('Invite');
    expect(drawn.filter((words) => /tenant|whole application/i.test(words))).toEqual([]);
  });
});
