import { Method, Resource, ScopeKind } from './brands';

/**
 * The operations a resource handle exposes. `batch` marks a resource whose mutations
 * ride the consolidated endpoint and can therefore be combined with other resources'
 * operations in one transaction.
 */
export type ResourceOperation = 'list' | 'read' | 'create' | 'patch' | 'remove' | 'batch';

/**
 * A resource's page sizes: the page a request without `limit` receives, and the
 * largest page it may ask for. A resource with no `max` also permits `limit: 'all'`.
 * The maximum is also the switch every reader of a whole set reads (`readMode`): a
 * resource with none is small enough to load and is read whole in one request; one
 * with a maximum is read one server page at a time and never whole, its chosen row
 * read by key. Declaring the maximum is the author's deliberate choice; nothing
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
}

/**
 * Everything the client needs to know about one generated API. The ccc TypeScript
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
   * The user-management surfaces this outlet serves, one per Auth whose users it manages
   * (its own, and every Auth the generator program declared it manages): the users, seats
   * and roles resources of each partition and the methods beside them, all of them
   * ordinary entries of `resources` and `methods`, so the handles, the digest and the
   * capability envelope work for them as for everything else. The block says which
   * entries make up a surface and what the Auth's sign-in methods give it. Absent, on a
   * descriptor from a generator that predates the surfaces, the outlet serves none.
   */
  userSurfaces?: readonly UserSurfaceDescriptor[];
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
 * One Auth's user-management surface as the generator derived it from the Auth's sign-in
 * methods: `auth` is the Auth's name (`Crew`, the auth package's name in PascalCase),
 * `label` the words a page shows for it, `password` whether a password method is
 * registered (then a user is created with an optional password, a password is set for
 * one, and a password an operator sets is changed before anything else is served),
 * `usernameIsEmail` whether that method declares the username as the person's email
 * address (then the username and the address are not edited by hand: a verified new
 * address renames the user), `openSignUp` whether it lets a stranger create an account
 * from a mailed link, and `registrations` the external methods. The partitions come
 * global first; the tenant partition is present when the application has a tenant
 * record. A descriptor from a generator that predates the mailed links carries neither
 * flag.
 */
export interface UserSurfaceDescriptor {
  readonly auth: string;
  readonly label: string;
  readonly password: boolean;
  readonly usernameIsEmail?: boolean;
  readonly openSignUp?: boolean;
  readonly registrations: readonly UserRegistrationDescriptor[];
  readonly partitions: readonly UserPartitionDescriptor[];
}

/**
 * One external sign-in registration of the Auth: `invited`, the directory admits only
 * people the application created first (so the surface creates users without a
 * password); `syncsRoles`, the directory writes the roles of the people who sign in
 * through it at every sign-in (so the surface leaves their seats to the directory and
 * says so on the row, `rolesSynced`).
 */
export interface UserRegistrationDescriptor {
  readonly name: string;
  readonly kind: string;
  readonly label: string;
  readonly invited: boolean;
  readonly syncsRoles: boolean;
}

/**
 * One partition of a surface: the global one (every user of the Auth, with the seats
 * held globally and in every domain) or the tenant one (the users seated in the route's
 * tenant or everywhere, with their seats there). `users`, `seats`, `roles` and
 * `invitations` name the partition's resources in `resources` (`invitations`, the
 * standing invitations into the partition, is absent on a descriptor from a generator
 * that predates them); `setPassword`, `unlinkIdentity` and `resetFactors` the methods in
 * `methods`: the first two absent where the Auth's methods give none (no password method;
 * no external registration), the last, which forgets the browsers a user asked to be
 * remembered so their next sign-in asks for the second factor, on every Auth, absent only
 * on a descriptor from a generator that predates the second factor.
 */
export interface UserPartitionDescriptor {
  readonly scope: ScopeKind;
  readonly users: string;
  readonly seats: string;
  readonly roles: string;
  readonly invitations?: string;
  readonly setPassword?: string;
  readonly unlinkIdentity?: string;
  readonly resetFactors?: string;
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
