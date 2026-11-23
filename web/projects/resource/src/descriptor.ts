import { Method, Resource, ScopeKind } from './brands';

/**
 * The operations a resource handle exposes. `batch` marks a resource whose mutations
 * ride the consolidated endpoint and can therefore be combined with other resources'
 * operations in one transaction.
 */
export type ResourceOperation = 'list' | 'read' | 'create' | 'patch' | 'remove' | 'batch' | 'patchOrAdd' | 'addOrSkip';

/**
 * A resource's page sizes: the page a request without `limit` receives, and the
 * largest page it may ask for. A resource with no `max` also permits `limit: 'all'`.
 * The maximum is also the switch every reader of a whole set reads (`readMode`): a
 * resource with none is small enough to load and is read whole in one request; one
 * with a maximum is read one server page at a time, never in one request, its chosen
 * row read by key. Declaring the maximum is the author's deliberate choice; nothing
 * decides at runtime from an observed size.
 */
export interface PageDescriptor {
  default: number;
  max?: number;
}

/** One entry of a resource's declared `@order`: a JSON field name and its direction. */
export interface OrderDescriptor {
  field: string;
  direction: 'asc' | 'desc';
}

/** One generated resource as the client needs to address it. */
export interface ResourceDescriptor {
  resource: Resource;
  /** The camelCase property the handle is exposed under on the client. */
  property: string;
  /** The kebab-case route segment, without API prefix or domain segment. */
  route: string;
  scope: ScopeKind;
  /** Mutations go to the consolidated endpoint (true) or to the resource's own PATCH route (false). */
  consolidated: boolean;
  /** JSON names of the primary-key fields, in route order. */
  keys: readonly string[];
  operations: readonly ResourceOperation[];
  /**
   * JSON names of the fields a patch may change — the same set that shapes the
   * generated Patch interface. Absent when the resource has no patch operation.
   * `changes()` enforces it: a form diff outside this list is an error, never a
   * silent drop.
   */
  patchable?: readonly string[];
  /** The resource's page sizes. Absent on a descriptor written by hand: the server's default applies. */
  page?: PageDescriptor;
  /**
   * The resource's declared `@order`, the order a list takes when the request names no
   * sort, with the primary key appended by the server so it is total. A resource that
   * declares one is paged by it when the request names no sort; one that declares none
   * needs a sort on every paged request, which the server refuses without one, and only
   * `limit: 'all'` reads it unsorted.
   */
  order?: readonly OrderDescriptor[];
  /**
   * The resource's `@file` segments: each is a file served at `<read route>/<segment>`,
   * gated by `Read` on the resource and a `Read` grant on the segment as a field. The
   * handle addresses one with `fileUrl(key, segment)`; the browser fetches it itself.
   * Absent when the resource declares none.
   */
  files?: readonly string[];
  /**
   * The feature flag that gates the resource (`@feature` on the struct), by name. While
   * the flag is off the resource is absent: its routes answer the outlet's not-found
   * handler, and the permission digest omits it, so a page over it is hidden by the
   * digest alone; the generated routes also skip it (a dark URL falls to the
   * application's wildcard). The generated `Feature` union in the application's
   * zz_gen_api.ts narrows this string; absent on an ungated resource.
   */
  feature?: string;
  /**
   * Set on a tenant-scoped resource whose list is also served without the tenant's
   * segment (`@listAcrossTenants`): one request reads its rows in every tenant the session
   * reaches in which the person may list it, each row checked in its own tenant and
   * naming it (`zzTenant`). The client hangs the list's handle off
   * `client.acrossTenants`. Absent, the resource is listed inside one tenant alone.
   */
  acrossTenants?: boolean;
}

/** One generated RPC method as the client needs to address it. */
export interface MethodDescriptor {
  method: Method;
  property: string;
  /** The kebab-case route segment, without API prefix or domain segment. */
  route: string;
  scope: ScopeKind;
  /** Set when the method answers with a result body; absent methods resolve with nothing. */
  answers?: boolean;
  /**
   * The statuses the method declares with `@answers`. A listed 4xx is the method's own
   * answer with its typed body, not a refusal by the frame: `execute` resolves with
   * `{ status, result }` for a method that declares statuses and throws ApiError for
   * every status outside the list.
   */
  statuses?: readonly number[];
  /**
   * Set on an `@upload` method: the request travels as multipart/form-data with the
   * JSON `request` part first and one `file` part per file, and the whole body is
   * bounded by `maxBytes`. The handle gains `upload(body, files)`.
   */
  upload?: { maxBytes: number };
  /**
   * The feature flag that gates the method (`@feature` on the method), by name. While
   * the flag is off the method is absent: its route answers not-found and the digest
   * omits its Execute entry. Absent on an ungated method.
   */
  feature?: string;
}

