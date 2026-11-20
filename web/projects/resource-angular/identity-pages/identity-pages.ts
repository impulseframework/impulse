import { Route } from '@angular/router';
import {
  ApiDescriptor,
  IdentityDescriptor,
  IdentitySideDescriptor,
  quoteFilterValue,
  ScopeKind,
} from '@impulseframework/resource';
import { Page } from '@impulseframework/resource-angular/auth-frame';
import { resourcePage, resourceRoutes } from '@impulseframework/resource-angular/resource-route-generator';
import {
  ChildResourceConfig,
  ColumnConfig,
  field,
  FieldName,
  listViewConfig,
  Method,
  MethodPresentation,
  multiColumnConfig,
  RecordData,
  Resource,
  ResourceMeta,
  rootConfig,
  RootConfig,
  section,
} from '@impulseframework/resource-angular/types';

// The pages over an Auth's people, built from the API's own description.
//
// GenerateIdentity writes an Auth's management as ordinary resources and methods, and the
// generated description names them by what each is (`identity`: the people, the roles
// they hold, the method that disables one). These functions turn that description into
// the configuration the library's list and row pages are drawn from, so an application
// writes no page for it: the people with what the caller may do to each, a person's row
// page with the roles, the ways they sign in and the identities under it, the groups of
// people with a group's people and roles under it, the invitations, and, for the whole
// application, the invitations that make a tenant (staff bring an organization in by
// inviting one person), the sessions, the failed sign-ins and the Auth's sign-in methods,
// each invite only or open to sign-up. What differs from one application
// to the next is read from the description: the Auth's name and label, the tenant
// record's name, which side the pages are for, and what the Auth's sign-in methods give
// (a list or a method the description does not name draws nothing).
//
// The pages hold no logic of their own. A row's menu holds the methods the server says
// the caller may run on that row; the words, the icons and the questions are the
// library's, and the one thing the server cannot know is said here: Enable belongs on a
// disabled account and Disable on an enabled one.

/** The pages of one side of an Auth's management, by what each lists. */
export type IdentityPage =
  'users' | 'groups' | 'invitations' | 'invitedTenants' | 'sessions' | 'signInAttempts' | 'signIn';

/** Which pages to build, and where they go. */
export interface IdentityPagesOptions {
  /** The Auth whose people the pages manage, by the name the description carries (Crew). */
  auth: string;
  /**
   * The side: `global` for the whole application's pages (every person of the Auth),
   * `domain` for a tenant's (the people who hold a role in the open tenant).
   */
  scope: ScopeKind;
  /**
   * The route of a page, where the default is not the one wanted. A tenant's page
   * lives under the API's tenant segment, with the tenant parameter as the router
   * names it (`sectors/:sectorID/people`). By default a page's route is the Auth's
   * name and what the page lists (`crew-accounts`, `sectors/:sectorID/crew-people`).
   */
  routes?: Partial<Record<IdentityPage, string>>;
}

/** The membership kinds the permission store writes: a role held in one tenant, and one held in every tenant. */
const heldInOneTenant = 'domain';
const heldInEveryTenant = 'every';

// The configurations made for each description and request, so the routes and the menu
// file's pages of one request are the same pages (resourcePage answers one per config).
const made = new WeakMap<ApiDescriptor, Map<string, RootConfig[]>>();

/**
 * The page configurations of one side of an Auth's management, in menu order: the
 * people, the groups, the invitations, and for the whole application the invitations
 * that make a tenant, the sessions, the failed sign-ins and the sign-in methods, each
 * with its label and icon. A page whose list the description
 * does not name is left out. The same request answers the same configurations. Refused, naming
 * what is missing: an Auth the description does not carry, a side it does not carry,
 * and a tenant's side on an API with no tenant segment.
 */
export function identityPages(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta | undefined,
  options: IdentityPagesOptions,
): RootConfig[] {
  const requests = made.get(descriptor) ?? new Map<string, RootConfig[]>();
  made.set(descriptor, requests);
  const key = JSON.stringify([options.auth, options.scope, options.routes ?? {}]);
  const known = requests.get(key);
  if (known) {
    return known;
  }
  const configs = buildPages(descriptor, resourceMeta, options);
  requests.set(key, configs);
  return configs;
}

function buildPages(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta | undefined,
  options: IdentityPagesOptions,
): RootConfig[] {
  const identity = (descriptor.identity ?? []).find((candidate) => candidate.auth === options.auth);
  if (!identity) {
    const known = (descriptor.identity ?? []).map((candidate) => candidate.auth).join(', ') || 'none';
    throw new Error(
      `identityPages: the API description carries no generated management of the auth ${options.auth} (it carries: ${known}); the auth is named as GenerateIdentity names it, and its management is served on the outlets that declare or manage it`,
    );
  }
  const side = identity.sides.find((candidate) => candidate.scope === options.scope);
  if (!side) {
    throw new Error(
      `identityPages: the generated management of ${options.auth} has no ${options.scope === 'domain' ? "tenant's" : "whole application's"} side on this API`,
    );
  }
  const build = new SideBuilder(descriptor, identity, side, resourceMeta, options);
  return [
    build.people(),
    build.groups(),
    build.invitations(),
    build.invitedTenants(),
    build.sessions(),
    build.signInAttempts(),
    build.signIn(),
  ].filter((config): config is RootConfig => config !== undefined);
}

/**
 * The routes of one side of an Auth's management: `identityPages` through
 * `resourceRoutes`, which guards each page by the List grant on its resource and carries
 * its page. A tenant's routes go where the application puts its tenant pages; their
 * paths carry the tenant segment already.
 */
export function identityRoutes(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta,
  options: IdentityPagesOptions,
): Route[] {
  return identityPages(descriptor, resourceMeta, options).map((config) => resourceRoutes(config, resourceMeta));
}

/**
 * The pages of one side of an Auth's management, for the application's menu file to
 * place: the same pages the routes of the same request carry (identityRoutes), in menu
 * order, each needing the List grant on its resource.
 */
export function identityMenuPages(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta,
  options: IdentityPagesOptions,
): Page[] {
  return identityPages(descriptor, resourceMeta, options).map((config) => resourcePage(config, resourceMeta));
}

/** A column or a row page field: the row's field and the words above it. */
type Named = [field: string, words: string];

class SideBuilder {
  private readonly tenant: boolean;
  /** The tenant record in words, lowercase ("sector", "cost center"); "tenant" where the description names none. */
  private readonly tenantWord: string;
  private readonly base: string;

  constructor(
    private readonly descriptor: ApiDescriptor,
    private readonly identity: IdentityDescriptor,
    private readonly side: IdentitySideDescriptor,
    private readonly resourceMeta: (resource: Resource) => ResourceMeta | undefined,
    private readonly options: IdentityPagesOptions,
  ) {
    this.tenant = side.scope === 'domain';
    this.tenantWord = identity.tenant ? words(identity.tenant).toLowerCase() : 'tenant';
    this.base = this.tenant ? `${this.tenantSegment()}/${kebab(identity.auth)}` : kebab(identity.auth);
  }

