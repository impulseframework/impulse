import { Route } from '@angular/router';
import { ApiDescriptor, IdentityDescriptor, IdentitySideDescriptor, ScopeKind } from '@impulseframework/resource';
import { resourceRoutes } from '@impulseframework/resource-angular/resource-route-generator';
import {
  ChildResourceConfig,
  ColumnConfig,
  field,
  FieldName,
  listViewConfig,
  Method,
  MethodPresentation,
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
// page with the roles and the identities under it, the invitations, and, for the whole
// application, the sessions and the failed sign-ins. What differs from one application
// to the next is read from the description: the Auth's name and label, the tenant
// record's name, which side the pages are for, and what the Auth's sign-in methods give
// (a list or a method the description does not name draws nothing).
//
// The pages hold no logic of their own. A row's menu holds the methods the server says
// the caller may run on that row; the words, the icons and the questions are the
// library's, and the one thing the server cannot know is said here: Enable belongs on a
// disabled account and Disable on an enabled one.

/** The pages of one side of an Auth's management, by what each lists. */
export type IdentityPage = 'users' | 'invitations' | 'sessions' | 'signInAttempts';

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
   * The navigation group the pages are listed in. By default the Auth's label and
   * "accounts" for the whole application ("Crew accounts"), and the tenant record's
   * name with the Auth's label for a tenant ("Sector crew").
   */
  group?: string;
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

/**
 * The page configurations of one side of an Auth's management, in navigation order: the
 * people, the invitations, and for the whole application the sessions and the failed
 * sign-ins. A page whose list the description does not name is left out. Refused, naming
 * what is missing: an Auth the description does not carry, a side it does not carry,
 * and a tenant's side on an API with no tenant segment.
 */
export function identityPages(
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
  return [build.people(), build.invitations(), build.sessions(), build.signInAttempts()].filter(
    (config): config is RootConfig => config !== undefined,
  );
}

/**
 * The routes of one side of an Auth's management: `identityPages` through
 * `resourceRoutes`, which lists each page in the navigation and guards it by the List
 * grant on its resource. A tenant's routes go where the application puts its tenant
 * pages; their paths carry the tenant segment already.
 */
export function identityRoutes(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta,
  options: IdentityPagesOptions,
): Route[] {
  return identityPages(descriptor, resourceMeta, options).map((config) => resourceRoutes(config, resourceMeta));
}

/** A column or a row page field: the row's field and the words above it. */
type Named = [field: string, words: string];

class SideBuilder {
  private readonly tenant: boolean;
  /** The tenant record in words, lowercase ("sector", "cost center"); "tenant" where the description names none. */
  private readonly tenantWord: string;
  private readonly group: string;
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
    this.group =
      options.group ??
      (this.tenant ? `${words(identity.tenant ?? 'Tenant')} ${identity.label.toLowerCase()}` : `${identity.label} accounts`);
    this.base = this.tenant ? `${this.tenantSegment()}/${kebab(identity.auth)}` : kebab(identity.auth);
  }

  /** The people, each with what the caller may do to them, and a person's row page with the roles and the identities under it. */
  people(): RootConfig {
    const { lists, methods } = this.side;
    const password = methods.setTemporaryPassword !== undefined || methods.requirePasswordChange !== undefined;
    const fields: Named[] = [
      ['username', 'Username'],
      ['email', 'Email'],
      ['disabled', 'Disabled'],
      ...(password ? ([['passwordChangeRequired', 'Must change password']] as Named[]) : []),
      ['lastSignInAt', 'Last sign-in'],
      ['heldElsewhere', `Also in another ${this.tenantWord}`],
    ];
    const more: Named[] = [
      ['emailVerifiedAt', 'Email verified'],
      ['createdAt', 'Created'],
    ];
    const users = lists.users as Resource;
    const related = [this.rolesOf(), this.identitiesOf(), this.sessionsOf()].filter(
      (config): config is ChildResourceConfig => config !== undefined,
    );
    return rootConfig({
      nav: { navItem: { label: this.tenant ? 'People' : 'Accounts' }, group: this.group },
      routeData: { route: this.route('users', this.tenant ? 'people' : 'accounts'), hasViewRoute: true },
      parentConfig: listViewConfig({
        title: this.tenant ? 'People' : `${this.identity.label} accounts`,
        primaryResource: users,
        showRowCount: true,
        listColumns: this.columns(users, fields),
        rowMethods: this.onPerson(),
        listMethods: this.present([
          [methods.create, { label: this.tenant ? 'Add a person' : 'Add an account', icon: 'person_add', labels: this.where() }],
          [methods.invite, { label: 'Invite', icon: 'send', labels: this.where() }],
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
          ['roleNames', 'Roles'],
          ['createdAt', 'Sent'],
          ['expiresAt', 'Expires'],
          ['pending', 'Still open'],
        ]
      : [
          ['kind', 'Kind'],
          ['email', 'Email'],
          ['tenant', words(this.identity.tenant ?? 'Tenant')],
          ['roleNames', 'Roles'],
          ['createdAt', 'Sent'],
          ['expiresAt', 'Expires'],
          ['consumedAt', 'Used'],
        ];
    return rootConfig({
      nav: { navItem: { label: this.tenant ? 'Invitations' : 'Mailed links' }, group: this.group },
      routeData: { route: this.route('invitations', this.tenant ? 'invitations' : 'mailed-links'), hasViewRoute: false },
      parentConfig: listViewConfig({
        title: this.tenant ? 'Invitations' : `${this.identity.label} mailed links`,
        primaryResource: resource,
        showViewButton: false,
        showRowCount: true,
        listColumns: this.columns(resource, fields),
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
        listMethods: this.tenant ? this.present([[methods.invite, { label: 'Invite', icon: 'send' }]]) : [],
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
      nav: { navItem: { label: 'Sessions' }, group: this.group },
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
      nav: { navItem: { label: 'Failed sign-ins' }, group: this.group },
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
    // The whole application's roles are held by name, in the whole application, in one
    // tenant, or in every tenant, and the methods that give and take one take the
    // person's name: the page fills each from the row.
    return listViewConfig({
      title: 'Roles held',
      primaryResource: resource,
      parentRelation: { parentKey: 'username' as FieldName, childKey: 'user' as FieldName },
      showViewButton: false,
      collapsible: false,
      listColumns: [
        ...this.columns(resource, [['role', 'Role']]),
        ...(this.has(resource, 'kind')
          ? [{ id: 'kind' as FieldName, header: 'Held in', valueGetter: (row: RecordData): string => this.heldIn(row) }]
          : []),
        ...this.columns(resource, [['createdAt', 'Given']]),
      ],
      rowMethods: this.present([
        [
          methods.removeRole,
          {
            label: 'Take the role away',
            icon: 'person_remove',
            fill: (row) => ({
              username: row['user'],
              role: row['role'],
              domain: row['kind'] === heldInOneTenant ? row['domain'] : undefined,
              everyDomain: row['kind'] === heldInEveryTenant,
            }),
            confirm: (row) => `Take ${String(row['role'])} (${this.heldIn(row).toLowerCase()}) away from ${String(row['user'])}?`,
          },
        ],
      ]),
      elements: [],
    });
  }

  /** The identities a person signs in with, under the person's row. */
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
          ? { label: 'Give a role', icon: 'badge' }
          : // The whole application's method takes the person's name, which the row fills.
            { label: 'Give a role', icon: 'badge', fill: (row) => ({ username: row['username'] }), labels: this.where() },
      ],
      [methods.rename, { label: 'Rename', icon: 'edit' }],
      [methods.setEmail, { label: 'Change the email address', icon: 'mail' }],
      [methods.setTemporaryPassword, { label: 'Set a temporary password', icon: 'password' }],
      [
        methods.requirePasswordChange,
        {
          label: 'Require a new password',
          icon: 'lock_reset',
          confirm: (row) => `Make ${named(row)} choose a new password the next time they sign in?`,
        },
      ],
      [
        methods.endUserSessions,
        { label: 'Sign out everywhere', icon: 'logout', confirm: (row) => `Sign ${named(row)} out of every browser and device?` },
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

  /** The presentations of the methods the description names; one it does not name presents nothing. */
  private present(entries: [method: string | undefined, presentation: Omit<MethodPresentation, 'method'>][]): MethodPresentation[] {
    return entries
      .filter((entry): entry is [string, Omit<MethodPresentation, 'method'>] => entry[0] !== undefined)
      .map(([method, presentation]) => ({ method: method as Method, ...presentation }));
  }

  /** Where a role of the whole application's list is held, in words. */
  private heldIn(row: RecordData): string {
    switch (row['kind']) {
      case heldInOneTenant:
        return `${words(this.identity.tenant ?? 'Tenant')} ${String(row['domain'])}`;
      case heldInEveryTenant:
        return `Every ${this.tenantWord}`;
      default:
        return 'The whole application';
    }
  }

  /** Whether the resource has the field, by its metadata. */
  private has(resource: Resource, name: string): boolean {
    return (this.resourceMeta(resource)?.fields ?? []).some((candidate) => candidate.fieldName === name);
  }

  /** The named fields the resource has, in the order given: a field this Auth's schema does not carry is left out. */
  private known(resource: Resource, named: Named[]): Named[] {
    return named.filter(([name]) => this.has(resource, name));
  }

  /** A column for each named field the resource has. */
  private columns(resource: Resource, named: Named[]): ColumnConfig[] {
    return this.known(resource, named).map(([name, header]) => ({ id: name as FieldName, header }));
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

/** The columns of a session, beside whose it is. */
const sessionColumns: Named[] = [
  ['authenticatedAt', 'Signed in'],
  ['updatedAt', 'Last active'],
  ['expired', 'Expired'],
];

/** A PascalCase name in words, the first capitalized: `CostCenter` is "Cost center". */
function words(name: string): string {
  const spaced = name
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2')
    .trim()
    .toLowerCase();
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

/** A PascalCase name as a route segment: `FleetStaff` is "fleet-staff". */
function kebab(name: string): string {
  return name
    .replace(/([a-z0-9])([A-Z])/g, '$1-$2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1-$2')
    .toLowerCase();
}