/** The domain route pair domain-scoped routes are served under: `<segment>/<domain>/...`. */
export interface DomainRouteDescriptor {
  segment: string;
  param: string;
  /**
   * What the application calls a tenant on screen, one and many: the tenant record's name
   * ("Customer", "Customers"), or the label the record declares for an application that
   * changed the word without renaming its tables. The frame's place switcher and menus
   * read it. Absent on a descriptor from a generator that predates it, where the frame
   * derives the word from the segment.
   */
  word?: { one: string; many: string };
}

/**
 * Everything the client needs to know about one generated API. The impulse TypeScript
 * generator emits it; the runtime interprets it. Routes carry no API prefix — the
 * client's `baseUrl` supplies it.
 *
 * The generator and this package release in either order, so every field added after
 * the package's first release is optional, and an absent one means the API does not
 * serve that feature: the client does nothing for it and everything else works as
 * before. `live` is such a field, and the next addition follows the same rule. A
 * descriptor from a newer generator may carry a field this package does not know yet;
 * the generated file hands the descriptor over as a value, which TypeScript checks
 * against the fields declared here and no others, and the client ignores the rest.
 */
export interface ApiDescriptor {
  resources: Record<string, ResourceDescriptor>;
  methods: Record<string, MethodDescriptor>;
  domainRoute?: DomainRouteDescriptor;
  /** The consolidated mutation route (`resources`), when consolidation is enabled. */
  consolidatedRoute?: string;
  permissionDigestRoute: string;
  userDomainsRoute: string;
  /**
   * Where the enabled set of feature flags is read (`GET <prefix>/features`, answering
   * `{ enabled: [...] }`), when the API serves feature flags: the generator writes the
   * block on every descriptor, since every outlet serves the route to a signed-in caller,
   * and the client loads the set once at sign-in, beside the digest, and again after the
   * person flips a flag. Absent, on a descriptor from a generator that predates feature
   * flags, the API serves none: the set stays empty and unloaded, every flag answers off,
   * nothing is requested, and the flags can be neither read nor flipped from the client.
   */
  features?: FeaturesRoute;
  /**
   * The live routes this outlet serves, when it serves live subscriptions: the generator
   * writes the block on every browser descriptor, and its presence is what says live is
   * served. Absent, a `{ live: true }` call is handled exactly as a plain one, and no
   * change feed can be started for the client.
   */
  live?: LiveRoutes;
  /**
   * The generated management of each Auth this outlet serves it for (GenerateIdentity):
   * the lists and the methods of each side, every one an ordinary entry of `resources`
   * and `methods`, under the key a page knows it by. The names differ in every
   * application (they carry the Auth's name and the tenant record's), so this block is
   * what lets the pages over an Auth's people be drawn from the description alone.
   * Absent where the outlet serves no Auth's generated management.
   */
  identity?: readonly IdentityDescriptor[];
  /**
   * The token routes this outlet serves, when its Auth issues tokens for the application's
   * machine outlets: where the signed-in person lists, issues and revokes their own tokens,
   * and where they look up, approve and deny a command-line client's device request. The
   * generator writes the block on the console's session outlet when the Auth has machine
   * outlets to issue for. Absent, on a descriptor from a generator that predates tokens or
   * for an Auth with no machine outlet, the outlet issues none: `userTokens(client)` is
   * undefined and a page over it says so.
   */
  tokens?: TokensDescriptor;
}

/**
 * One Auth's generated management: the Auth's name (the prefix of everything generated
 * for it), the words a page shows for it, the application's tenant record by its
 * struct's name where it has one, and the sides the outlet serves, the whole
 * application's first and a tenant's after it.
 */
export interface IdentityDescriptor {
  readonly auth: string;
  readonly label: string;
  readonly tenant?: string;
  /**
   * The tenant record's resource, where the outlet serves it. A list of the whole
   * application's side carries the key of the tenant a role or an invitation is in, an
   * id nobody reads: the pages read the tenant's name from this resource (its metadata's
   * `nameField`). Absent where the outlet does not serve the record, or on a description
   * from a generator that predates it, and the pages show the key.
   */
  readonly tenantResource?: string;
  /**
   * The directories that decide the Auth's roles, in the words of its login page's
   * buttons ("Google", "Google or Microsoft"), where a directory writes them at every
   * sign-in. Such an Auth keeps no groups of its own, and its Groups page says whose
   * groups do that job. Absent where the application gives the roles by hand, and on a
   * description from a generator that predates it.
   */
  readonly rolesFrom?: string;
  readonly sides: readonly IdentitySideDescriptor[];
}