  /**
   * The people, each with what the caller may do to them, and a person's row page with
   * the roles and the identities under it: the roles the person holds themselves, and
   * beside them the ones they hold through a group, where the Auth keeps groups.
   */
  people(): RootConfig {
    const { lists, methods } = this.side;
    const password = methods.setTemporaryPassword !== undefined || methods.requirePasswordChange !== undefined;
    const fields: Named[] = [
      ['username', 'Username'],
      ['name', 'Name'],
      ['email', 'Email'],
      ['disabled', 'Disabled'],
      ...(password ? ([['passwordChangeRequired', 'Must change password']] as Named[]) : []),
      ['lastSignInAt', 'Last sign-in'],
      ['heldElsewhere', `Also in another ${this.tenantWord}`],
      // Only a tenant's list of its people carries it, and only where the tenant record
      // has a directory-only switch: the server's sentence for a person the switch keeps
      // out, empty for everyone else.
      ['cannotEnter', 'Cannot enter'],
    ];
    const more: Named[] = [
      ['emailVerifiedAt', 'Email verified'],
      ['createdAt', 'Created'],
    ];
    const users = lists.users as Resource;
    const related = [
      this.rolesOf(),
      this.rolesThroughGroupsOf(),
      this.signInWaysOf(),
      this.identitiesOf(),
      this.sessionsOf(),
    ].filter((config): config is ChildResourceConfig => config !== undefined);
    return rootConfig({
      page: this.tenant ? { label: 'People', icon: 'group' } : { label: 'Accounts', icon: 'manage_accounts' },
      routeData: { route: this.route('users', this.tenant ? 'people' : 'accounts'), hasViewRoute: true },
      parentConfig: listViewConfig({
        title: this.tenant ? 'People' : `${this.identity.label} accounts`,
        primaryResource: users,
        showRowCount: true,
        listColumns: this.columns(users, fields),
        rowMethods: this.onPerson(),
        listMethods: this.present([
          [
            methods.create,
            {
              label: this.tenant ? 'Add a person' : 'Add an account',
              icon: 'person_add',
              labels: this.where(),
              ...this.rolesWhere('roles', methods.create),
            },
          ],
          [
            methods.invite,
            { label: 'Invite', icon: 'send', labels: this.where(), ...this.rolesWhere('roles', methods.invite) },
          ],
        ]),
        elements: [
          section({
            label: this.tenant ? 'Person' : 'Account',
            children: this.known(users, [...fields, ...more]).map(([name, label]) =>
              field({ name: name as FieldName, label, cols: 4 }),
            ),
          }),
        ],
      }),
      relatedConfigs: related,
    });
  }

  /**
   * The groups of people, each with what the caller may do to it, and a group's row page
   * with the people in it and the roles it holds under it. Left out where the Auth
   * keeps no groups.
   */
  groups(): RootConfig | undefined {
    const { lists, methods } = this.side;
    if (!lists.groups) {
      return undefined;
    }
    const groups = lists.groups as Resource;
    const fields: Named[] = [
      ['name', 'Name'],
      ['description', 'Description'],
    ];
    const named = (row: RecordData): string => String(row['name'] ?? 'this group');
    const related = [this.peopleOfGroup(), this.rolesOfGroup()].filter(
      (config): config is ChildResourceConfig => config !== undefined,
    );
    return rootConfig({
      page: { label: 'Groups', icon: 'groups' },
      routeData: { route: this.route('groups', 'groups'), hasViewRoute: true },
      parentConfig: listViewConfig({
        title: this.tenant ? 'Groups' : `${this.identity.label} groups`,
        primaryResource: groups,
        showRowCount: true,
        listColumns: [
          ...this.columns(groups, fields),
          // Where the whole application's group is kept: the tenant by its name, where
          // the tenant record can be read, and in words for a group of the whole
          // application. A tenant's own list holds that tenant's groups alone.
          ...(this.has(groups, 'kind')
            ? [
                this.tenantNamed('domainId' as FieldName, 'Kept in', (row) => this.heldIn(row)),
                { id: 'kind' as FieldName, hidden: true },
              ]
            : []),
        ],
        rowMethods: this.present([
          [methods.describeGroup, { label: 'Change the description', icon: 'edit' }],
          [
            methods.addGroupMember,
            { label: 'Add a person', icon: 'person_add', labels: periodWords, throughDay: periodEnds },
          ],
          [
            methods.giveGroupRole,
            {
              label: 'Give a role',
              icon: 'badge',
              labels: { ...this.where(), ...periodWords },
              throughDay: periodEnds,
              ...this.rolesWhere('role', methods.giveGroupRole),
            },
          ],
          [
            methods.deleteGroup,
            {
              label: 'Delete',
              icon: 'delete',
              confirm: (row) =>
                `Delete ${named(row)}? A group that still has people in it or holds a role is not deleted: empty it first.`,
            },
          ],
        ]),
        listMethods: this.present([
          [methods.createGroup, { label: 'Add a group', icon: 'group_add', labels: this.where() }],
        ]),
        elements: [
          section({
            label: 'Group',
            children: this.known(groups, fields).map(([name, label]) =>
              field({ name: name as FieldName, label, cols: 6 }),
            ),
          }),
        ],
      }),
      relatedConfigs: related,
    });
  }

  /** A tenant's invitations, or every link the whole application has mailed, each with Withdraw where it still stands. */
  invitations(): RootConfig | undefined {
    const { lists, methods } = this.side;
    if (!lists.invitations) {
      return undefined;
    }
    const resource = lists.invitations as Resource;
    const fields: Named[] = this.tenant
      ? [
          ['email', 'Email'],
          ['name', 'Name'],
          ['roleNames', 'Roles'],
          ['createdAt', 'Sent'],
          ['expiresAt', 'Expires'],
          ['pending', 'Still open'],
        ]
      : [
          ['kind', 'Kind'],
          ['email', 'Email'],
          ['name', 'Name'],
          ['tenantId', words(this.identity.tenant ?? 'Tenant')],
          ['roleNames', 'Roles'],
          ['createdAt', 'Sent'],
          ['expiresAt', 'Expires'],
          ['consumedAt', 'Used'],
        ];
    return rootConfig({
      page: this.tenant ? { label: 'Invitations', icon: 'mail' } : { label: 'Mailed links', icon: 'forward_to_inbox' },
      routeData: {
        route: this.route('invitations', this.tenant ? 'invitations' : 'mailed-links'),
        hasViewRoute: false,
      },
      parentConfig: listViewConfig({
        title: this.tenant ? 'Invitations' : `${this.identity.label} mailed links`,
        primaryResource: resource,
        showViewButton: false,
        showRowCount: true,
        // The tenant a link invites into is named, where the tenant record can be read.
        listColumns: this.columns(resource, fields).map((column) =>
          column.id === 'tenantId'
            ? this.tenantNamed(column.id, column.header ?? '', (row) => String(row['tenantId'] ?? ''))
            : column,
        ),
        rowMethods: this.present([
          [
            methods.withdrawInvitation,
            {
              label: 'Withdraw',
              icon: 'cancel_schedule_send',
              // An invitation that was used or withdrawn has nothing left to withdraw.
              when: (row) => (this.tenant ? row['pending'] === true : !row['consumedAt']),
              confirm: (row) => `Withdraw the invitation sent to ${String(row['email'])}?`,
            },
          ],
        ]),
        listMethods: this.tenant
          ? this.present([
              [methods.invite, { label: 'Invite', icon: 'send', ...this.rolesWhere('roles', methods.invite) }],
            ])
          : [],
        elements: [],
      }),
    });
  }

