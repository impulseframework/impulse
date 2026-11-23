import { Route } from '@angular/router';
import {
  ApiDescriptor,
  IdentityDescriptor,
  IdentitySideDescriptor,
  quoteFilterValue,
  rowCapabilities,
  ScopeKind,
} from '@impulseframework/resource';
import { AuthorizationGuard } from '@impulseframework/resource-angular/auth-authorization-guard';
import { Page, page, pageRoute } from '@impulseframework/resource-angular/auth-frame';
import { resourcePage, resourceRoutes } from '@impulseframework/resource-angular/resource-route-generator';
import {
  ChildResourceConfig,
  ColumnConfig,
  computedDisplayField,
  ConfigElement,
  field,
  FieldName,
  ListPermission,
  listViewConfig,
  Method,
  MethodPresentation,
  multiColumnConfig,
  nameInLowerWords,
  nameInWords,
  PermissionScope,
  RecordData,
  Resource,
  ResourceMeta,
  roleInWords,
  rootConfig,
  RootConfig,
  section,
} from '@impulseframework/resource-angular/types';
import { format } from 'date-fns';
import { directoryGroupsSaysKey, DirectoryGroupsSays } from './directory-groups-says';

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
// inviting one person), the sessions, the failed sign-ins, the Auth's sign-in methods,
// each invite only or open to sign-up, and Break glass, where a person takes a role for a
// set time and says why. Where a directory decides the Auth's roles the Groups entry
// stays, and its page says whose groups do that job. What differs from one application
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
  'users' | 'groups' | 'invitations' | 'invitedTenants' | 'sessions' | 'signInAttempts' | 'signIn' | 'breakGlass';

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

/**
 * A page of the pages that lists nothing: where it lives, how the menus show it, the
 * grant it needs, and the sentences it says. The Groups page of an Auth whose roles a
 * directory decides is one.
 */
interface SaidPage {
  path: string;
  page: Page;
  scope: PermissionScope;
  says: DirectoryGroupsSays;
}

/** One page of a side, in menu order: a page the library's list draws, or one that says something. */
type BuiltPage = { config: RootConfig } | { said: SaidPage };

/** The pages of one request: every page in menu order, and the configurations among them. */
interface BuiltPages {
  pages: BuiltPage[];
  configs: RootConfig[];
}

// The pages made for each description and request, so the routes and the menu file's
// pages of one request are the same pages (resourcePage answers one per config), and the
// same request answers the same configurations.
const made = new WeakMap<ApiDescriptor, Map<string, BuiltPages>>();

/**
 * The page configurations of one side of an Auth's management, in menu order: the
 * people, the groups, the invitations, and for the whole application the invitations
 * that make a tenant, the sessions, the failed sign-ins, the sign-in methods and Break
 * glass, each with its label and icon. A page whose list the description
 * does not name is left out. The same request answers the same configurations. Refused, naming
 * what is missing: an Auth the description does not carry, a side it does not carry,
 * and a tenant's side on an API with no tenant segment.
 *
 * A page that lists nothing has no configuration and is not among them: the Groups page
 * of an Auth whose roles a directory decides, which `identityRoutes` and
 * `identityMenuPages` carry in its place.
 */
export function identityPages(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta | undefined,
  options: IdentityPagesOptions,
): RootConfig[] {
  return builtPages(descriptor, resourceMeta, options).configs;
}

/** The pages of one request, made once (see `made`). */
function builtPages(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta | undefined,
  options: IdentityPagesOptions,
): BuiltPages {
  const requests = made.get(descriptor) ?? new Map<string, BuiltPages>();
  made.set(descriptor, requests);
  const key = JSON.stringify([options.auth, options.scope, options.routes ?? {}]);
  const known = requests.get(key);
  if (known) {
    return known;
  }
  const pages = buildPages(descriptor, resourceMeta, options);
  const built = { pages, configs: pages.flatMap((page) => ('config' in page ? [page.config] : [])) };
  requests.set(key, built);
  return built;
}

function buildPages(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta | undefined,
  options: IdentityPagesOptions,
): BuiltPage[] {
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
  const listed = (config: RootConfig | undefined): BuiltPage[] => (config ? [{ config }] : []);
  const said = build.directoryGroups();
  return [
    ...listed(build.people()),
    ...listed(build.groups()),
    ...(said ? [{ said }] : []),
    ...listed(build.invitations()),
    ...listed(build.invitedTenants()),
    ...listed(build.sessions()),
    ...listed(build.signInAttempts()),
    ...listed(build.signIn()),
    ...listed(build.breakGlass()),
  ];
}

/**
 * The routes of one side of an Auth's management: `identityPages` through
 * `resourceRoutes`, which guards each page by the List grant on its resource and carries
 * its page. A tenant's routes go where the application puts its tenant pages; their
 * paths carry the tenant segment already. A page that lists nothing (the Groups page of
 * an Auth whose roles a directory decides) is a route of its own, guarded by the grant
 * its page needs.
 */
export function identityRoutes(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta,
  options: IdentityPagesOptions,
): Route[] {
  return builtPages(descriptor, resourceMeta, options).pages.map((built) =>
    'config' in built ? resourceRoutes(built.config, resourceMeta) : saidRoute(built.said),
  );
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
  return builtPages(descriptor, resourceMeta, options).pages.map((built) =>
    'config' in built ? resourcePage(built.config, resourceMeta) : built.said.page,
  );
}

/** The route of a page that says something: guarded by the grant its page needs, carrying the page and its sentences. */
function saidRoute(said: SaidPage): Route {
  return pageRoute(said.page, {
    path: said.path,
    data: { scope: said.scope, [directoryGroupsSaysKey]: said.says },
    canActivate: [AuthorizationGuard],
    loadComponent: () => import('./directory-groups-page.component').then((mod) => mod.DirectoryGroupsPageComponent),
  });
}

/** A column or a row page field: the row's field and the words above it. */
type Named = [field: string, words: string];

/** What a method of a side is: its name in the description's list of the side's methods (`invite`, `assignRole`). */
type MethodKind = keyof IdentitySideDescriptor['methods'];