/**
 * One side of an Auth's generated management: the whole application's (`global`: every
 * person of the Auth) or a tenant's (`domain`: the people who hold a role in the
 * route's tenant). A side names only what the outlet serves: a list the schema has no
 * table for and a method the Auth's sign-in methods do not give are absent.
 */
export interface IdentitySideDescriptor {
  readonly scope: ScopeKind;
  readonly lists: IdentityLists;
  readonly methods: IdentityMethods;
}

/**
 * The lists of a side, each a resource in `resources`: the people; the roles they hold
 * (one row for each role a person holds); the identities linked to them; the ways each
 * signs in (their password and the identities that can sign them in, as the server
 * judges it, listed for one person at a time); the invitations (a tenant's) or every
 * mailed link (the whole application's); and, for the whole application alone, the
 * sessions, the names whose sign-in is being counted or is locked, the Auth's sign-in
 * methods (each invite only or open to sign-up), in an application without tenants
 * the roles a person who signs up may be given, and, for the Auth whose people make the
 * application's tenants, the invitations that make a tenant and still stand. Where the Auth keeps groups of people,
 * the side also names the groups, the people in them, the roles they hold and the roles
 * each person holds through one. Where the application gives the Auth's roles by hand,
 * the whole application's side names the roles held for a set time with a reason.
 */
export interface IdentityLists {
  readonly users: string;
  /**
   * The roles people hold, one row for each: a role of the person's own and, where the
   * Auth keeps groups, a role that comes through a group, which the row names
   * (`groupId`, `groupName`, both empty for a role of the person's own). A row carries
   * the role's period and, for a role of the person's own, the reason it was given for
   * (`reason`). A description from a generator that predates the one list names the
   * roles each person holds directly here, and the ones held through a group under
   * `rolesThroughGroups`.
   */
  readonly roles?: string;
  /**
   * The list a role is picked from by the side's methods that give one (`assignRole`,
   * `invite`, `giveGroupRole`): the roles the caller could give. Where the
   * list carries a `method` field it answers for one method at a time, the one the
   * request's filter names (`method:eq:AssignCrewRole`), and a request that names none
   * is listed no role.
   */
  readonly rolesToGive?: string;
  readonly identities?: string;
  /**
   * The ways a person signs in, for an Auth with a password method: the password, where
   * the person holds one (the row whose `way` is `password`), and each linked identity
   * that can sign them in, with the words the login page's button carries for it
   * (`through`). The removal of a person's password is offered from it.
   */
  readonly signInWays?: string;
  readonly invitations?: string;
  readonly sessions?: string;
  readonly signInAttempts?: string;
  readonly signInMethods?: string;
  readonly signUpRoles?: string;
  /**
   * The groups of people, where the Auth keeps them: a tenant's own on its side. A row
   * says what the group holds: how many people (`people`) and which roles, a line each
   * (`roles`), over what has not ended.
   */
  readonly groups?: string;
  /** The people of the groups: one row for each person in each group, with their name. */
  readonly groupUsers?: string;
  /**
   * The roles the groups hold: one row for each role of each group, with how many people
   * lose the role when the group stops holding it (`losing`).
   */
  readonly groupRoles?: string;
  /** The roles people hold through a group, beside `roles`, the ones each holds directly. */
  readonly rolesThroughGroups?: string;
  /**
   * The invitations that make a tenant and still stand, on the whole application's side
   * of the Auth whose people make the tenants: staff bring an organization in by inviting
   * one person, and no tenant exists until that person first signs in. Each row carries
   * the invited address and the tenant's details as staff entered them, a field of the
   * tenant record under the record's name (`sectorName`), present only where the caller
   * may read that field on the record itself.
   */
  readonly invitedTenants?: string;
  /**
   * The roles held for a set time and for a reason, on the whole application's side: what
   * the Break glass page lists, each with whether its time is over (`ended`).
   */
  readonly takenRoles?: string;
  /**
   * The tenant's directory, on a tenant's side: the record a tenant's one directory is a
   * row of. The Sign-in settings page sets and removes it.
   */
  readonly directories?: string;
}