  /**
   * The invitations that make a tenant and still stand, each with Withdraw, and above the
   * list the method that sends one: staff bring an organization in by inviting one
   * person, and that person's first sign-in makes the tenant, so nobody creates a tenant
   * directly. The whole application's alone, for the Auth whose people make the tenants.
   *
   * The list and the method's form are the server's: a row carries the tenant record's
   * fields as staff entered them, each under the record's name (`sectorName`), and the
   * method takes the same fields, so the columns and the form's inputs follow the record
   * with nothing written here for it. A field the caller may not read on the tenant
   * record is absent from a row, and its cell is empty. The caller sees an action only
   * while their roles grant its method, as on every list. To change an invitation's
   * details, it is withdrawn and another sent, which the question before a withdrawal
   * says.
   */
  invitedTenants(): RootConfig | undefined {
    const { lists, methods } = this.side;
    if (!lists.invitedTenants) {
      return undefined;
    }
    const resource = lists.invitedTenants as Resource;
    const record = this.identity.tenant ?? 'Tenant';
    // The fields of the tenant record, as the list and the method name them: the
    // record's name, then the field's.
    const prefix = record.charAt(0).toLowerCase() + record.slice(1);
    const directoryOnly = `${prefix}DirectoryOnly`;
    const ofRecord = (this.resourceMeta(resource)?.fields ?? [])
      .map((candidate) => String(candidate.fieldName))
      .filter(
        (name) => name !== directoryOnly && name.startsWith(prefix) && /^[A-Z0-9]/.test(name.slice(prefix.length)),
      );
    const fields: Named[] = [
      ...ofRecord.map((name): Named => [name, words(name)]),
      [directoryOnly, 'Directory only'],
      ['defaultRole', 'Starting role'],
      ['email', 'Invited'],
      ['name', 'Name'],
      ['createdAt', 'Sent'],
      ['expiresAt', 'Expires'],
    ];
    // What the tenants are called, many of them: the list's own name says it
    // (CrewInvitedSectors), since the generator names the list for the record.
    const listed = lists.invitedTenants.startsWith(`${this.identity.auth}Invited`)
      ? lists.invitedTenants.slice(`${this.identity.auth}Invited`.length)
      : `${record}s`;
    const title = `Invited ${words(listed).toLowerCase()}`;
    // The tenant a row would make, by the name staff gave it where the caller may read it.
    const nameField = this.identity.tenantResource
      ? this.resourceMeta(this.identity.tenantResource as Resource)?.nameField
      : undefined;
    const nameKey = nameField ? `${prefix}${nameField.charAt(0).toUpperCase()}${nameField.slice(1)}` : undefined;
    const made = (row: RecordData): string => {
      const name = nameKey ? row[nameKey] : undefined;
      return typeof name === 'string' && name !== '' ? name : `the ${this.tenantWord}`;
    };
    return rootConfig({
      page: { label: title, icon: 'domain_add' },
      routeData: { route: this.route('invitedTenants', `invited-${kebab(listed)}`), hasViewRoute: false },
      parentConfig: listViewConfig({
        title,
        primaryResource: resource,
        showViewButton: false,
        showRowCount: true,
        listColumns: [
          ...this.columns(resource, fields),
          // Read with each row and not drawn: what Withdraw is filled from.
          ...this.columns(resource, [['id', 'Id']]).map((column) => ({ ...column, hidden: true })),
        ],
        rowMethods: this.present([
          [
            methods.withdrawInvitedTenant,
            {
              label: 'Withdraw',
              icon: 'cancel_schedule_send',
              fill: (row) => ({ invitationId: row['id'] }),
              confirm: (row) =>
                `Withdraw the invitation sent to ${String(row['email'])}? Its link stops working, and ${made(row)} is not set up. To change an invitation's details, withdraw it and send another.`,
              successMessage: 'The invitation is withdrawn.',
            },
          ],
        ]),
        listMethods: this.present([
          [
            methods.inviteTenant,
            {
              label: `Invite ${withArticle(this.tenantWord)}`,
              icon: 'send',
              labels: {
                email: "Person's email",
                name: "Person's name",
                [directoryOnly]: 'Entered through its directory only',
                defaultRole: "Role its directory's people start with",
              },
            },
          ],
        ]),
        elements: [],
      }),
    });
  }

  /** Every session of the Auth's people, each with End the session: the whole application's alone. */
  sessions(): RootConfig | undefined {
    const { lists } = this.side;
    if (!lists.sessions) {
      return undefined;
    }
    const resource = lists.sessions as Resource;
    return rootConfig({
      page: { label: 'Sessions', icon: 'devices' },
      routeData: { route: this.route('sessions', 'sessions'), hasViewRoute: false },
      parentConfig: listViewConfig({
        title: `${this.identity.label} sessions`,
        primaryResource: resource,
        showViewButton: false,
        showRowCount: true,
        listColumns: this.columns(resource, [['username', 'Username'], ...sessionColumns]),
        rowMethods: this.onSession(),
        elements: [],
      }),
    });
  }

  /** The names whose failed sign-ins are being counted or are locked out, each with Unlock: the whole application's alone. */
  signInAttempts(): RootConfig | undefined {
    const { lists, methods } = this.side;
    if (!lists.signInAttempts) {
      return undefined;
    }
    const resource = lists.signInAttempts as Resource;
    return rootConfig({
      page: { label: 'Failed sign-ins', icon: 'gpp_bad' },
      routeData: { route: this.route('signInAttempts', 'failed-sign-ins'), hasViewRoute: false },
      parentConfig: listViewConfig({
        title: `${this.identity.label} failed sign-ins`,
        primaryResource: resource,
        showViewButton: false,
        showRowCount: true,
        listColumns: this.columns(resource, [
          ['name', 'Name'],
          ['failures', 'Failed tries'],
          ['lockedUntil', 'Locked until'],
          ['updatedAt', 'Last try'],
        ]),
        rowMethods: this.present([
          [
            methods.unlockSignIn,
            {
              label: 'Unlock',
              icon: 'lock_open',
              confirm: (row) => `Unlock ${String(row['name'])}? The count of failed tries starts over.`,
            },
          ],
        ]),
        elements: [],
      }),
    });
  }