class SideBuilder {
  private readonly tenant: boolean;
  /** The tenant record in words, lowercase ("sector", "cost center"); "tenant" where the description names none. */
  private readonly tenantWord: string;
  private readonly base: string;
  /** What each of the side's methods is, by the name the server gave it (`InviteCrewUser` is `invite`). */
  private readonly kinds: ReadonlyMap<string, MethodKind>;
  /** What the notice says once a method has run, by what the method is (doneWords). */
  private readonly done: Partial<Record<MethodKind, string>>;

  constructor(
    private readonly descriptor: ApiDescriptor,
    private readonly identity: IdentityDescriptor,
    private readonly side: IdentitySideDescriptor,
    private readonly resourceMeta: (resource: Resource) => ResourceMeta | undefined,
    private readonly options: IdentityPagesOptions,
  ) {
    this.tenant = side.scope === 'domain';
    this.tenantWord = identity.tenant ? nameInLowerWords(identity.tenant) : 'tenant';
    this.base = this.tenant ? `${this.tenantSegment()}/${kebab(identity.auth)}` : kebab(identity.auth);
    this.kinds = new Map(
      (Object.entries(side.methods) as [MethodKind, string | undefined][])
        .filter((entry): entry is [MethodKind, string] => entry[1] !== undefined)
        .map(([kind, name]) => [name, kind]),
    );
    this.done = doneWords;
  }