/**
 * The methods of a side, each a method in `methods`, by what it does. `invite` names
 * no row. On a tenant's side `assignRole` runs against a person and
 * `removeRole` against a role held; on the whole application's side both take the
 * person's name, since a role may be held by a name that has no account.
 * `withdrawInvitation` runs against an invitation, `unlinkIdentity` against an identity,
 * `endSession` against a session, `unlockSignIn` against a counted name, `openSignUp`
 * and `makeInviteOnly` name a sign-in method, which the page fills from its row, and the
 * rest run against a person. The methods over groups: `createGroup` names no row;
 * `describeGroup`, `deleteGroup`, `addGroupMember` and `giveGroupRole` run against a
 * group; `removeGroupMember` against a person's place in one; and `takeGroupRole`
 * against a role a group holds on a tenant's side, while the whole application's takes
 * the group's id and where the role is held. `removeFromTenant`, a tenant's side alone,
 * runs against a person: it takes away everything the person holds in the tenant itself,
 * and warns first, in the server's words, whether the account goes with it. `inviteTenant` names no row: it takes the
 * invited address and the tenant record's fields, and sends the invitation whose first
 * sign-in makes the tenant; `withdrawInvitedTenant` takes such an invitation's id
 * (`invitationId`), which the page fills from its row. `takeRole` names no row and no
 * person: the caller takes a role themselves for a set time, with a reason.
 */
export interface IdentityMethods {
  readonly invite?: string;
  readonly withdrawInvitation?: string;
  readonly assignRole?: string;
  readonly removeRole?: string;
  readonly disable?: string;
  readonly enable?: string;
  readonly delete?: string;
  readonly removeFromTenant?: string;
  readonly rename?: string;
  readonly setName?: string;
  readonly setEmail?: string;
  readonly sendPasswordResetLink?: string;
  readonly requirePasswordChange?: string;
  readonly endUserSessions?: string;
  readonly revokeTrustedDevices?: string;
  readonly unlinkIdentity?: string;
  readonly removePassword?: string;
  readonly endSession?: string;
  readonly unlockSignIn?: string;
  readonly openSignUp?: string;
  readonly makeInviteOnly?: string;
  readonly setSelfServiceReset?: string;
  readonly createGroup?: string;
  readonly describeGroup?: string;
  readonly deleteGroup?: string;
  readonly addGroupMember?: string;
  readonly removeGroupMember?: string;
  readonly giveGroupRole?: string;
  readonly takeGroupRole?: string;
  readonly inviteTenant?: string;
  readonly withdrawInvitedTenant?: string;
  readonly takeRole?: string;
  /**
   * On a tenant's side, how the tenant's people enter: `openToDirectory` names the role a
   * person gets the first time they sign in through the tenant's directory,
   * `makeDirectoryInviteOnly` clears it, and `makeDirectoryOnly` and `endDirectoryOnly`
   * turn the directory-only switch. None names a row.
   */
  readonly openToDirectory?: string;
  readonly makeDirectoryInviteOnly?: string;
  readonly makeDirectoryOnly?: string;
  readonly endDirectoryOnly?: string;
}

/** The route the enabled set of feature flags is read from, under the API prefix like every other route. */
export interface FeaturesRoute {
  route: string;
}

/**
 * The routes of a live outlet, under the API prefix like every other route: where a tab
 * renews its subscriptions, where it unsubscribes, and where it fetches the identity the
 * server minted for the change feed.
 */
export interface LiveRoutes {
  renewRoute: string;
  unsubscribeRoute: string;
  tokenRoute: string;
}

/**
 * The routes of an outlet whose Auth issues tokens, under the API prefix like
 * `permissionDigestRoute` and `userDomainsRoute`: a path relative to the client's
 * `baseUrl`, without the prefix and without a leading slash (`user/tokens`, never
 * `/api/user/tokens`). `route` is where the person's live tokens are listed (GET) and a
 * new application token is issued (POST); `revokeRoute` where one is revoked by id;
 * `deviceRoute` where a device request is looked up by the code the program printed
 * (GET, `userCode` in the query); `approveRoute` and `denyRoute` where it is decided.
 * `outlets` are the machine outlets the Auth issues tokens for, the choice an issue form
 * offers.
 */
export interface TokensDescriptor {
  route: string;
  revokeRoute: string;
  deviceRoute: string;
  approveRoute: string;
  denyRoute: string;
  outlets: readonly MachineOutletDescriptor[];
}

/**
 * One machine outlet of the application: `name` as the server knows it and a token names
 * it (`machines`), `prefix` where the outlet is mounted (`/machines`), for a page that
 * tells the person where a token signs in.
 */
export interface MachineOutletDescriptor {
  name: string;
  prefix: string;
}