  /**
   * The Auth's sign-in methods, one row each: who can sign in with it, which is how wide
   * the method is and is fixed in the application's code, and whether a person with no
   * invitation is admitted, which an administrator chooses here. The whole application's
   * alone. A method that is invite only offers Open sign-up, and one that is open
   * offers Make invite only; a method whose directory gives the roles offers neither,
   * nor does one whose registration carries a rule of its own, which the application's
   * code decides, and the row says why. A method that was opened when its registration
   * admitted someone else is invite only until it is opened again: the server says for
   * whom it was opened, and the row offers Open sign-up. The caller sees an action only
   * while their roles grant the method, as on every list.
   *
   * Opening a method is a warning the server writes for that method (who will be
   * admitted, and what each of them gets), confirmed by typing a word: the method
   * declares both, and the library's dialog draws them. Without tenants the dialog also
   * asks for the role each new person gets, listed with its description, from the roles
   * the caller may give a person directly, which is what the server lists; with tenants a
   * person who signs up creates a tenant of their own and gets no role here. While a
   * method is open the page says so above the list, in the warning colors.
   */
  signIn(): RootConfig | undefined {
    const { lists, methods } = this.side;
    if (!lists.signInMethods) {
      return undefined;
    }
    const resource = lists.signInMethods as Resource;
    const fromRow = (row: RecordData): Record<string, unknown> => ({ name: row['name'] });
    return rootConfig({
      page: { label: 'Sign-in', icon: 'login' },
      routeData: { route: this.route('signIn', 'sign-in'), hasViewRoute: false },
      parentConfig: listViewConfig({
        title: `${this.identity.label} sign-in`,
        primaryResource: resource,
        showViewButton: false,
        showRowCount: false,
        notice: openToSignUp,
        listColumns: [
          ...this.columns(resource, [
            ['label', 'Sign-in method'],
            ['admits', 'Who can sign in with it'],
          ]),
          ...this.columns(resource, [['openSignUp', 'Sign-up']]).map((column) => ({
            ...column,
            valueGetter: signUpChoice,
            filterable: false,
          })),
          // The role a new person gets is chosen where the application has no tenants.
          ...(lists.signUpRoles ? this.columns(resource, [['defaultRole', 'Role a new person gets']]) : []),
          ...this.columns(resource, [['says', 'A person with no invitation']]),
          // Whether a person may ask for a reset link themselves, where the Auth has a
          // password to reset: the password method's row says, and the other rows say nothing.
          ...(methods.setSelfServiceReset
            ? this.columns(resource, [['selfServiceReset', 'People can reset their own password']]).map((column) => ({
                ...column,
                valueGetter: ownReset,
                filterable: false,
              }))
            : []),
          // Read with each row and not drawn: what the actions are offered by and filled from.
          ...this.columns(resource, [
            ['name', 'Name'],
            ['directoryGivesRoles', 'Directory gives roles'],
            ['ownRule', 'Decided by its own rule'],
          ]).map((column) => ({ ...column, hidden: true })),
        ],
        rowMethods: this.present([
          [
            methods.openSignUp,
            {
              label: 'Open sign-up',
              icon: 'public',
              // A method that is open already has nothing to open, and a directory that
              // gives the roles, or the registration's own rule, decides who signs in.
              when: (row) => row['openSignUp'] !== true && !decidedElsewhere(row),
              fill: fromRow,
              labels: { defaultRole: 'Role each new person gets' },
              shows: { defaultRole: ['name', 'description'] },
              successMessage: 'The method is open to sign-up.',
            },
          ],
          [
            methods.makeInviteOnly,
            {
              label: 'Make invite only',
              icon: 'lock',
              when: (row) => row['openSignUp'] === true,
              fill: fromRow,
              confirm: 'People who already have accounts keep them. New people will need an invitation.',
              successMessage: 'The method is invite only.',
            },
          ],
          // Whether people may reset their own password is the password method's alone, and
          // one method says it either way: the page offers the change that is left to make.
          [
            methods.setSelfServiceReset,
            {
              label: 'Let people reset their own password',
              icon: 'lock_reset',
              when: (row) => row['name'] === 'password' && row['selfServiceReset'] !== true,
              fill: () => ({ on: true }),
              confirm:
                'The sign-in page will offer "Forgot your password?". A person who asks is mailed a link to the email address on their account.',
              successMessage: 'People can now ask for a reset link at the sign-in page.',
            },
          ],
          [
            methods.setSelfServiceReset,
            {
              label: 'Stop people resetting their own password',
              icon: 'lock',
              when: (row) => row['name'] === 'password' && row['selfServiceReset'] === true,
              fill: () => ({ on: false }),
              confirm:
                'The sign-in page will no longer offer "Forgot your password?". A link already mailed still works, and an administrator can still send one.',
              successMessage: 'People can no longer ask for a reset link at the sign-in page.',
            },
          ],
        ]),
        elements: [],
      }),
    });
  }

  /** The roles a person holds, under the person's row. */
  private rolesOf(): ChildResourceConfig | undefined {
    const { lists, methods } = this.side;
    if (!lists.roles) {
      return undefined;
    }
    const resource = lists.roles as Resource;
    if (this.tenant) {
      return listViewConfig({
        title: 'Roles held here',
        primaryResource: resource,
        parentRelation: { parentKey: 'id' as FieldName, childKey: 'userId' as FieldName },
        showViewButton: false,
        collapsible: false,
        listColumns: [
          ...this.columns(resource, [
            ['role', 'Role'],
            ['everyDomain', `In every ${this.tenantWord}`],
            ['createdAt', 'Given'],
            ...periodColumns,
          ]),
          // Read with each row and not drawn: the list sits under the person, and the
          // question before a role is taken away names them.
          ...this.columns(resource, [['username', 'Username']]).map((column) => ({ ...column, hidden: true })),
        ],
        rowMethods: this.present([
          [
            methods.removeRole,
            {
              label: 'Take the role away',
              icon: 'person_remove',
              confirm: (row) => `Take ${String(row['role'])} away from ${String(row['username'] ?? 'this person')}?`,
            },
          ],
        ]),
        elements: [],
      });
    }
    // The whole application's roles are held by the person's account, in the whole
    // application, in one tenant, or in every tenant: a role row names its person by the
    // id of their account, and the methods that give and take one take that id, which
    // the page fills from the row.
    return listViewConfig({
      title: 'Roles held',
      primaryResource: resource,
      parentRelation: { parentKey: 'id' as FieldName, childKey: 'userId' as FieldName },
      showViewButton: false,
      collapsible: false,
      listColumns: [
        ...this.columns(resource, [['role', 'Role']]),
        // Where the role is held: the tenant by its name, where the tenant record can
        // be read, and in words for a role held in every tenant or the whole application.
        ...(this.has(resource, 'kind')
          ? [
              this.tenantNamed('domainId' as FieldName, 'Held in', (row) => this.heldIn(row)),
              // Read with each row and not drawn: what the words and the methods are filled from.
              { id: 'kind' as FieldName, hidden: true },
            ]
          : []),
        ...this.columns(resource, [['createdAt', 'Given'], ...periodColumns]),
      ],
      rowMethods: this.present([
        [
          methods.removeRole,
          {
            label: 'Take the role away',
            icon: 'person_remove',
            fill: (row) => ({
              userId: row['userId'],
              role: row['role'],
              // A role held for the whole application or in every tenant names no tenant:
              // the page fills that with nothing (null), so the dialog asks for none and
              // puts its question. Left undefined, the dialog would ask the person for it.
              domain: row['kind'] === heldInOneTenant ? row['domainId'] : null,
              everyDomain: row['kind'] === heldInEveryTenant,
            }),
            // The list sits under the person, and a role row carries their id, which
            // nobody reads: the question names the role and where it is held.
            confirm: (row) => `Take ${String(row['role'])}${this.heldWhere(row)} away from this person?`,
          },
        ],
      ]),
      elements: [],
    });
  }