  /**
   * The people, each with what the caller may do to them, and a person's row page with
   * the roles and the identities under it. A person's roles are one list, each saying
   * where it comes from: their own, or the group it comes through.
   *
   * A row says the person's state in one column, State, a line for each thing there is
   * to say (statesOf): Disabled, Must change password, and on a tenant's list that
   * another tenant or the whole application also holds the person. It is blank when all
   * is well. Its words come from several fields, so the column neither sorts nor filters.
   * A person's page is headed by the person (personTitle), and says the same state on
   * one line, where there is any, and whether the email address is confirmed
   * (personFields).
   */
  people(): RootConfig {
    const { lists, methods } = this.side;
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
        // A tenant's people are those who hold a role in it: the last role taken away
        // takes the person off the list, and the list says so.
        ...(this.tenant
          ? {
              rowGone: (row: RecordData): string =>
                `${String(row['username'] ?? 'This person')} no longer holds a role here.`,
            }
          : {}),
        primaryResource: users,
        showRowCount: true,
        // A person's page is headed by the person.
        rowTitle: (row: RecordData) => this.personTitle(row),
        listColumns: [
          ...this.columns(users, [
            ['username', 'Username'],
            ['name', 'Name'],
            ['email', 'Email'],
          ]),
          // The person's state, on the field it says first.
          ...(this.has(users, 'disabled')
            ? [
                {
                  id: 'disabled' as FieldName,
                  header: 'State',
                  lines: true,
                  sortable: false,
                  filterable: false,
                  valueGetter: (row: RecordData) => this.statesOf(row),
                },
              ]
            : []),
          ...this.columns(users, [
            ['lastSignInAt', 'Last sign-in'],
            // Only a tenant's list of its people carries these, and only where the tenant
            // record has a directory-only switch. The first is a mark on each person the
            // switch keeps out while it is on and would keep out while it is off, which the
            // list filters by and the Sign-in settings page links to; the second is the
            // server's sentence for a person the switch keeps out, empty for everyone else.
            ['outsideDirectory', 'Kept out by directory only'],
            ['cannotEnter', 'Cannot enter'],
          ]),
          // Read with each row and not drawn: what the State column says besides Disabled,
          // and on a tenant's side whether the person holds something in the tenant itself.
          ...[
            ...this.known(
              users,
              this.stateLines().filter(([name]) => name !== 'disabled'),
            ).map(([name]) => name),
            ...this.heldHere(users),
          ].map((name) => ({ id: name as FieldName, hidden: true })),
        ],
        rowMethods: this.onPerson(),
        listMethods: this.present([
          [
            methods.invite,
            { label: 'Invite', icon: 'send', labels: this.where(), ...this.rolesWhere('roles', methods.invite) },
          ],
        ]),
        elements: [
          section({
            label: this.tenant ? 'Person' : 'Account',
            children: this.personFields(users),
          }),
        ],
      }),
      relatedConfigs: related,
    });
  }

  /**
   * The groups of people, each with what the caller may do to it, and a group's row page
   * with the people in it and the roles it holds under it. Left out where the Auth
   * keeps no groups (directoryGroups says why, where a directory decides the roles).
   *
   * A row says what the group holds: how many people, and its roles, a line each, so
   * nobody opens every group to learn what it is for. Delete is offered on a group that
   * holds nothing; one that still has people or roles says what is left where Delete
   * would be, and the server refuses a request that never saw the page. A group's page
   * is headed by the group's name. Outside every tenant the list says where each group
   * is kept, in an application that has tenants.
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
    // What the list says each group holds, where the server says it (`people`, `roles`).
    const counted = this.has(groups, 'people') && this.has(groups, 'roles');
    const holds: ColumnConfig[] = counted
      ? [
          { id: 'people' as FieldName, header: 'People', filterable: false },
          { id: 'roles' as FieldName, header: 'Roles', lines: true, filterable: false },
        ]
      : [];
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
        // A group's page is headed by its name.
        rowTitle: named,
        listColumns: [
          ...this.columns(groups, fields),
          ...holds,
          // Where the whole application's group is kept: the tenant by its name, where
          // the tenant record can be read, and in words for a group of the whole
          // application. A tenant's own list holds that tenant's groups alone, and an
          // application without tenants keeps every group in the one place.
          ...(this.has(groups, 'kind') && this.identity.tenant
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
              fill: (row) => this.keptPlace(row),
              ...this.rolesWhere('role', methods.giveGroupRole),
            },
          ],
          [
            methods.deleteGroup,
            counted
              ? {
                  label: 'Delete',
                  icon: 'delete',
                  // Only a group that holds nothing is deleted: one that still has people
                  // or roles says what is left, where Delete would be.
                  when: (row) => whatIsLeft(row) === undefined,
                  otherwise: (row) => emptyItFirst(row),
                  confirm: (row) => `Delete ${named(row)}?`,
                }
              : {
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
            label: 'About the group',
            children: this.known(groups, fields).map(([name, label]) =>
              field({ name: name as FieldName, label, cols: 6 }),
            ),
          }),
        ],
      }),
      relatedConfigs: related,
    });
  }

  /**
   * The Groups page of an Auth whose roles a directory decides: the Auth keeps no groups
   * of its own, since the directory's groups do that job, and the entry stays in the
   * menus with a page that says so and names the directory, so a person looking for
   * Groups finds the answer in place of a missing entry. It is shown to whoever sees the
   * people, by the List grant on them. Nothing where the Auth keeps its own groups, or
   * where the description names no directory.
   */
  directoryGroups(): SaidPage | undefined {
    const directory = this.identity.rolesFrom;
    if (this.side.lists.groups || !directory) {
      return undefined;
    }
    const path = this.route('groups', 'groups');
    const scope: PermissionScope = { resource: this.side.lists.users as Resource, permission: ListPermission };
    return {
      path,
      scope,
      page: page({ label: 'Groups', icon: 'groups', route: [path], permission: scope }),
      says: {
        title: this.tenant ? 'Groups' : `${this.identity.label} groups`,
        paragraphs: [
          `Here, ${directory} decides who holds which role, so the groups in ${directory} do the job that groups do in other applications.`,
          `To give several people a role at once, or take it away, change who is in the group in ${directory}. Each person's roles follow at their next sign-in.`,
          'There are no groups to keep in this application.',
        ],
      },
    };
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
          // The tenant a link invites into, where the application has tenants.
          ...(this.identity.tenant ? ([['tenantId', nameInWords(this.identity.tenant)]] as Named[]) : []),
          ['roleNames', 'Roles'],
          ['createdAt', 'Sent'],
          ['expiresAt', 'Expires'],
          ['consumedAt', 'Used'],
        ];
    // Whether the list says where a link that names no tenant gives its roles: the whole
    // application's list, in an application with tenants, where the reader may read the
    // mark of an invitation into every tenant.
    const placed = !this.tenant && this.identity.tenant ? this.has(resource, 'everyTenant') : false;
    // Whether the list can tell the first administrator's own invitation, which nobody
    // sent: where it says the place, and the reader may read who sent a link.
    const sender = placed && this.has(resource, 'invitedByUserId');
    // Whether the list can tell a link whose message could not be mailed: the whole
    // application's list, where the reader may read when the mail failed.
    const unmailed = !this.tenant && this.has(resource, 'mailFailedAt');
    const into = (row: RecordData): string => String(row['tenantId'] ?? '') || this.invitedInto(row, placed, sender);
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
        // The tenant a link invites into is named, where the tenant record can be read,
        // and a link that names none says where its roles are held instead. A link whose
        // message could not be mailed says so where its dates would be.
        listColumns: [
          ...this.columns(resource, fields).map((column) => {
            if (column.id === 'tenantId') {
              return this.tenantNamed(column.id, column.header ?? '', into);
            }
            return unmailed ? this.unmailedCell(column) : column;
          }),
          // Read with each row and not drawn.
          ...(placed ? [{ id: 'everyTenant' as FieldName, hidden: true }] : []),
          ...(sender ? [{ id: 'invitedByUserId' as FieldName, hidden: true }] : []),
          ...(unmailed ? [{ id: 'mailFailedAt' as FieldName, hidden: true }] : []),
        ],
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
   * with nothing written here for it. The starting role is picked from the roles the
   * server lists for the invitation, each by its name and description. A field the
   * caller may not read on the tenant record is absent from a row, and its cell is
   * empty. The caller sees an action only while their roles grant its method, as on
   * every list. To change an invitation's details, it is withdrawn and another sent,
   * which the question before a withdrawal says.
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
      ...ofRecord.map((name): Named => [name, nameInWords(name)]),
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
    const title = `Invited ${nameInLowerWords(listed)}`;
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
              shows: { defaultRole: ['name', 'description'] },
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

  /**
   * Break glass: a person takes a role for a set time and says why. The whole
   * application's alone, for an Auth whose roles the application gives by hand; in an
   * application with tenants the role is taken in one tenant, and without them it is a
   * role of the whole application, with no tenant to choose.
   *
   * "Take a role" above the first list opens the method's dialog: the tenant, the role,
   * how long, and why, each the method's own request field, the role and the length
   * picked from the lists the server offers. The role goes to the person who asks and
   * nobody else: the method names no person. "Held right now" lists the roles held for a
   * set time with a reason, whoever holds them, each with who, where, why, when it ends
   * and how long is left, and End now, which is the ordinary removal of the role and is
   * offered to whoever may run it. "Ended" lists the ones whose time is over, which the
   * server keeps until the person next signs in. The page is shown to whoever may list
   * the roles held for a set time, as every list page is.
   */
  breakGlass(): RootConfig | undefined {
    const { lists, methods } = this.side;
    if (this.tenant || !lists.takenRoles) {
      return undefined;
    }
    const taken = lists.takenRoles as Resource;
    const tenant = nameInWords(this.identity.tenant ?? 'Tenant');
    // Who holds which role where, and why: what both lists say of a row.
    const held: ColumnConfig[] = [
      ...this.columns(taken, [
        ['username', 'Username'],
        ['name', 'Name'],
      ]),
      ...(this.identity.tenant && this.has(taken, 'kind')
        ? [this.tenantNamed('domainId' as FieldName, tenant, (row) => this.heldIn(row))]
        : []),
      ...this.columns(taken, [
        ['role', 'Role'],
        ['reason', 'Why'],
      ]),
      // Read with each row and not drawn: what End now is filled from.
      ...this.columns(taken, [
        ['kind', 'Kind'],
        ['userId', 'User'],
        ...(this.identity.tenant ? [] : ([['domainId', 'Domain']] as Named[])),
      ]).map((column) => ({ ...column, hidden: true })),
    ];
    const when = (id: string, header: string): ColumnConfig[] =>
      this.has(taken, id) ? [{ id: id as FieldName, header, formatType: dayAndTime, filterable: false }] : [];
    const ended = listViewConfig({
      title: 'Ended',
      primaryResource: taken,
      showViewButton: false,
      showRowCount: true,
      collapsible: false,
      showSearchBox: false,
      filter: () => 'ended:eq:true',
      listColumns: [...held, ...when('startsAt', 'From'), ...when('endsAt', 'Ended')],
      rowMethods: false,
      elements: [],
    });
    return rootConfig({
      page: { label: 'Break glass', icon: 'lock_open' },
      routeData: { route: this.route('breakGlass', 'break-glass'), hasViewRoute: false },
      parentConfig: listViewConfig({
        title: 'Held right now',
        primaryResource: taken,
        showViewButton: false,
        showRowCount: true,
        showSearchBox: false,
        filter: () => 'ended:eq:false',
        listColumns: [
          ...held,
          // When it ends and how long is left, as the page was read.
          ...(this.has(taken, 'endsAt')
            ? [
                {
                  id: 'endsAt' as FieldName,
                  header: 'Ends',
                  valueGetter: (row: RecordData): string => endsIn(row['endsAt'], Date.now()),
                  filterable: false,
                },
              ]
            : []),
        ],
        rowMethods: this.present([
          [
            methods.removeRole,
            {
              label: 'End now',
              icon: 'timer_off',
              fill: (row) => this.heldRole(row),
              confirm: (row) =>
                `End ${roleInWords(String(row['role']))}${this.heldWhere(row)} for ${String(row['username'] ?? 'this person')} now? It is taken away, as any role is.`,
              successMessage: 'The role is ended.',
            },
          ],
        ]),
        listMethods: this.present([
          [
            methods.takeRole,
            {
              label: 'Take a role',
              icon: 'lock_open',
              labels: { domain: tenant, length: 'For how long', reason: 'Why you need it' },
              shows: { role: ['name', 'description'] },
              successMessage: 'You hold the role. It ends by itself when the time is over.',
            },
          ],
        ]),
        below: [ended],
        elements: [],
      }),
    });
  }

  /**
   * What a person's page is headed by: the name with the username beside it ("Ada
   * Lovelace (ada)"), the one of the two the row holds where it holds one, and the page's
   * word, "Person" or "Account", where the reader is shown neither.
   */
  private personTitle(row: RecordData): string {
    const name = typeof row['name'] === 'string' ? row['name'] : '';
    const username = typeof row['username'] === 'string' ? row['username'] : '';
    if (name !== '' && username !== '') {
      return `${name} (${username})`;
    }
    return name || username || (this.tenant ? 'Person' : 'Account');
  }

  /**
   * The fields of a person's page, each where the schema carries the field it reads. The
   * page says the state as the list does (statesOf), on one line, drawn only where there
   * is something to say, and whether the email address is confirmed (emailConfirmed). The
   * fields those two read are held by the page's form and not drawn: a field the page
   * says in words reads the form's values, and the form holds a field the page names.
   */
  private personFields(users: Resource): ConfigElement[] {
    const drawn = (named: Named[]): ConfigElement[] =>
      this.known(users, named).map(([name, label]) => field({ name: name as FieldName, label, cols: 4 }));
    return [
      ...drawn([
        ['username', 'Username'],
        ['name', 'Name'],
        ['email', 'Email'],
      ]),
      ...(this.has(users, 'emailVerifiedAt')
        ? [
            computedDisplayField({
              label: 'Email confirmed',
              cols: 4,
              calculatedValue: (values: RecordData | undefined) => emailConfirmed(values),
              shouldRender: (values: RecordData | undefined) => emailConfirmed(values) !== '',
            }),
          ]
        : []),
      // The state, where the schema carries the field the list's State column is on.
      ...(this.has(users, 'disabled')
        ? [
            computedDisplayField({
              label: 'State',
              cols: 4,
              calculatedValue: (values: RecordData | undefined) => this.statesOf(values).join(', '),
              shouldRender: (values: RecordData | undefined) => this.statesOf(values).length > 0,
            }),
          ]
        : []),
      ...drawn([
        ['lastSignInAt', 'Last sign-in'],
        ['cannotEnter', 'Cannot enter'],
        ['createdAt', 'Created'],
      ]),
      // Held by the form for the words above and for the methods offered, and not drawn.
      ...[
        ...this.known(users, [...this.stateLines(), ['emailVerifiedAt', 'Email confirmed']]).map(([name]) => name),
        ...this.heldHere(users),
      ].map((name) => field({ name: name as FieldName, cols: 4, shouldRender: false })),
    ];
  }

  /**
   * The roles a person holds, under the person's row: one list, each role saying where
   * it comes from. A role of the person's own says "Their own", carries the reason it
   * was given for beside when it ends, and is taken away here. A role that comes through
   * a group names the group and links to the group's page, where it is changed: the
   * group loses the role there, or the person leaves the group. Where the server's list
   * holds a person's own roles alone (a description from before the one list), the roles
   * held through a group are a second list under it (rolesThroughGroupsOf). Give a role
   * is offered above the list as well as in the person's menu (giveRole), the same
   * dialog in both places.
   */
  private rolesOf(): ChildResourceConfig | undefined {
    const { lists, methods } = this.side;
    if (!lists.roles) {
      return undefined;
    }
    const resource = lists.roles as Resource;
    // Whether the list holds the roles that come through a group too, each naming it:
    // the Auth keeps groups, and the list says which group a role comes through. An Auth
    // that keeps none has nothing to say of where a role comes from.
    const oneList = lists.groups !== undefined && this.has(resource, 'groupName');
    const own = (row: RecordData): boolean => !row['groupId'];
    // A row that comes through a group opens the group's page, where the application
    // mounts one and the row names a group of it (linksToGroup).
    const link = oneList ? this.linksToGroup(resource) : undefined;
    const comesFrom: ColumnConfig[] = oneList
      ? [
          { id: 'groupName' as FieldName, header: 'Comes from', valueGetter: (row: RecordData) => this.comesFrom(row) },
          // Read with each row and not drawn: which rows are the person's own, for a
          // tenant's row whether the group is the whole application's, and what the row
          // opens its group's page by.
          ...this.columns(resource, [
            ['groupId', 'Group'],
            ['globalGroup', 'Group of the whole application'],
            ...(link && link !== 'groupId' ? ([[link, 'Group page']] as Named[]) : []),
          ]).map((column) => ({ ...column, hidden: true })),
        ]
      : [];
    if (this.tenant) {
      return listViewConfig({
        title: 'Roles held here',
        primaryResource: resource,
        parentRelation: { parentKey: 'id' as FieldName, childKey: 'userId' as FieldName },
        showViewButton: link !== undefined,
        ...(link ? { rowRoute: link } : {}),
        collapsible: false,
        listColumns: [
          ...this.columns(resource, [['role', 'Role']]),
          ...comesFrom,
          ...this.columns(resource, [
            ['everyDomain', `In every ${this.tenantWord}`],
            ['createdAt', 'Given'],
            ...periodColumns,
            ['reason', 'Why'],
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
              // A role that comes through a group is not the person's to lose here.
              when: own,
              confirm: (row) =>
                `Take ${roleInWords(String(row['role']))} away from ${String(row['username'] ?? 'this person')}?`,
            },
          ],
        ]),
        listMethods: this.present([[methods.assignRole, this.giveRole()]]),
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
      showViewButton: link !== undefined,
      ...(link ? { rowRoute: link } : {}),
      collapsible: false,
      listColumns: [
        ...this.columns(resource, [['role', 'Role']]),
        ...comesFrom,
        // Where the role is held: the tenant by its name, where the tenant record can
        // be read, and in words for a role held in every tenant or the whole application.
        // An application without tenants holds every role in one place, and says none.
        ...(this.has(resource, 'kind') && this.identity.tenant
          ? [
              this.tenantNamed('domainId' as FieldName, 'Held in', (row) => this.heldIn(row)),
              // Read with each row and not drawn: what the words and the methods are filled from.
              { id: 'kind' as FieldName, hidden: true },
            ]
          : []),
        ...this.columns(resource, [['createdAt', 'Given'], ...periodColumns, ['reason', 'Why']]),
      ],
      rowMethods: this.present([
        [
          methods.removeRole,
          {
            label: 'Take the role away',
            icon: 'person_remove',
            // A role that comes through a group is not the person's to lose here.
            when: own,
            fill: (row) => this.heldRole(row),
            // The list sits under the person, and a role row carries their id, which
            // nobody reads: the question names the role and where it is held.
            confirm: (row) => `Take ${roleInWords(String(row['role']))}${this.heldWhere(row)} away from this person?`,
          },
        ],
      ]),
      listMethods: this.present([[methods.assignRole, this.giveRole()]]),
      elements: [],
    });
  }

  /**
   * The roles a person holds through a group, under the person's row, where the server's
   * list of a person's roles holds their own alone (a description from before the one
   * list): each names its group. Nothing is changed from here: a group's role is taken
   * away on the group's page, and a person leaves a group there. Nothing where the one
   * list holds them (rolesOf).
   */
  private rolesThroughGroupsOf(): ChildResourceConfig | undefined {
    const { lists } = this.side;
    if (!lists.rolesThroughGroups || (lists.roles && this.has(lists.roles as Resource, 'groupName'))) {
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
            ...(this.has(resource, 'kind') && this.identity.tenant
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
    // How many people lose the role, where the server counts them (`losing`): the people
    // in the group who hold the role in no other way. Read with each row and not drawn.
    const counted = this.columns(resource, [['losing', 'People who lose it']]).map((column) => ({
      ...column,
      hidden: true,
    }));
    // The method runs against the group the list sits under, and picks the role as the
    // same method does from the group's row: among the roles that group may hold in the
    // place the dialog holds.
    const give = this.present([
      [
        methods.giveGroupRole,
        {
          label: 'Give a role',
          icon: 'badge',
          labels: { ...this.where(), ...periodWords },
          throughDay: periodEnds,
          fill: (row) => this.keptPlace(row),
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
        listColumns: [
          ...this.columns(resource, [['role', 'Role'], ['createdAt', 'Given'], ...periodColumns]),
          ...counted,
        ],
        rowMethods: this.present([
          [
            methods.takeGroupRole,
            {
              label: 'Take the role away',
              icon: 'remove_moderator',
              confirm: (row) => `Take ${roleInWords(String(row['role']))} away from the group? ${whoLoses(row)}`,
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
        ...(this.has(resource, 'kind') && this.identity.tenant
          ? [
              this.tenantNamed('domainId' as FieldName, 'Held in', (row) => this.heldIn(row)),
              { id: 'kind' as FieldName, hidden: true },
            ]
          : []),
        ...this.columns(resource, [['createdAt', 'Given'], ...periodColumns]),
        ...counted,
      ],
      rowMethods: this.present([
        [
          methods.takeGroupRole,
          {
            label: 'Take the role away',
            icon: 'remove_moderator',
            fill: (row) => ({ groupId: row['groupId'], role: row['role'], ...this.heldPlace(row) }),
            confirm: (row) =>
              `Take ${roleInWords(String(row['role']))}${this.heldWhere(row)} away from the group? ${whoLoses(row)}`,
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

  /**
   * How Give a role is presented, in the person's menu and above the list of the
   * person's roles. A tenant's method names the person's row; the whole application's
   * takes the id of the person's account, which the person's row fills: the row offered
   * on in the menu, and the row the list sits under above it.
   */
  private giveRole(): Omit<MethodPresentation, 'method'> {
    const { methods } = this.side;
    return this.tenant
      ? {
          label: 'Give a role',
          icon: 'badge',
          labels: periodWords,
          throughDay: periodEnds,
          ...this.rolesWhere('role', methods.assignRole),
        }
      : {
          label: 'Give a role',
          icon: 'badge',
          fill: (row) => ({ userId: row['id'] }),
          labels: { ...this.where(), ...periodWords },
          throughDay: periodEnds,
          ...this.rolesWhere('role', methods.assignRole),
        };
  }

  /** How the methods that run against a person are presented. */
  private onPerson(): MethodPresentation[] {
    const { methods } = this.side;
    const named = (row: RecordData): string => String(row['username'] ?? 'this person');
    const removal = this.tenant ? methods.removeFromTenant : undefined;
    return this.present([
      [methods.assignRole, this.giveRole()],
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
      // A tenant's administrator takes a person out of the tenant: the method warns in the
      // server's words, so it carries no confirm of its own. A row that says the person
      // holds nothing in the tenant itself does not offer it; a row without the field does.
      [
        methods.removeFromTenant,
        {
          label: `Remove from the ${this.tenantWord}`,
          icon: 'person_remove',
          when: (row) => row['heldHere'] !== false,
        },
      ],
      // The page does not draw Delete on a row that offers the removal from the tenant,
      // on a tenant's side that has it: the removal is what the row offers in its place.
      // A row that does not offer the removal keeps Delete.
      [
        methods.delete,
        {
          label: 'Delete',
          icon: 'delete',
          confirm: (row) => `Delete ${named(row)}? The account, its sign-ins and its sessions are removed.`,
          ...(removal === undefined
            ? {}
            : { when: (row: RecordData) => !(rowCapabilities(row)?.Execute ?? []).includes(removal) }),
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
   * tenant. None in an application without tenants, whose methods have no field that
   * says where.
   */
  private where(): Record<string, string> {
    if (!this.identity.tenant) {
      return {};
    }
    return { domain: nameInWords(this.identity.tenant), everyDomain: `In every ${this.tenantWord}` };
  }

  /**
   * How a dialog that gives a role offers the roles: the role field's picker lists the
   * roles the caller could give with the dialog's own method. The generated list of the
   * roles to give answers for one method at a time, the one its filter names
   * (`method:eq:InviteCrewUser`), so each dialog offers what its own request is allowed
   * and nothing that only another method's is. The whole application's dialog also asks
   * where the role is to be held first (the tenant, "In every tenant", or neither for
   * the whole application), and its picker lists the roles that exist there: the filter
   * names the place beside the method (heldWhere). Every such dialog asks the same,
   * the invitation's too, so a role is given in every tenant only where the person said
   * so. A tenant's own dialogs pick from that tenant's roles, and an application
   * without tenants holds every role for the whole application: neither names a place.
   * A list that carries no `method` field lists the same roles for every method (a
   * tenant's roles, where nothing answers what the caller could give), and is asked for
   * none.
   *
   * The dialog that gives a group a role names the group too, by the id the dialog was
   * opened with, where the list takes one (`group`): the server then lists the roles
   * that group may hold, by the rule its method keeps, so the page holds no copy of it.
   */
  private rolesWhere(field: 'roles' | 'role', method: string | undefined): Pick<MethodPresentation, 'narrows'> {
    const list = this.side.lists.rolesToGive as Resource | undefined;
    const asked = method !== undefined && list !== undefined && this.has(list, 'method') ? `method:eq:${method}` : '';
    const placed = !this.tenant && this.identity.tenant !== undefined;
    const grouped =
      asked !== '' && method === this.side.methods.giveGroupRole && list !== undefined && this.has(list, 'group');
    if (!asked && !placed) {
      return {};
    }
    return {
      narrows: {
        [field]: (values) =>
          [asked, grouped ? ofGroup(values) : '', placed ? heldWhere(values) : '']
            .filter((part) => part !== '')
            .join(','),
      },
    };
  }

  /**
   * Where a group is given a role without the dialog asking: a group kept in one tenant
   * holds roles in that tenant alone, so the whole application's dialog is opened with
   * the tenant the group's row names and puts no question a person could only answer one
   * way. A group kept for the whole application holds a role anywhere a person can, and
   * its dialog asks where. The roles offered are the server's either way (rolesWhere).
   * Nothing in a tenant's own pages, whose method names no place, nor in an application
   * without tenants, whose methods have no field that says where.
   */
  private keptPlace(row: RecordData): Record<string, unknown> {
    if (this.tenant || !this.identity.tenant || row['kind'] !== heldInOneTenant) {
      return {};
    }
    return { domain: row['domainId'], everyDomain: false };
  }

  /**
   * The presentations of the methods the description names; one it does not name presents
   * nothing. Each carries what every offer of its method shares, wherever a page makes it
   * (shared).
   */
  private present(
    entries: [method: string | undefined, presentation: Omit<MethodPresentation, 'method'>][],
  ): MethodPresentation[] {
    return entries
      .filter((entry): entry is [string, Omit<MethodPresentation, 'method'>] => entry[0] !== undefined)
      .map(([method, presentation]) => ({ method: method as Method, ...this.shared(method, presentation) }));
  }

  /**
   * A method's presentation with what every offer of the method shares. The notice says
   * what happened once the method has run, in a sentence a person would say: a method
   * that sends an invitation reads its answer (invited), and every other the words for
   * what it is (doneWords); an offer that carries words of its own keeps them.
   */
  private shared(method: string, presentation: Omit<MethodPresentation, 'method'>): Omit<MethodPresentation, 'method'> {
    const kind = this.kinds.get(method);
    if (!kind) {
      return presentation;
    }
    const said: Omit<MethodPresentation, 'method'> = {};
    if (presentation.successMessage === undefined && presentation.answered === undefined) {
      const words = this.done[kind];
      if (invitations.includes(kind)) {
        said.answered = invited;
      } else if (words !== undefined) {
        said.successMessage = words;
      }
    }
    return { ...said, ...presentation };
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
        return `${nameInWords(this.identity.tenant ?? 'Tenant')} ${String(row['domainId'])}`;
      case heldInEveryTenant:
        return `Every ${this.tenantWord}`;
      default:
        return 'The whole application';
    }
  }

  /**
   * Where a mailed link that names no tenant gives its roles, in words: every tenant, or
   * the whole application, or both for the first administrator's own invitation, which
   * nobody sent and which gives the Super Administrator role (told where the list reads
   * who sent a link: sender). A link that gives no role says nothing, since nothing is held
   * anywhere: a verification, and an invitation that makes a tenant, which the list of
   * invited tenants shows. Nothing either where the list does not say the place (placed
   * is false): without the mark of an invitation into every tenant the two could not be
   * told apart.
   */
  private invitedInto(row: RecordData, placed: boolean, sender: boolean): string {
    if (!placed) {
      return '';
    }
    if (row['everyTenant'] === true) {
      return `Every ${this.tenantWord}`;
    }
    const roles: unknown = row['roleNames'];
    if (!Array.isArray(roles) || roles.length === 0) {
      return '';
    }
    // The first administrator's own invitation: its acceptance gives roles in the whole
    // application and in every tenant, so the row says both.
    const first =
      sender && row['kind'] === 'invitation' && !row['invitedByUserId'] && roles.includes(superAdministrator);
    return first ? `The whole application and every ${this.tenantWord}` : 'The whole application';
  }

  /**
   * Where a role of the one list of a person's roles comes from: "Their own" for a role
   * of the person's own, and the name of the group it comes through otherwise. A tenant's
   * row says when that group is the whole application's, which the tenant does not keep.
   */
  private comesFrom(row: RecordData): string {
    if (!row['groupId']) {
      return 'Their own';
    }
    const group = String(row['groupName'] || 'A group');
    return row['globalGroup'] === true ? `${group}, a group of the whole application` : group;
  }

  /**
   * The field of the one list of a person's roles a row opens its group's page by, where
   * rows can open one: the field names the groups this side lists (the metadata's
   * `enumeratedResource`), which the pages mount a row page for. A tenant's list names
   * the group's page by a field of its own (`groupPage`), empty for a group the tenant
   * does not keep; the whole application's by the group's id. Nothing where the Auth
   * keeps no groups or the list names no such field: the rows then open nowhere.
   */
  private linksToGroup(resource: Resource): FieldName | undefined {
    const groups = this.side.lists.groups;
    if (!groups) {
      return undefined;
    }
    const fields = this.resourceMeta(resource)?.fields ?? [];
    for (const name of ['groupPage', 'groupId']) {
      if (fields.some((candidate) => candidate.fieldName === name && candidate.enumeratedResource === groups)) {
        return name as FieldName;
      }
    }
    return undefined;
  }

  /**
   * What the whole application's method that takes a role from a person takes, from a
   * row that names the role and where it is held.
   */
  private heldRole(row: RecordData): Record<string, unknown> {
    return { userId: row['userId'], role: row['role'], ...this.heldPlace(row) };
  }

  /**
   * Where the role of a row is held, as the whole application's methods that take a role
   * away name it in their requests. A role held for the whole application or in every
   * tenant names no tenant: the page fills that with nothing (null), so the dialog asks
   * for none and puts its question. Left undefined, the dialog would ask the person for
   * it. In an application without tenants the methods have no field that says where,
   * and the server refuses a field a method does not have: the request names no place.
   */
  private heldPlace(row: RecordData): Record<string, unknown> {
    if (!this.identity.tenant) {
      return {};
    }
    return {
      domain: row['kind'] === heldInOneTenant ? row['domainId'] : null,
      everyDomain: row['kind'] === heldInEveryTenant,
    };
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
    const word = nameInWords(this.identity.tenant ?? 'Tenant');
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

  /**
   * A column of the whole application's mailed links that says when a link's message
   * could not be mailed. Such a link ended where the mail failed, so its Sent cell says
   * "Not mailed", its Expires cell "Ended" and its Used cell nothing: the dates the row
   * keeps would say it was sent, still waits and was used. Every other row shows its
   * dates, and every other column is left as it is.
   */
  private unmailedCell(column: ColumnConfig): ColumnConfig {
    const id: string = column.id;
    if (!(id in unmailedWords)) {
      return column;
    }
    const said = unmailedWords[id];
    return {
      id: column.id,
      header: column.header,
      valueGetter: (row: RecordData) => (row['mailFailedAt'] ? said : String(row[id] ?? '')),
    };
  }

  /**
   * The fields a person's state is said from on this side, each with the line a row
   * says where the field is true, in the order the lines are said: Disabled; Must change
   * password, where the side has a password method; and on a tenant's side that another
   * tenant also holds the person, that they hold a role in every tenant at once, or that
   * they hold a role in the whole application, which keeps Disable and Delete from the
   * tenant's administrator as a shared person does.
   */
  private stateLines(): Named[] {
    const password = this.side.methods.requirePasswordChange !== undefined;
    return [
      ['disabled', 'Disabled'],
      ...(password ? ([['passwordChangeRequired', 'Must change password']] as Named[]) : []),
      ...(this.tenant
        ? ([
            ['heldElsewhere', `Also in another ${this.tenantWord}`],
            ['heldInEveryTenant', `Holds a role in every ${this.tenantWord}`],
            ['heldGlobally', 'Also holds a role in the whole application'],
          ] as Named[])
        : []),
    ];
  }

  /**
   * What a person's row says of their state, a line each (stateLines): a line for each
   * field the row holds as true, so a field the reader's role leaves out of the row says
   * nothing. A row that says the person holds a role in every tenant does not also say
   * they are in another: the first says the second. Empty when all is well, and for no
   * row.
   */
  private statesOf(row: RecordData | undefined): string[] {
    if (!row) {
      return [];
    }
    return this.stateLines()
      .filter(([name]) => row[name] === true)
      .filter(([name]) => !(name === 'heldElsewhere' && row['heldInEveryTenant'] === true))
      .map(([, line]) => line);
  }

  /**
   * The field a tenant's side reads with a person and does not draw besides the State's:
   * whether the person holds something in the tenant itself, by which the removal from
   * the tenant is offered. Only where the list has it.
   */
  private heldHere(resource: Resource): string[] {
    return this.tenant && this.has(resource, 'heldHere') ? ['heldHere'] : [];
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
 * the roles held in every tenant or for the whole application, as "In every tenant"
 * says. The request then says the same place, and the server holds it to that place: a
 * request that names neither a tenant nor every tenant gives roles of the whole
 * application alone, so a role of a tenant's kind is given in every tenant only by a
 * person who ticked that.
 */
function heldWhere(values: RecordData): string {
  const domain = values['domain'];
  if (typeof domain === 'string' && domain !== '') {
    return `domain:eq:${quoteFilterValue(domain)}`;
  }
  return `everyDomain:eq:${values['everyDomain'] === true}`;
}

/**
 * The filter that names the group a dialog gives a role to, from the id the dialog was
 * opened with (`groupId`, the method's target). Nothing where the dialog holds none:
 * the server then lists no role for the method, which gives a role to a group alone.
 */
function ofGroup(values: RecordData): string {
  const group = values['groupId'];
  return typeof group === 'string' && group !== '' ? `group:eq:${quoteFilterValue(group)}` : '';
}

/**
 * Reads the answer of a method that sends an invitation (`{ id, sent }`). Where the
 * message could not be sent the server has ended the invitation, not mailed, so that no
 * link stands that nobody holds: the notice says so, as a failure, with what to do.
 */
function invited(answer: unknown): { message: string; failed?: boolean } {
  if (((answer ?? {}) as { sent?: unknown }).sent === true) {
    return { message: 'The invitation is on its way.' };
  }
  return {
    message: 'The invitation was not mailed, so it is ended. Invite the person again.',
    failed: true,
  };
}

/** The methods that send an invitation, whose notice reads the answer (invited). */
const invitations: readonly MethodKind[] = ['invite', 'inviteTenant'];

/**
 * What the notice says once a method has run, by what the method is: what happened, as a
 * person would say it, in place of the method's label and "done". A method that is not
 * here says its own: the ones that mail a person read their answer, and the Sign-in
 * page's and a withdrawn tenant invitation's carry their words where they are offered.
 */
const doneWords: Partial<Record<MethodKind, string>> = {
  withdrawInvitation: 'The invitation is withdrawn.',
  assignRole: 'The role was given.',
  removeRole: 'The role was taken away.',
  disable: 'The account is disabled.',
  enable: 'The account is enabled.',
  delete: 'The account was deleted.',
  removeFromTenant: 'The person was removed.',
  rename: 'The username was changed.',
  setName: 'The name was changed.',
  requirePasswordChange: 'The person is asked for a new password.',
  endUserSessions: 'The person is signed out everywhere.',
  revokeTrustedDevices: 'The trusted devices are forgotten.',
  unlinkIdentity: 'The sign-in identity is unlinked.',
  removePassword: 'The password is removed.',
  endSession: 'The session is ended.',
  unlockSignIn: 'The name is unlocked.',
  createGroup: 'The group was added.',
  describeGroup: 'The description was changed.',
  deleteGroup: 'The group was deleted.',
  addGroupMember: 'The person was added to the group.',
  removeGroupMember: 'The person was removed from the group.',
  giveGroupRole: 'The role was given to the group.',
  takeGroupRole: 'The role was taken away from the group.',
};

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
 * What a group still holds, as the row says it where Delete would be: "3 people and 2
 * roles", "1 person", "2 roles". Nothing for a group that holds nothing, which is the one
 * Delete is offered on. The counts are the server's, over what has not ended, which is
 * what it refuses a deletion for.
 */
function whatIsLeft(row: RecordData): string | undefined {
  const people = Number(row['people'] ?? 0);
  const roles = Array.isArray(row['roles']) ? row['roles'].length : 0;
  const left = [
    ...(people > 0 ? [people === 1 ? '1 person' : `${people} people`] : []),
    ...(roles > 0 ? [roles === 1 ? '1 role' : `${roles} roles`] : []),
  ];
  return left.length > 0 ? left.join(' and ') : undefined;
}

/** What a group that still holds something says where Delete would be; nothing for one that holds nothing. */
function emptyItFirst(row: RecordData): string | undefined {
  const left = whatIsLeft(row);
  return left ? `${left}: empty it first` : undefined;
}

/**
 * Who loses a role a group stops holding, as the question before it is taken away says
 * it: how many people, by the server's count of the people in the group who hold the
 * role in no other way. Where the row carries no count (a description from before it),
 * the question says the rule in place of the number.
 */
function whoLoses(row: RecordData): string {
  const losing = row['losing'];
  if (typeof losing !== 'number') {
    return 'Everyone in the group loses it, unless they hold it some other way.';
  }
  switch (losing) {
    case 0:
      return 'Nobody loses it: everyone in the group who holds it holds it some other way too, or nobody holds it through the group yet.';
    case 1:
      return '1 person loses it. Anyone else in the group holds it some other way and keeps it.';
    default:
      return `${losing} people lose it. Anyone else in the group holds it some other way and keeps it.`;
  }
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

/** The words for the two times of a method that gives something for a period. */
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
 * The role the deployment's first administrator is invited to hold, as the server names
 * it. The invitation that gives it and that nobody sent is the first administrator's own.
 */
const superAdministrator = 'SuperAdministrator';

/**
 * What the Sent, Expires and Used cells of a mailed link say when its message could not
 * be mailed: it was never sent, it waits for no deadline and nobody used it.
 */
const unmailedWords: Readonly<Record<string, string>> = {
  createdAt: 'Not mailed',
  expiresAt: 'Ended',
  consumedAt: '',
};

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

/**
 * How a list writes an instant that matters to the minute: the day and the time of day,
 * in the viewer's time zone. A role taken for an hour ends at a time, not on a day.
 */
const dayAndTime = 'M/d/yyyy h:mm a';

/**
 * How a page writes the day of an instant, in the viewer's time zone and with the year,
 * so a day of last year is not read as one of this year: "Nov 15, 2026".
 */
const dayWithYear = 'MMM d, yyyy';

/**
 * Whether a person's email address is confirmed, as their page says it: the day it was
 * ("Nov 15, 2026"); "Not confirmed" beside an address with no such day; and nothing where
 * the page holds neither, as a page holds a field the reader's role does not show, and
 * before the page's form is made.
 */
function emailConfirmed(values: RecordData | undefined): string {
  if (!values) {
    return '';
  }
  const at = values['emailVerifiedAt'];
  if (typeof at === 'string' || at instanceof Date) {
    const instant = new Date(at);
    if (!Number.isNaN(instant.getTime())) {
      return format(instant, dayWithYear);
    }
  }
  const email = values['email'];
  return typeof email === 'string' && email !== '' ? 'Not confirmed' : '';
}

/**
 * When a role held for a set time ends and how long is left, as a list says it in one
 * cell: the day and the time of day, then the time left in words ("11/18/2026 3:00 PM,
 * 45 minutes left"). The day and time alone once the instant has passed, and empty for
 * no instant.
 */
function endsIn(end: unknown, now: number): string {
  if (typeof end !== 'string' && !(end instanceof Date)) {
    return '';
  }
  const instant = new Date(end);
  if (Number.isNaN(instant.getTime())) {
    return '';
  }
  const at = format(instant, dayAndTime);
  const left = timeLeft(instant.getTime() - now);
  return left ? `${at}, ${left} left` : at;
}

/**
 * A length of time in words a person reads at a glance: "45 minutes", "3 hours 10
 * minutes", "1 day", rounded down to the unit, and "less than a minute" at the very end.
 * Empty for none.
 */
function timeLeft(left: number): string {
  if (Number.isNaN(left) || left <= 0) {
    return '';
  }
  const minutes = Math.floor(left / 60_000);
  if (minutes < 1) {
    return 'less than a minute';
  }
  const count = (n: number, unit: string): string => `${n} ${unit}${n === 1 ? '' : 's'}`;
  if (minutes < 60) {
    return count(minutes, 'minute');
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    const rest = minutes % 60;
    return rest === 0 ? count(hours, 'hour') : `${count(hours, 'hour')} ${count(rest, 'minute')}`;
  }
  return count(Math.floor(hours / 24), 'day');
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