  /**
   * The roles a person holds through a group, under the person's row beside the roles
   * they hold themselves: each names its group, so the page says which roles are the
   * person's own and which come through which group. Nothing is changed from here: a
   * group's role is taken away on the group's page, and a person leaves a group there.
   */
  private rolesThroughGroupsOf(): ChildResourceConfig | undefined {
    const { lists } = this.side;
    if (!lists.rolesThroughGroups) {
      return undefined;
    }
    const resource = lists.rolesThroughGroups as Resource;
    const named: Named[] = [
      ['role', 'Role'],
      ['groupName', 'Group'],
    ];
    return listViewConfig({
      title: this.tenant ? 'Roles held here through a group' : 'Roles held through a group',
      primaryResource: resource,
      parentRelation: { parentKey: 'id' as FieldName, childKey: 'userId' as FieldName },
      showViewButton: false,
      collapsible: false,
      listColumns: this.tenant
        ? this.columns(resource, [
            ...named,
            ['globalGroup', 'Group of the whole application'],
            ['everyDomain', `In every ${this.tenantWord}`],
            ...periodColumns,
          ])
        : [
            ...this.columns(resource, named),
            ...(this.has(resource, 'kind')
              ? [
                  this.tenantNamed('domainId' as FieldName, 'Held in', (row) => this.heldIn(row)),
                  { id: 'kind' as FieldName, hidden: true },
                ]
              : []),
            ...this.columns(resource, periodColumns),
          ],
      rowMethods: false,
      elements: [],
    });
  }

  /** The people in a group, under the group's row. */
  private peopleOfGroup(): ChildResourceConfig | undefined {
    const { lists, methods } = this.side;
    if (!lists.groupUsers) {
      return undefined;
    }
    const resource = lists.groupUsers as Resource;
    return listViewConfig({
      title: 'People in the group',
      primaryResource: resource,
      parentRelation: { parentKey: 'id' as FieldName, childKey: 'groupId' as FieldName },
      showViewButton: false,
      collapsible: false,
      listColumns: this.columns(resource, [
        ['username', 'Username'],
        ['name', 'Name'],
        ['createdAt', 'Added'],
        ...periodColumns,
      ]),
      rowMethods: this.present([
        [
          methods.removeGroupMember,
          {
            label: 'Remove from the group',
            icon: 'person_remove',
            confirm: (row) =>
              `Remove ${String(row['username'] ?? 'this person')} from the group? They keep the roles they hold themselves and lose the group's.`,
          },
        ],
      ]),
      // The method runs against the group the list sits under.
      listMethods: this.present([
        [
          methods.addGroupMember,
          { label: 'Add a person', icon: 'person_add', labels: periodWords, throughDay: periodEnds },
        ],
      ]),
      elements: [],
    });
  }

  /** The roles a group holds, under the group's row. */
  private rolesOfGroup(): ChildResourceConfig | undefined {
    const { lists, methods } = this.side;
    if (!lists.groupRoles) {
      return undefined;
    }
    const resource = lists.groupRoles as Resource;
    const lost = 'Everyone in the group loses it, unless they hold it some other way.';
    // The method runs against the group the list sits under, and picks the role as the
    // same method does from the group's row: among the roles of the place the dialog holds.
    const give = this.present([
      [
        methods.giveGroupRole,
        {
          label: 'Give a role',
          icon: 'badge',
          labels: { ...this.where(), ...periodWords },
          throughDay: periodEnds,
          ...this.rolesWhere('role', methods.giveGroupRole),
        },
      ],
    ]);
    if (this.tenant) {
      return listViewConfig({
        title: 'Roles the group holds',
        primaryResource: resource,
        parentRelation: { parentKey: 'id' as FieldName, childKey: 'groupId' as FieldName },
        showViewButton: false,
        collapsible: false,
        listColumns: this.columns(resource, [['role', 'Role'], ['createdAt', 'Given'], ...periodColumns]),
        rowMethods: this.present([
          [
            methods.takeGroupRole,
            {
              label: 'Take the role away',
              icon: 'remove_moderator',
              confirm: (row) => `Take ${String(row['role'])} away from the group? ${lost}`,
            },
          ],
        ]),
        listMethods: give,
        elements: [],
      });
    }
    // The whole application's group holds a role in the whole application, in one tenant
    // or in every tenant: the method that takes one away takes the group's id and where
    // the role is held, which the page fills from the row.
    return listViewConfig({
      title: 'Roles the group holds',
      primaryResource: resource,
      parentRelation: { parentKey: 'id' as FieldName, childKey: 'groupId' as FieldName },
      showViewButton: false,
      collapsible: false,
      listColumns: [
        ...this.columns(resource, [['role', 'Role']]),
        ...(this.has(resource, 'kind')
          ? [
              this.tenantNamed('domainId' as FieldName, 'Held in', (row) => this.heldIn(row)),
              { id: 'kind' as FieldName, hidden: true },
            ]
          : []),
        ...this.columns(resource, [['createdAt', 'Given'], ...periodColumns]),
      ],
      rowMethods: this.present([
        [
          methods.takeGroupRole,
          {
            label: 'Take the role away',
            icon: 'remove_moderator',
            fill: (row) => ({
              groupId: row['groupId'],
              role: row['role'],
              domain: row['kind'] === heldInOneTenant ? row['domainId'] : null,
              everyDomain: row['kind'] === heldInEveryTenant,
            }),
            confirm: (row) => `Take ${String(row['role'])}${this.heldWhere(row)} away from the group? ${lost}`,
          },
        ],
      ]),
      listMethods: give,
      elements: [],
    });
  }

  /**
   * How a person signs in, under the person's row: the password, where they hold one, and
   * each identity linked to them that can sign them in, as the server judges it. An
   * identity left from a sign-in the Auth no longer has is in the list of identities
   * below and not here. The removal of the password is offered above this list and runs
   * against the person the list sits under. It is offered where the list shows a
   * password and an identity that counts, which is where the server lets it through, so
   * a person with no password, or with no other way in, is offered none. The list is
   * read again once the removal ran, and the offer goes with the password. The question
   * names what is left by the words of the rows, the ones the login page's buttons
   * carry. Where the server serves no such list, the removal is not offered.
   */
  private signInWaysOf(): ChildResourceConfig | undefined {
    const { lists, methods } = this.side;
    if (!lists.signInWays) {
      return undefined;
    }
    const resource = lists.signInWays as Resource;
    return listViewConfig({
      title: 'Ways to sign in',
      primaryResource: resource,
      parentRelation: { parentKey: 'id' as FieldName, childKey: 'userId' as FieldName },
      showViewButton: false,
      collapsible: false,
      listColumns: this.columns(resource, [
        ['through', 'Signs in with'],
        ['connection', 'Connection'],
      ]),
      // No method runs against a way: the password's removal runs against the person.
      rowMethods: false,
      listMethods: this.present([
        [
          methods.removePassword,
          {
            label: 'Remove password',
            icon: 'key_off',
            when: (_person, ways) => holdsPassword(ways ?? []) && otherWays(ways ?? []).length > 0,
            confirm: (person, ways) =>
              `Remove the password of ${String(person['username'] ?? 'this person')}? ${leftToSignInThrough(ways ?? [])}`,
          },
        ],
      ]),
      elements: [],
    });
  }

  /**
   * The identities linked to a person, under the person's row, each with the method
   * that unlinks it. Which of them can sign the person in is the list above
   * (`signInWaysOf`).
   */
  private identitiesOf(): ChildResourceConfig | undefined {
    const { lists, methods } = this.side;
    if (!lists.identities) {
      return undefined;
    }
    const resource = lists.identities as Resource;
    return listViewConfig({
      title: 'Sign-in identities',
      primaryResource: resource,
      parentRelation: { parentKey: 'id' as FieldName, childKey: 'userId' as FieldName },
      showViewButton: false,
      collapsible: false,
      listColumns: this.columns(resource, [
        ['method', 'Sign-in method'],
        ['connection', 'Connection'],
        ['emailAtLink', 'Email when linked'],
        ['createdAt', 'Linked'],
        ['lastUsedAt', 'Last used'],
      ]),
      rowMethods: this.present([
        [
          methods.unlinkIdentity,
          {
            label: 'Unlink',
            icon: 'link_off',
            confirm: 'Unlink this sign-in identity? The person can no longer sign in with it.',
          },
        ],
      ]),
      elements: [],
    });
  }

  /** A person's sessions, under the person's row: the whole application's alone. */
  private sessionsOf(): ChildResourceConfig | undefined {
    const { lists } = this.side;
    if (!lists.sessions) {
      return undefined;
    }
    const resource = lists.sessions as Resource;
    return listViewConfig({
      title: 'Sessions',
      primaryResource: resource,
      parentRelation: { parentKey: 'id' as FieldName, childKey: 'userId' as FieldName },
      showViewButton: false,
      collapsible: false,
      listColumns: this.columns(resource, sessionColumns),
      rowMethods: this.onSession(),
      elements: [],
    });
  }

  /** How the methods that run against a person are presented. */
  private onPerson(): MethodPresentation[] {
    const { methods } = this.side;
    const named = (row: RecordData): string => String(row['username'] ?? 'this person');
    return this.present([
      [
        methods.assignRole,
        this.tenant
          ? {
              label: 'Give a role',
              icon: 'badge',
              labels: periodWords,
              throughDay: periodEnds,
              ...this.rolesWhere('role', methods.assignRole),
            }
          : // The whole application's method takes the id of the person's account, which the row fills.
            {
              label: 'Give a role',
              icon: 'badge',
              fill: (row) => ({ userId: row['id'] }),
              labels: { ...this.where(), ...periodWords },
              throughDay: periodEnds,
              ...this.rolesWhere('role', methods.assignRole),
            },
      ],
      // A person has two names: the one people read, and the username they sign in under.
      [methods.setName, { label: 'Change the name', icon: 'person' }],
      [methods.rename, { label: 'Change the username', icon: 'edit' }],
      // An address changes when the person confirms it from the new mailbox, and a reset
      // link changes nothing until it is used: each answers whether its message went.
      [
        methods.setEmail,
        {
          label: 'Change the email address',
          icon: 'mail',
          answered: mailed(
            'A link to confirm the new address is on its way to it. The account keeps its address until the link is used.',
          ),
        },
      ],
      [methods.setTemporaryPassword, { label: 'Set a temporary password', icon: 'password' }],
      [
        methods.sendPasswordResetLink,
        {
          label: 'Send a password reset link',
          icon: 'lock_reset',
          confirm:
            'A link to choose a new password is mailed to the email address on the account. Nothing changes until the link is used.',
          answered: mailed('The reset link is on its way.'),
        },
      ],
      [
        methods.requirePasswordChange,
        {
          label: 'Require a new password',
          icon: 'lock_reset',
          confirm: (row) =>
            `Make ${named(row)} choose a new password? They are asked at once, in any session they have open, and can do nothing else until they have.`,
        },
      ],
      // Offered above the ways the person signs in, on the person's page.
      [methods.removePassword, { hidden: true }],
      [
        methods.endUserSessions,
        {
          label: 'Sign out everywhere',
          icon: 'logout',
          confirm: (row) => `Sign ${named(row)} out of every browser and device?`,
        },
      ],
      [
        methods.revokeTrustedDevices,
        {
          label: 'Forget trusted devices',
          icon: 'phonelink_erase',
          confirm: (row) => `Forget the devices ${named(row)} trusted? They are asked for a second factor again.`,
        },
      ],
      [
        methods.disable,
        {
          label: 'Disable',
          icon: 'block',
          when: (row) => row['disabled'] === false,
          confirm: (row) => `Disable ${named(row)}? They cannot sign in until the account is enabled again.`,
        },
      ],
      [
        methods.enable,
        {
          label: 'Enable',
          icon: 'check_circle',
          when: (row) => row['disabled'] === true,
          confirm: (row) => `Enable ${named(row)}? They can sign in again.`,
        },
      ],
      [
        methods.delete,
        {
          label: 'Delete',
          icon: 'delete',
          confirm: (row) => `Delete ${named(row)}? The account, its sign-ins and its sessions are removed.`,
        },
      ],
    ]);
  }

  /** How the method that ends one session is presented. */
  private onSession(): MethodPresentation[] {
    return this.present([
      [
        this.side.methods.endSession,
        { label: 'End the session', icon: 'logout', confirm: 'End this session? The browser or device is signed out.' },
      ],
    ]);
  }

  /**
   * The words for where a role is held, on the whole application's methods that take a
   * tenant: the tenant record's name for the tenant, and for the switch that says every
   * tenant.
   */
  private where(): Record<string, string> {
    return { domain: words(this.identity.tenant ?? 'Tenant'), everyDomain: `In every ${this.tenantWord}` };
  }

  /**
   * How a dialog that gives a role offers the roles: the role field's picker lists the
   * roles the caller could give with the dialog's own method. The generated list of the
   * roles to give answers for one method at a time, the one its filter names
   * (`method:eq:InviteCrewUser`), so each dialog offers what its own request is allowed
   * and nothing that only another method's is. The whole application's dialog also asks
   * where the role is to be held first (the tenant, "In every tenant", or neither for
   * the whole application), and its picker lists the roles that exist there: the filter
   * names the place beside the method. A dialog of the invitation, which names no "every
   * tenant", lists the roles of the whole application alone until a tenant is chosen
   * (heldWhere). A tenant's own dialogs pick from that tenant's roles, and an
   * application without tenants holds every role for the whole application: neither
   * names a place. A list that carries no `method` field lists the same roles for every
   * method (a tenant's roles, where nothing answers what the caller could give), and is
   * asked for none.
   */
  private rolesWhere(field: 'roles' | 'role', method: string | undefined): Pick<MethodPresentation, 'narrows'> {
    const list = this.side.lists.rolesToGive as Resource | undefined;
    const asked = method !== undefined && list !== undefined && this.has(list, 'method') ? `method:eq:${method}` : '';
    const placed = !this.tenant && this.identity.tenant !== undefined;
    if (!asked && !placed) {
      return {};
    }
    return {
      narrows: {
        [field]: (values) => [asked, placed ? heldWhere(values) : ''].filter((part) => part !== '').join(','),
      },
    };
  }

  /** The presentations of the methods the description names; one it does not name presents nothing. */
  private present(
    entries: [method: string | undefined, presentation: Omit<MethodPresentation, 'method'>][],
  ): MethodPresentation[] {
    return entries
      .filter((entry): entry is [string, Omit<MethodPresentation, 'method'>] => entry[0] !== undefined)
      .map(([method, presentation]) => ({ method: method as Method, ...presentation }));
  }

  /**
   * Where a role of the whole application's list is held, or a group of it is kept, in
   * words, for a row that names no tenant, and for one whose tenant has no name to
   * show (the tenant record marks none): a role held in one tenant then falls back to the
   * tenant's key, which is all there is. A tenant that has a name the reader may not
   * read is said by tenantNamed, in words and never by its key.
   */
  private heldIn(row: RecordData): string {
    switch (row['kind']) {
      case heldInOneTenant:
        return `${words(this.identity.tenant ?? 'Tenant')} ${String(row['domainId'])}`;
      case heldInEveryTenant:
        return `Every ${this.tenantWord}`;
      default:
        return 'The whole application';
    }
  }

  /**
   * Where the role of a row is held, as the question before it is taken away says it.
   * The row the person clicked names the tenant; the question says no key.
   */
  private heldWhere(row: RecordData): string {
    switch (row['kind']) {
      case heldInOneTenant:
        return ` in this ${this.tenantWord}`;
      case heldInEveryTenant:
        return ` in every ${this.tenantWord}`;
      default:
        return '';
    }
  }

  /**
   * A column over a field that holds a tenant's key, showing the tenant's name: the
   * tenant record's row is read by the keys on the page (the list's related-record
   * column) and its name field shown; a reader who may not read the record is shown the
   * name their own list of tenants holds, for a tenant they hold a role in
   * (`tenantKey`). Under a heading that is not the tenant's word the name follows the
   * word ("Sector Anvil" under "Held in"), and under the word itself it stands alone. A
   * tenant's key is an id nobody reads, so a row whose tenant's name the reader may not
   * read says that in words (tenantUnseen) and never shows the key. Where the outlet's
   * description names no tenant record there is none to read, and the reader's own list
   * of tenants and those words are what a row shows. `otherwise` says what a row shows
   * that names no tenant; it is all a row shows where the record marks no name, since
   * the key is then all there is.
   */
  private tenantNamed(id: FieldName, header: string, otherwise: (row: RecordData) => string): ColumnConfig {
    const record = this.identity.tenantResource as Resource | undefined;
    const meta = record ? this.resourceMeta(record) : undefined;
    const key = meta?.fields.find((candidate) => candidate.primaryKey)?.fieldName;
    if (!this.identity.tenant || (record && (!meta?.nameField || !key))) {
      // An application without tenants has no key to name, and where the tenant record
      // marks no name the key is all there is to say.
      return { id, header, valueGetter: otherwise, filterable: false };
    }
    const word = words(this.identity.tenant ?? 'Tenant');
    const worded = header.trim().toLowerCase() !== word.toLowerCase();
    return multiColumnConfig({
      id,
      header,
      // Where the outlet serves no tenant record there is none to read: the person's own
      // list of tenants names the tenant, or the page's words stand.
      additionalIds:
        record && meta?.nameField && key
          ? [{ resource: record, id: key as FieldName, field: meta.nameField as FieldName }]
          : [],
      concatFn: 'hyphen-concat',
      tenantKey: true,
      ...(worded ? { said: (name: string) => `${word} ${name}` } : {}),
      fallback: (row: RecordData) => (String(row[id] ?? '') === '' ? otherwise(row) : this.tenantUnseen()),
      // The field holds the key: a filter typed over the column would be asked for an id.
      filterable: false,
    });
  }

  /**
   * What a cell says of a tenant that has a name the reader may not read, because they
   * hold no List on the tenant record and no role in the tenant: "A sector you cannot
   * see".
   */
  private tenantUnseen(): string {
    const one = withArticle(this.tenantWord);
    return `${one.charAt(0).toUpperCase()}${one.slice(1)} you cannot see`;
  }

  /** Whether the resource has the field, by its metadata. */
  private has(resource: Resource, name: string): boolean {
    return (this.resourceMeta(resource)?.fields ?? []).some((candidate) => candidate.fieldName === name);
  }

  /** The named fields the resource has, in the order given: a field this Auth's schema does not carry is left out. */
  private known(resource: Resource, named: Named[]): Named[] {
    return named.filter(([name]) => this.has(resource, name));
  }

  /**
   * A column for each named field the resource has. The end of a period is shown as its
   * last day (lastDay), the day a person picked for it.
   */
  private columns(resource: Resource, named: Named[]): ColumnConfig[] {
    return this.known(resource, named).map(([name, header]) =>
      periodEnds.includes(name)
        ? { id: name as FieldName, header, valueGetter: (row: RecordData) => lastDay(row[name]) }
        : { id: name as FieldName, header },
    );
  }

  /** The route of a page: the one the options give, or the Auth's name and what the page lists. */
  private route(page: IdentityPage, lists: string): string {
    return this.options.routes?.[page] ?? `${this.base}-${lists}`;
  }

  /** The API's tenant segment with the tenant parameter as the router names it (`sectors/:sectorID`). */
  private tenantSegment(): string {
    const route = this.descriptor.domainRoute;
    if (!route) {
      throw new Error(
        `identityPages: a tenant's pages of ${this.identity.auth} live under the API's tenant segment, and the API description declares none (domainRoute)`,
      );
    }
    return `${route.segment}/:${route.param}`;
  }
}

/**
 * The filter the list of the roles to give is asked with for where a role is to be held,
 * from the dialog's values: one tenant's roles where a tenant is chosen; where none is,
 * the roles held in every tenant or for the whole application as "In every tenant" says,
 * and the whole application's alone where the dialog does not ask it (an invitation). A
 * role the request names with no tenant is held in every tenant when it is a tenant's
 * kind, so a dialog that does not ask that place never offers one: the person would give
 * it in every tenant without being told.
 */
function heldWhere(values: RecordData): string {
  const domain = values['domain'];
  if (typeof domain === 'string' && domain !== '') {
    return `domain:eq:${quoteFilterValue(domain)}`;
  }
  return `everyDomain:eq:${values['everyDomain'] === true}`;
}

/**
 * Whether who signs in through a method is decided outside this page: by the directory
 * that gives the roles, or by the account rule the registration carries in the
 * application's code. Such a method is neither opened to sign-up nor made invite only.
 */
function decidedElsewhere(row: RecordData): boolean {
  return row['directoryGivesRoles'] === true || row['ownRule'] === true;
}

/** What a sign-in method's row says of sign-up: who decides, or what an administrator chose. */
function signUpChoice(row: RecordData): string {
  if (row['directoryGivesRoles'] === true) {
    return 'Set by the directory';
  }
  if (row['ownRule'] === true) {
    return 'Set by the application';
  }
  return row['openSignUp'] === true ? 'Open' : 'Invite only';
}

/** Whether people may ask for a link that resets their own password, in the Sign-in page's words: the password method's row says, and no other. */
function ownReset(row: RecordData): string {
  if (row['name'] !== 'password') {
    return '';
  }
  return row['selfServiceReset'] === true ? 'Yes' : 'No';
}

/** The words the server opens an open method's sentence with, which the notice says its own way. */
const openPrefix = 'Open: ';

/**
 * The Sign-in page's standing notice: one sentence for each method that is open to
 * sign-up, naming the method and saying, in the server's words for it, who is admitted
 * and what each new person gets. Nothing while every method is invite only.
 */
function openToSignUp(rows: RecordData[]): string | undefined {
  const open = rows.filter((row) => row['openSignUp'] === true);
  if (open.length === 0) {
    return undefined;
  }
  return open
    .map((row) => {
      const says = String(row['says'] ?? '');
      const consequence = says.startsWith(openPrefix) ? says.slice(openPrefix.length) : says;
      return `${String(row['label'] ?? row['name'])} is open to sign-up. ${consequence}`.trim();
    })
    .join('\n');
}

/**
 * The period of a role, of a place in a group or of a group's role, as a list shows it
 * and as a method that gives one asks for it: the first day it counts and the last day
 * it does. Each is empty where there is none, and a list that carries no period (an
 * Auth whose schema holds none) shows neither.
 */
const periodColumns: Named[] = [
  ['startsAt', 'From'],
  ['endsAt', 'Until'],
];

/** The words for the two times of a method that gives something for a period. */
/**
 * Reads the answer of a method that mails a person (`{ sent, reason }`): `sent` once the
 * message went, and where it did not, the server's sentence for why, drawn as a failure.
 */
function mailed(sent: string): (answer: unknown) => { message: string; failed?: boolean } | undefined {
  return (answer) => {
    const said = (answer ?? {}) as { sent?: unknown; reason?: unknown };
    if (said.sent === true) {
      return { message: sent };
    }
    const reason =
      typeof said.reason === 'string' && said.reason !== '' ? said.reason : 'The message could not be sent.';
    return { message: reason, failed: true };
  };
}

const periodWords: Record<string, string> = { startsAt: 'From', endsAt: 'Until' };

/**
 * The fields that hold the end of a period. The server keeps an end as the first
 * instant the assignment no longer counts, and a person picks and reads a day: "until
 * 1 December" is through the end of 1 December. So a method's dialog sends the day
 * picked as the first instant of the day after it, in the viewer's time zone (the
 * method's `throughDay`), and a list shows the last day the assignment counts (lastDay).
 */
const periodEnds: readonly string[] = ['endsAt'];

/**
 * The last day a period that ends at the instant counts, as a list shows it: the day of
 * the instant before the end, in the viewer's time zone, which the list writes as a
 * calendar date. For an end a dialog of these pages sent it is the day that was picked,
 * and for any other instant the last day some of the period falls on. Empty for no end.
 */
function lastDay(end: unknown): string {
  if (typeof end !== 'string' && !(end instanceof Date)) {
    return '';
  }
  const instant = new Date(end).getTime();
  return Number.isNaN(instant) ? '' : new Date(instant - 1).toISOString();
}

/** The columns of a session, beside whose it is. */
const sessionColumns: Named[] = [
  ['authenticatedAt', 'Signed in'],
  ['updatedAt', 'Last active'],
  ['expired', 'Expired'],
];

/** The key of the password among the ways a person signs in (the row's `way`); every other way is an identity. */
const passwordWay = 'password';

/** Whether the ways a person signs in hold their password. */
function holdsPassword(ways: readonly RecordData[]): boolean {
  return ways.some((way) => way['way'] === passwordWay);
}

/** The ways a person signs in other than their password: the identities that can sign them in. */
function otherWays(ways: readonly RecordData[]): readonly RecordData[] {
  return ways.filter((way) => way['way'] !== passwordWay);
}

/**
 * What a person whose password is removed is left to sign in through: the words of the
 * ways the server listed beside the password (`through`, what the login page's button
 * carries for each), each once, in the list's order.
 */
function leftToSignInThrough(ways: readonly RecordData[]): string {
  const labels = [...new Set(otherWays(ways).map((way) => String(way['through'] ?? '')))].filter(
    (label) => label !== '',
  );
  if (labels.length === 0) {
    return 'They will sign in only through the other ways listed here.';
  }
  const last = labels[labels.length - 1];
  return `They will sign in through ${labels.length === 1 ? last : `${labels.slice(0, -1).join(', ')} or ${last}`} only.`;
}

/** A PascalCase name in words, the first capitalized: `CostCenter` is "Cost center". */
function words(name: string): string {
  const spaced = name
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2')
    .trim()
    .toLowerCase();
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

/** A word with its article: "a sector", "an office". */
function withArticle(word: string): string {
  return `${/^[aeiou]/i.test(word) ? 'an' : 'a'} ${word}`;
}

/** A PascalCase name as a route segment: `FleetStaff` is "fleet-staff". */
function kebab(name: string): string {
  return name
    .replace(/([a-z0-9])([A-Z])/g, '$1-$2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1-$2')
    .toLowerCase();
}
