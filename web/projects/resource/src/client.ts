import {
  CreatePermission,
  DeletePermission,
  Domain,
  ExecutePermission,
  Method,
  Permission,
  PermissionScope,
  Resource,
  UpdatePermission,
} from './brands';
import { AuthClient, loginMethodsRoute } from './auth';
import { ApiDescriptor, MethodDescriptor, ResourceDescriptor, ResourceOperation } from './descriptor';
import { Capability, PermissionDigestState, WithCapabilities, rowCapabilities } from './digest';
import { FeatureFlag, FeatureState, featureFlagsResource, setFeatureMethod } from './features';
import {
  LiveOptions,
  LiveSession,
  LiveSubscription,
  liveSubscription,
  subscribeHeader,
  versionParam,
  versionedUrl,
} from './live';
import { BatchResult, Operation, OperationRoute, operationRoute } from './operations';
import { ClientResponse, PermissionStore, Requester, RequestOptions, ResponseRequester } from './permissions';
import { Store } from './store';
import { dryRunHeader } from './transport';
import { LinkHeader, ListQuery, ReadOptions, TotalCountHeader, listSearchParams, parseLinkHeader, readSearchParams } from './query';
import { readMode, wholeListQuery } from './reading';
import {
  ApiError,
  ApiVersionError,
  HttpMethod,
  MaintenanceError,
  Transport,
  TransportResponse,
  apiVersionHeader,
  fetchTransport,
  maintenanceHeader,
  maintenanceHeaderValue,
  retryAfterHeader,
  retryAfterSeconds,
  timeZoneHeader,
} from './transport';
import { compareReleases, releaseVersion } from './versions';
import { Warn, warnOnce } from './warnings';

export interface ClientOptions {
  /** The API prefix every route is served under, e.g. `/api` or `https://host/api`. */
  baseUrl: string;
  /** Defaults to fetchTransport(). */
  transport?: Transport;
  /** Observes every non-2xx response before it is thrown. */
  onError?: (error: ApiError) => void;
  /**
   * Receives each warning the client raises, once per distinct message: a page asked for
   * something the generated API descriptor says the API does not serve, and the client
   * answered without it, naming the regeneration that would serve it. `console.warn` by default.
   */
  warn?: Warn;
  /** The live session's knobs (renewal interval, page events, the keepalive fetch); every one has a default. */
  live?: LiveOptions;
  /**
   * The release this build of the application was made from, sent as the version header
   * (`X-Api-Version`) on every request so the server can refuse a build older than the
   * oldest release it answers. Absent, empty, or `dev`, the local build's name, nothing is
   * sent and the build is never refused.
   */
  apiVersion?: string;
  /**
   * The pauses, in milliseconds, before each retry of a request a server older than this
   * build refused: traffic shifts instance by instance during a deploy, so the next try may
   * land on the new one. When every retry is refused the error is thrown. Three retries
   * over ten seconds by default (olderServerRetryDelays).
   */
  olderServerRetryDelays?: readonly number[];
  /**
   * The person's time zone, an IANA name such as America/New_York, sent as the Time-Zone
   * header on every request: the server reads a filter value written with no zone of its
   * own, `2026-12-15` or `2026-12-15T09:30`, as the whole of that day or minute in it, so a
   * list filters by the day the browser shows. Absent, the browser's own zone is sent
   * (browserTimeZone); an empty value sends none, and the server reads such a value in the
   * column's declared zone, else UTC.
   */
  timeZone?: string;
}

/** The default pauses before each retry of a request a server older than this build refused: 1 s, 3 s, 6 s. */
export const olderServerRetryDelays: readonly number[] = [1000, 3000, 6000];

/**
 * The zone the person's device is set to, as Intl resolves it: an IANA name such as
 * America/New_York, which the client sends as the Time-Zone header when ClientOptions
 * names none; undefined where the device's zone is unknown (Etc/Unknown) or Intl is absent.
 */
export function browserTimeZone(): string | undefined {
  try {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    return zone && zone !== 'Etc/Unknown' ? zone : undefined;
  } catch {
    return undefined;
  }
}

/** The zone the client sends: the option as given, the browser's own when the option is absent, none for an empty one. */
function timeZoneToSend(option: string | undefined): string | undefined {
  const zone = option ?? browserTimeZone();
  return zone === undefined || zone.trim() === '' ? undefined : zone;
}

/** The request's headers with the release and the zone the client sends on every request; the caller's own alone when it sends neither. */
function requestHeaders(
  own: Record<string, string> | undefined,
  version: string | undefined,
  zone: string | undefined,
): Record<string, string> | undefined {
  if (version === undefined && zone === undefined) {
    return own;
  }
  return {
    ...own,
    ...(version === undefined ? {} : { [apiVersionHeader]: version }),
    ...(zone === undefined ? {} : { [timeZoneHeader]: zone }),
  };
}

/** What every resource handle offers, whatever operations the resource supports. */
export interface ResourceHandleBase<Row, Key extends unknown[]> {
  readonly resource: Resource;
  readonly descriptor: ResourceDescriptor;
  /** The partition this handle is bound to; undefined for a global resource. */
  readonly domain?: Domain;
  /** The absolute URL of the collection, or of one row when a key is given. */
  url(key?: Key): string;
  /**
   * The absolute URL of a row's file: the `@file` route under the row's read route,
   * `content` by default. Nothing fetches; put it in an `<img src>` or an `<a href>` and
   * the browser fetches it with the session, revalidating a kept copy with the route's
   * validator. A segment the descriptor does not list is an error: the route does not
   * exist.
   */
  fileUrl(key: Key, segment?: string): string;
  /** The operation path (route plus key segments, no API prefix) used in mutation bodies. */
  path(key?: Key): string;
  /** The row's primary key, in route order. */
  keyOf(row: Row): Key;
  /**
   * What a live request of this handle subscribes to: the list (resource and domain, no
   * domain for a global resource) with no key, or one row (resource and key, and the
   * handle's domain when the resource is domain-scoped) with one. A store hands it to
   * `client.live.watch` beside the refetch the change should run.
   */
  subscription(key?: Key): LiveSubscription;
  /** Asks the digest of this handle's scope. Conditional grants answer true. */
  can(permission: Permission): boolean;
  state(permission: Permission): PermissionDigestState | undefined;
  /**
   * The fields this handle's digest says the session user may supply for the
   * permission — `Create` narrows a create form to the inputs worth rendering:
   * sorted JSON field names whose dotted field target is granted or conditional
   * (conditional renders; the server judges the write). Undefined when the digest
   * carries no field-level entries for the permission — no field information (the
   * permission is denied outright, or the resource has no grant-bearing fields) —
   * so narrow only on a defined answer. Key fields never appear: they are
   * structural, not grant-bearing.
   */
  grantedFields(permission: Permission): readonly string[] | undefined;
  /**
   * Asks the row first, the digest second: a capability envelope on the row decides
   * `Update`/`Delete` for that row — `Execute` whether the named RPC method's
   * declared transition applies to it, and `Create` whether the named workflow member
   * resource may be created beneath it — while without an envelope the scope's digest
   * decides. `Execute` requires the method name; `Create` the member resource name.
   */
  rowCan(row: Row, permission: Capability, target?: Method | string): boolean;
  /** Whether the session user may write this field on this row (envelope first, digest second). */
  fieldEditable(row: Row, field: keyof Row & string): boolean;
}

/** The row type a read returns: with the capability envelope when the query asked for one. */
export type Returned<Row, Query> = Query extends { capabilities: Capability[] } ? WithCapabilities<Row> : Row;

/**
 * One page of a list. `next` and `prev` follow the server's Link relations exactly as
 * issued and are absent where no such page exists; `total` answers a `count: true`
 * request on a first page. Every paged request carries an order (the resource's
 * declared `@order` or the request's sort; the server refuses one with neither), so a
 * page always has the relations that exist. A key-less resource (an empty `keys`
 * tuple) is served whole on every request: its one page is the whole list, with no
 * `next` and no `prev`.
 */
export interface Page<Row> {
  rows: Row[];
  total?: number;
  next?: () => Promise<Page<Row>>;
  prev?: () => Promise<Page<Row>>;
  /**
   * The same page again: the request that produced it, repeated as issued — the first
   * page's parameters, or the cursor a relation handed out — so a table refreshes the
   * page it is on after a write without losing its position.
   */
  reload: () => Promise<Page<Row>>;
}

/**
 * The list operations of a handle. `Key` is the resource's key tuple: on the empty
 * tuple, a key-less resource served whole, the query type carries no `limit` and no
 * `cursor` (ListQuery), so a page asked of such a handle does not compile. There is no
 * operation that reads every row of a resource with a maximum page size: the maximum is
 * declared to keep the whole set out of memory, so such a resource is read one server
 * page at a time (`page`), and a resource with no maximum is read whole with
 * `list({ limit: 'all' })` (see readMode and wholeListQuery).
 */
export interface Listable<Row, Key extends readonly unknown[] = readonly unknown[]> {
  /**
   * One page of rows, the resource's default page when the query names no limit; every
   * row with `limit: 'all'` on a resource with no maximum; the whole list on a key-less
   * resource.
   */
  list<Query extends ListQuery<Row, Key> | undefined = undefined>(query?: Query): Promise<Returned<Row, Query>[]>;
  /**
   * One page with its neighbors: follow `next` and `prev` as the server names them. On
   * a key-less resource the one page is the whole list and has no neighbors.
   */
  page<Query extends ListQuery<Row, Key> | undefined = undefined>(query?: Query): Promise<Page<Returned<Row, Query>>>;
}

export interface Readable<Row, Key extends unknown[]> {
  read<Options extends ReadOptions<Row> | undefined = undefined>(
    key: Key,
    options?: Options,
  ): Promise<Returned<Row, Options>>;
}

export interface Creatable<Create> {
  /**
   * Creates one row. Key fields present in the value become path segments; the
   * server-generated id of the new row is returned when the server allocates one.
   */
  create(value: Create): Promise<string | undefined>;
}

export interface Patchable<Patch, Key extends unknown[]> {
  patch(key: Key, value: Patch): Promise<void>;
}

export interface Removable<Key extends unknown[]> {
  remove(key: Key): Promise<void>;
}

/** Operation builders for the consolidated endpoint; pass their results to `client.batch`. */
export interface Batchable<Create, Patch, Key extends unknown[]> {
  readonly ops: {
    add(value: Create): Operation;
    patch(key: Key, value: Patch): Operation;
    remove(key: Key): Operation;
  };
}

/**
 * A typed handle on one resource in one scope. `Ops` names the operations the server
 * generated for it; the others do not exist on the type, so a call the server would
 * refuse does not compile.
 */
export type ResourceHandle<
  Row,
  Key extends unknown[],
  Ops extends ResourceOperation,
  Create = never,
  Patch = never,
> = ResourceHandleBase<Row, Key> &
  ('list' extends Ops ? Listable<Row, Key> : unknown) &
  ('read' extends Ops ? Readable<Row, Key> : unknown) &
  ('create' extends Ops ? Creatable<Create> : unknown) &
  ('patch' extends Ops ? Patchable<Patch, Key> : unknown) &
  ('remove' extends Ops ? Removable<Key> : unknown) &
  ('batch' extends Ops ? Batchable<Create, Patch, Key> : unknown);

/** A handle with every operation, for resources the generator did not describe. */
export type AnyResourceHandle<Row = Record<string, unknown>, Key extends unknown[] = string[]> = ResourceHandle<
  Row,
  Key,
  ResourceOperation,
  Record<string, unknown>,
  Record<string, unknown>
>;

/**
 * The answer of a method that declares its statuses (`@answers`): the status the
 * method chose for this response and its typed result. A 4xx here is the method's own
 * refusal, resolved rather than thrown, because the method declared it and typed its
 * body.
 */
export interface MethodAnswer<Status extends number = number, Result = unknown> {
  status: Status;
  result: Result;
}

export interface MethodHandle<Body, Result = void> {
  readonly method: Method;
  readonly descriptor: MethodDescriptor;
  readonly domain?: Domain;
  url(): string;
  /**
   * Posts the body to the Execute-gated route and resolves with the method's answer:
   * the generated `<Method>Result` for a method whose Execute returns one, the
   * generated `<Method>Answer` (`{ status, result }`) for a method that declares its
   * statuses, nothing for the rest. A status the method did not declare, and every
   * refusal by the frame, rejects with ApiError.
   */
  execute(body: Body): Promise<Result>;
  /**
   * Runs the method's whole frame — decode, the entry check, the target's checks, the
   * body with every write it arms — and rolls the transaction back instead of
   * committing. It resolves when the real call would commit and rejects with the
   * same ApiError the real call would raise, so a control can be enabled or a refusal
   * explained before the user commits. A 4xx the method declares is still a refusal
   * here: nothing would commit, and the ApiError carries the method's typed body. A
   * method that runs outside a transaction rejects with 400.
   */
  dryRun(body: Body): Promise<void>;
  can(): boolean;
  state(): PermissionDigestState | undefined;
}

/**
 * The handle of an `@upload` method: `execute` and `dryRun` as on any method, and
 * `upload`, which sends the body with files as multipart/form-data — the JSON body
 * as the `request` part first, then one `file` part per file — and resolves like
 * `execute`. The sum of the files' sizes is checked against the declared maximum
 * before anything is sent, with the same message shape as the server's 413.
 */
export interface UploadMethodHandle<Body, Result = void> extends MethodHandle<Body, Result> {
  upload(body: Body, files: readonly (File | Blob)[]): Promise<Result>;
}

/** The name of the multipart part carrying the method's JSON, sent first. */
export const uploadRequestPart = 'request';
/** The name of each multipart file part. */
export const uploadFilePart = 'file';

/** Renders a byte count the way @upload declares it: whole GB, MB, or KB when even, bytes otherwise. */
export function formatByteSize(n: number): string {
  const kb = 1024;
  const mb = kb * 1024;
  const gb = mb * 1024;
  if (n >= gb && n % gb === 0) return `${n / gb}GB`;
  if (n >= mb && n % mb === 0) return `${n / mb}MB`;
  if (n >= kb && n % kb === 0) return `${n / kb}KB`;
  return `${n} bytes`;
}

export interface DomainClientBase {
  readonly domain: Domain;
  /** Asks this partition's digest about a domain-scoped resource or method. */
  can(permission: Permission, target: Resource | Method): boolean;
}

export type DomainClient<D> = DomainClientBase & D;

export interface ClientBase {
  readonly descriptor: ApiDescriptor;
  readonly baseUrl: string;
  readonly permissions: PermissionStore;
  /**
   * The tab's live session: start it with a change feed once the session is
   * authenticated, watch rows and lists on it, and stop it at logout before the session
   * is logged out. See LiveSession.
   */
  readonly live: LiveSession;
  /**
   * The enabled set of feature flags: loaded once at sign-in beside the digest, asked
   * synchronously (`enabled(name)`), and refreshed by the person who flips a flag
   * (`setFeature`). See FeatureState.
   */
  readonly features: FeatureState;
  /**
   * The sign-in client: the methods the server offers its login page, the password form's
   * post, the redirect method's URL, and the pending sign-in's status, confirmation and
   * cancel. See AuthClient.
   */
  readonly auth: AuthClient;
  /**
   * The release the server named on its last answer (`X-Api-Version`, which every answer
   * of an outlet carries), undefined until an answer named one: what a release line shows,
   * read with `get()` and watched with `subscribe()`. `dev` is a server built locally.
   */
  readonly serverRelease: Store<string | undefined>;
  /**
   * The client's warning hook (`ClientOptions.warn`, the console by default), each
   * distinct message delivered once: what the client announces the descriptor's gaps
   * through, and what a page layer announces its own through, so an application's
   * warnings arrive in one place.
   */
  readonly warn: Warn;
  /** Issues a request under baseUrl; the escape hatch for routes the generator did not describe. */
  readonly request: Requester;
  /** Issues a request and resolves with the headers too. */
  readonly requestResponse: ResponseRequester;
  /**
   * Sends operations as one transaction. Operations built by consolidated resources go
   * to the consolidated endpoint; operations built by one standalone resource (a
   * `consolidated: false` descriptor) go to that resource's own PATCH route, which
   * offers the same transaction over its own rows. Operations from more than one
   * endpoint cannot commit together and are refused before any request is sent.
   */
  batch(operations: Operation[]): Promise<BatchResult>;
  /**
   * Asks the right digest: the descriptor decides whether the target is global or
   * domain-scoped; a domain-scoped target with no domain answers false.
   */
  can(permission: Permission, target: Resource | Method, domain?: Domain): boolean;
  /**
   * The digest's field-level answer for a resource, scope resolved from the
   * descriptor like `can` (a domain-scoped resource with no domain answers
   * undefined) — see ResourceHandleBase.grantedFields.
   */
  grantedFields(permission: Permission, target: Resource, domain?: Domain): readonly string[] | undefined;
  /** Builds a handle for a resource outside the generated descriptor (a manually registered one). */
  define<Row, Key extends unknown[], Ops extends ResourceOperation, Create = never, Patch = never>(
    descriptor: ResourceDescriptor,
    domain?: Domain,
  ): ResourceHandle<Row, Key, Ops, Create, Patch>;
}

/**
 * The client for one generated API: global handles on the root, domain-scoped handles
 * under `domain(...)`. The generated `Api` type fills in G and D.
 */
export type Client<G, D> = ClientBase & G & { domain(domain: Domain | string): DomainClient<D> };

export function createClient<G, D>(descriptor: ApiDescriptor, options: ClientOptions): Client<G, D> {
  const baseUrl = options.baseUrl.replace(/\/+$/, '');
  const transport = options.transport ?? fetchTransport();
  const serverRelease = new Store<string | undefined>(undefined);
  const requestResponse = createRequester(baseUrl, transport, {
    onError: options.onError,
    apiVersion: options.apiVersion,
    olderServerRetryDelays: options.olderServerRetryDelays,
    timeZone: options.timeZone,
    serverRelease,
  });
  const request: Requester = async <T>(method: HttpMethod, path: string, requestOptions?: RequestOptions): Promise<T> =>
    (await requestResponse<T>(method, path, requestOptions)).body;
  const permissions = new PermissionStore(request, {
    digest: descriptor.permissionDigestRoute,
    domains: descriptor.userDomainsRoute,
  });
  const warn = warnOnce(options.warn);
  const live = new LiveSession({ ...options.live, request, baseUrl, routes: descriptor.live, warn });
  const features = new FeatureState({
    request,
    route: descriptor.features?.route,
    setFeature: descriptor.methods[setFeatureMethod],
    permissions,
    flags: () => listFlags(base),
    warn,
  });

  const auth = new AuthClient({ request: requestResponse, route: loginMethodsRoute, warn });

  const base: ClientBase = {
    descriptor,
    baseUrl,
    permissions,
    live,
    features,
    auth,
    serverRelease,
    warn,
    request,
    requestResponse,
    batch: (operations) => batch(request, descriptor, operations),
    can: (permission, target, domain) => {
      const scope =
        descriptor.resources[target]?.scope ?? descriptor.methods[target]?.scope ?? (domain ? 'domain' : 'global');
      if (scope === 'domain' && !domain) {
        return false;
      }
      return permissions.can({ resource: target, permission, domain: scope === 'domain' ? domain : undefined });
    },
    grantedFields: (permission, target, domain) => {
      const scope = descriptor.resources[target]?.scope ?? (domain ? 'domain' : 'global');
      if (scope === 'domain' && !domain) {
        return undefined;
      }
      return definedFields(permissions, {
        resource: target,
        permission,
        domain: scope === 'domain' ? domain : undefined,
      });
    },
    define: (resource, domain) => createResourceHandle(base, resource, domain) as never,
  };

  const domains = new Map<string, DomainClient<D>>();
  const client = Object.assign(base, {
    domain: (domain: Domain | string): DomainClient<D> => {
      let bound = domains.get(domain);
      if (!bound) {
        bound = createDomainClient<D>(base, domain as Domain);
        domains.set(domain, bound);
      }
      return bound;
    },
  }) as Client<G, D>;

  attachHandles(client, base, 'global', undefined);
  return client;
}

function createDomainClient<D>(client: ClientBase, domain: Domain): DomainClient<D> {
  const bound: DomainClientBase = {
    domain,
    can: (permission, target) => client.can(permission, target, domain),
  };
  attachHandles(bound, client, 'domain', domain);
  return bound as DomainClient<D>;
}

function attachHandles(
  target: object,
  client: ClientBase,
  scope: 'global' | 'domain',
  domain: Domain | undefined,
): void {
  const handles = target as Record<string, unknown>;
  for (const resource of Object.values(client.descriptor.resources)) {
    if (resource.scope === scope) {
      handles[resource.property] = createResourceHandle(client, resource, domain);
    }
  }
  for (const method of Object.values(client.descriptor.methods)) {
    if (method.scope === scope) {
      handles[method.property] = createMethodHandle(client, method, domain);
    }
  }
}

/**
 * Every row of the feature flags resource, by name: read whole where the resource
 * declares no maximum page size, else one server page at a time to the end (a few dozen
 * flags at most, so the walk is short). An API whose descriptor has no such resource
 * (one generated before flags existed, or written by hand) is refused naming it.
 */
async function listFlags(client: ClientBase): Promise<FeatureFlag[]> {
  const descriptor = client.descriptor.resources[featureFlagsResource];
  if (!descriptor) {
    throw new Error(`this API declares no ${featureFlagsResource} resource; feature flags cannot be listed from here`);
  }
  const handle = createResourceHandle<FeatureFlag, [string]>(client, descriptor, undefined);
  const sort = { field: 'name' as const };
  if (readMode(descriptor) === 'whole') {
    return handle.list(wholeListQuery<FeatureFlag>(descriptor, { sort }));
  }
  const rows: FeatureFlag[] = [];
  let page = await handle.page({ sort, limit: descriptor.page?.max });
  rows.push(...page.rows);
  while (page.next) {
    page = await page.next();
    rows.push(...page.rows);
  }
  return rows;
}

/** The digest's field-level enumeration for a scope, as sorted names — undefined when it holds none. */
function definedFields(permissions: PermissionStore, scope: PermissionScope): readonly string[] | undefined {
  const fields = Object.keys(permissions.fieldStates(scope)).sort();
  return fields.length > 0 ? fields : undefined;
}

/** What createRequester takes beside the transport: the error hook and the release header's settings. */
interface RequesterOptions {
  onError?: (error: ApiError) => void;
  apiVersion?: string;
  olderServerRetryDelays?: readonly number[];
  /** The person's time zone to send on every request; absent, the browser's own. */
  timeZone?: string;
  /** Where the release each answer names is kept, when the caller keeps it. */
  serverRelease?: Store<string | undefined>;
}

/**
 * Issues every request of the client. A build that names a release sends it in the
 * version header. A 412 that carries the server's release in that header is the server's
 * refusal of this build and is judged before the caller's declared answers: when the
 * server's release is older than this build's, the request is retried after each pause in
 * `olderServerRetryDelays` (a deploy still shifting traffic answers the next try from the
 * new instance); when the retries run out, or when this build is the older one, the
 * refusal is thrown as ApiVersionError, observed by the hook first. Every answer that names
 * the server's release in the version header, refusals included, is kept in `serverRelease`
 * before it is judged. A 503 that carries the
 * maintenance marker is the server down for maintenance and is judged the same way, before
 * the caller's declared answers, and thrown as MaintenanceError with the server's
 * Retry-After; a 503 without the marker is an ordinary error. Every other 4xx or 5xx the
 * caller did not declare becomes ApiError.
 */
function createRequester(baseUrl: string, transport: Transport, requester: RequesterOptions): ResponseRequester {
  const { onError } = requester;
  const version = releaseVersion(requester.apiVersion);
  const zone = timeZoneToSend(requester.timeZone);
  const retryDelays = requester.olderServerRetryDelays ?? olderServerRetryDelays;
  return async <T>(method: HttpMethod, path: string, options?: RequestOptions): Promise<ClientResponse<T>> => {
    const query = options?.query?.toString();
    const url = options?.absolute ? resolveAbsolute(baseUrl, path) : `${baseUrl}/${path}${query ? `?${query}` : ''}`;
    const headers = requestHeaders(options?.headers, version, zone);
    let retries = 0;
    for (;;) {
      const response = await transport({ method, url, body: options?.body, headers });
      const named = response.headers?.[apiVersionHeader.toLowerCase()];
      if (named !== undefined && named !== '') {
        requester.serverRelease?.set(named);
      }
      const serverVersion = versionRefusal(response);
      if (serverVersion !== undefined) {
        const serverOlder = version !== undefined && compareReleases(serverVersion, version) < 0;
        if (serverOlder && retries < retryDelays.length) {
          await pause(retryDelays[retries++]);
          continue;
        }
        const error = new ApiVersionError(method, url, response.body, serverVersion, version ?? '', serverOlder);
        onError?.(error);
        throw error;
      }
      if (maintenanceAnswer(response)) {
        const retryAfter = retryAfterSeconds(response.headers?.[retryAfterHeader.toLowerCase()]);
        const error = new MaintenanceError(method, url, response.body, retryAfter);
        onError?.(error);
        throw error;
      }
      if (response.status >= 400 && !options?.accept?.includes(response.status)) {
        const error = new ApiError(method, url, response.status, response.body);
        onError?.(error);
        throw error;
      }
      return { status: response.status, body: response.body as T, headers: response.headers ?? {} };
    }
  };
}

/** The server's release when the response is its refusal of this build's release (a 412 carrying the version header), else undefined. */
function versionRefusal(response: TransportResponse): string | undefined {
  if (response.status !== 412 || !response.headers) {
    return undefined;
  }
  const serverVersion = response.headers[apiVersionHeader.toLowerCase()];
  return serverVersion === undefined || serverVersion === '' ? undefined : serverVersion;
}

/** Whether the response is the server's answer while down for maintenance: a 503 carrying the maintenance marker. */
function maintenanceAnswer(response: TransportResponse): boolean {
  return response.status === 503 && response.headers?.[maintenanceHeader.toLowerCase()] === maintenanceHeaderValue;
}

function pause(milliseconds: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

/**
 * Resolves a URL reference the server issued. The server writes path-absolute
 * references carrying the API prefix, so under a baseUrl with an origin they are
 * joined to that origin; a same-origin baseUrl passes them through, and a complete
 * URL is used as given.
 */
export function resolveAbsolute(baseUrl: string, reference: string): string {
  if (/^https?:\/\//i.test(reference)) {
    return reference;
  }
  const origin = /^https?:\/\/[^/]+/i.exec(baseUrl);
  return origin && reference.startsWith('/') ? origin[0] + reference : reference;
}

async function batch(request: Requester, descriptor: ApiDescriptor, operations: Operation[]): Promise<BatchResult> {
  const endpoints = new Set(operations.map((operation) => operationRoute(operation) ?? descriptor.consolidatedRoute));
  if (endpoints.size > 1) {
    throw new Error(
      `operations for more than one endpoint (${[...endpoints].map(String).join(', ')}) cannot commit as one transaction: ` +
        'a standalone resource has its own PATCH route; send its operations in their own batch',
    );
  }
  const [endpoint] = endpoints;
  if (operations.length === 0 || !endpoint) {
    throw new Error('this API has no consolidated endpoint; mutate resources individually');
  }
  // The binding is the client's own bookkeeping; the wire carries the operation alone.
  const body = operations.map(({ op, path, value }) => (value === undefined ? { op, path } : { op, path, value }));
  return (await request<BatchResult | null>('PATCH', endpoint, { body })) ?? {};
}

/** The route of a resource or method in a scope: the domain pair is prepended for domain-scoped targets. */
function scopedRoute(
  descriptor: ApiDescriptor,
  route: string,
  scope: 'global' | 'domain',
  domain: Domain | undefined,
): string {
  if (scope !== 'domain') {
    return route;
  }
  if (!domain) {
    throw new Error(`${route} is domain-scoped: bind a domain with client.domain(...) first`);
  }
  if (!descriptor.domainRoute) {
    throw new Error(`${route} is domain-scoped but the API declares no domain route`);
  }
  return `${descriptor.domainRoute.segment}/${encodeURIComponent(domain)}/${route}`;
}

function keySegments(key: readonly unknown[] | undefined): string {
  return key && key.length > 0 ? `/${key.map((part) => encodeURIComponent(String(part))).join('/')}` : '';
}

function createResourceHandle<Row extends object, Key extends unknown[]>(
  client: ClientBase,
  descriptor: ResourceDescriptor,
  domain: Domain | undefined,
): AnyResourceHandle<Row, Key> {
  const api = client.descriptor;
  const route = scopedRoute(api, descriptor.route, descriptor.scope, domain);
  const scopeDomain = descriptor.scope === 'domain' ? domain : undefined;
  const digestScope = (permission: Permission, target: string = descriptor.resource) => ({
    resource: target as Resource,
    permission,
    domain: scopeDomain,
  });

  const path = (key?: readonly unknown[]): string => {
    const segments = keySegments(key);
    return descriptor.consolidated ? `/${route}${segments}` : segments || '/';
  };
  // A key-less resource is served whole on every request and refuses a limit or a
  // cursor, so neither is sent for it, whatever a caller typed past the type; the rest
  // of the query goes as given.
  const keyless = descriptor.keys.length === 0;
  const wholeList = (query: ListQuery<Row> | undefined): ListQuery<Row> | undefined => {
    if (!keyless || !query) {
      return query;
    }
    const rest = { ...query };
    delete rest.limit;
    delete rest.cursor;
    return rest;
  };
  // A standalone resource's operations carry their route, so client.batch delivers
  // them to the resource's own PATCH handler; a consolidated one's carry nothing.
  const bound = (operation: Operation): Operation =>
    descriptor.consolidated ? operation : { ...operation, [OperationRoute]: route };
  const mutate = async (operation: Operation): Promise<BatchResult> => {
    if (descriptor.consolidated && !api.consolidatedRoute) {
      throw new Error(`${descriptor.resource} is consolidated but the API declares no consolidated route`);
    }
    return client.batch([operation]);
  };

  const ops = {
    add: (value: Record<string, unknown>): Operation => {
      const body = { ...value };
      const key: unknown[] = [];
      for (const field of descriptor.keys) {
        if (body[field] !== undefined) {
          key.push(body[field]);
          delete body[field];
        }
      }
      // A partial compound key would address the wrong row: either every key field is
      // supplied (client-assigned keys) or none is (server-generated key).
      if (key.length !== 0 && key.length !== descriptor.keys.length) {
        throw new Error(
          `${descriptor.resource}: a create must supply every key field (${descriptor.keys.join(', ')}) or none`,
        );
      }
      return bound({ op: 'add', path: path(key), value: body });
    },
    patch: (key: Key, value: Record<string, unknown>): Operation => bound({ op: 'patch', path: path(key), value }),
    remove: (key: Key): Operation => bound({ op: 'remove', path: path(key) }),
  };

  const handle: AnyResourceHandle<Row, Key> = {
    resource: descriptor.resource,
    descriptor,
    domain: scopeDomain,
    url: (key?: Key) => `${client.baseUrl}/${route}${keySegments(key)}`,
    fileUrl: (key: Key, segment = 'content') => {
      if (!descriptor.files?.includes(segment)) {
        throw new Error(`${descriptor.resource} serves no file under ${segment}: its files are [${descriptor.files?.join(', ') ?? ''}]`);
      }
      return `${client.baseUrl}/${route}${keySegments(key)}/${segment}`;
    },
    path,
    keyOf: (row: Row) => descriptor.keys.map((field) => (row as Record<string, unknown>)[field]) as Key,
    subscription: (key?: Key) => liveSubscription(descriptor, scopeDomain, key),
    can: (permission) => client.permissions.can(digestScope(permission)),
    state: (permission) => client.permissions.state(digestScope(permission)),
    grantedFields: (permission) => definedFields(client.permissions, digestScope(permission)),
    rowCan: (row, permission, target) => {
      const envelope = rowCapabilities(row);
      if (permission === 'Execute') {
        if (!target) {
          throw new Error(`${descriptor.resource}: rowCan(row, 'Execute', method) requires the method name`);
        }
        if (envelope) {
          return envelope.Execute?.includes(target) ?? false;
        }
        return client.permissions.can(digestScope(ExecutePermission, target));
      }
      if (permission === 'Create') {
        if (!target) {
          throw new Error(`${descriptor.resource}: rowCan(row, 'Create', member) requires the member resource name`);
        }
        if (envelope) {
          return envelope.Create?.includes(target) ?? false;
        }
        return client.permissions.can(digestScope(CreatePermission, target));
      }
      if (envelope) {
        return permission === 'Update' ? (envelope.Update?.length ?? 0) > 0 : envelope.Delete === true;
      }
      return client.permissions.can(digestScope(permission === 'Update' ? UpdatePermission : DeletePermission));
    },
    fieldEditable: (row, field) => {
      const envelope = rowCapabilities(row);
      if (envelope) {
        return envelope.Update?.includes(field) ?? false;
      }
      return (
        client.permissions.can(digestScope(UpdatePermission, `${descriptor.resource}.${field}`)) ||
        client.permissions.can(digestScope(UpdatePermission))
      );
    },
    list: (async (query?: ListQuery<Row>) => {
      const { method, params, body } = listRequest(wholeList(query));
      const wanted = query?.live ? liveSubscription(descriptor, scopeDomain) : undefined;
      return (
        (await client.request<Row[] | null>(method, route, { ...liveRequest(client.live, wanted, params), body })) ?? []
      );
    }) as Listable<Row>['list'],
    page: ((query?: ListQuery<Row>) => {
      const { method, params, body } = listRequest(wholeList(query));
      const wanted = query?.live ? liveSubscription(descriptor, scopeDomain) : undefined;
      // The live parts are read when each request is made, not when the page was asked
      // for, so a reload and a turn carry the version current at that moment.
      return pageOf<Row>(
        client,
        method,
        body,
        () => client.requestResponse(method, route, { ...liveRequest(client.live, wanted, params), body }),
        wanted,
      );
    }) as Listable<Row>['page'],
    read: (async (key: Key, options?: ReadOptions<Row>) => {
      const wanted = options?.live ? liveSubscription(descriptor, scopeDomain, key) : undefined;
      return client.request<Row>(
        'GET',
        `${route}${keySegments(key)}`,
        liveRequest(client.live, wanted, readSearchParams(options)),
      );
    }) as Readable<Row, Key>['read'],
    create: async (value) => {
      const result = await mutate(ops.add(value));
      // A consolidated route keys the new keys by resource; a standalone route answers them
      // under `ids`, and under `iDs` on a server from before the key changed.
      return result[descriptor.property]?.[0] ?? result['ids']?.[0] ?? result['iDs']?.[0];
    },
    patch: async (key, value) => {
      await mutate(ops.patch(key, value));
    },
    remove: async (key) => {
      await mutate(ops.remove(key));
    },
    ops,
  };
  return handle;
}

/**
 * The request a list query makes: a GET with every parameter in the URL, or, for a
 * sensitive filter, a POST carrying the filter in the body and the rest in the URL.
 */
function listRequest<Row>(query: ListQuery<Row> | undefined): {
  method: HttpMethod;
  params: URLSearchParams;
  body: { filter: string } | undefined;
} {
  const params = listSearchParams(query);
  if (!query?.sensitiveFilter || !params.has('filter')) {
    return { method: 'GET', params, body: undefined };
  }
  const filter = params.get('filter') ?? '';
  params.delete('filter');
  return { method: 'POST', params, body: { filter } };
}

/**
 * The query and headers of one request as it is made. A request the caller asked to be
 * live adds the tab's subscribe header and the version parameter while the live session
 * is active — read now, so a repeat of the request carries the version current at the
 * repeat — and is the plain request otherwise; on an API that serves no live
 * subscriptions the session announces the plain answer once.
 */
function liveRequest(
  live: LiveSession,
  wanted: LiveSubscription | undefined,
  params: URLSearchParams,
): Pick<RequestOptions, 'query' | 'headers'> {
  if (!wanted || !live.requested()) {
    return { query: params };
  }
  const query = new URLSearchParams(params);
  query.set(versionParam, live.version(wanted));
  return { query, headers: { [subscribeHeader]: live.tab } };
}

/**
 * Reads one list response into a Page, with `next` and `prev` following the Link
 * relations as issued — by the same method and with the same body, so a filter carried
 * in the body travels with the walk — and `reload` repeating the request that produced
 * the page. A live page's relations carry the subscribe header and the version too: the
 * list subscription is the resource in the domain, whatever page of it is open.
 */
async function pageOf<Row>(
  client: ClientBase,
  method: HttpMethod,
  body: unknown,
  request: () => Promise<ClientResponse<Row[] | null>>,
  wanted?: LiveSubscription,
): Promise<Page<Row>> {
  const { body: rows, headers } = await request();
  const relations = parseLinkHeader(headers[LinkHeader]);
  const total = headers[TotalCountHeader];
  const follow = (reference: string) => () =>
    pageOf<Row>(
      client,
      method,
      body,
      () => {
        const live = wanted && client.live.requested();
        const url = live ? versionedUrl(reference, client.live.version(wanted)) : reference;
        return client.requestResponse<Row[] | null>(method, url, {
          absolute: true,
          body,
          headers: live ? { [subscribeHeader]: client.live.tab } : undefined,
        });
      },
      wanted,
    );
  return {
    rows: rows ?? [],
    total: total === undefined ? undefined : Number(total),
    next: relations['next'] ? follow(relations['next']) : undefined,
    prev: relations['prev'] ? follow(relations['prev']) : undefined,
    reload: () => pageOf<Row>(client, method, body, request, wanted),
  };
}

function createMethodHandle<Body, Result = void>(
  client: ClientBase,
  descriptor: MethodDescriptor,
  domain: Domain | undefined,
): MethodHandle<Body, Result> {
  const route = scopedRoute(client.descriptor, descriptor.route, descriptor.scope, domain);
  const scope = {
    resource: descriptor.method,
    permission: ExecutePermission,
    domain: descriptor.scope === 'domain' ? domain : undefined,
  };
  // Posts one body — JSON, or the multipart form of an upload — and resolves with
  // the method's answer. A method with declared statuses resolves every declared
  // code as { status, result }; the rest resolve the result or nothing.
  const post = async (body: unknown, headers?: Record<string, string>): Promise<Result> => {
    if (descriptor.statuses) {
      const response = await client.requestResponse<unknown>('POST', route, {
        body,
        headers,
        accept: descriptor.statuses,
      });
      if (!descriptor.answers) {
        // An answerless method declaring 204: nothing to pair.
        return undefined as Result;
      }
      const answer: MethodAnswer = { status: response.status, result: response.body ?? undefined };
      return answer as Result;
    }
    // A method without an answer serves an empty 200; the transport decodes that
    // to undefined, which is the void the handle promises.
    const answer = await client.request<Result | null | undefined>('POST', route, { body, headers });
    return (answer ?? undefined) as Result;
  };
  const handle: MethodHandle<Body, Result> = {
    method: descriptor.method,
    descriptor,
    domain: scope.domain,
    url: () => `${client.baseUrl}/${route}`,
    execute: (body) => post(body),
    dryRun: async (body) => {
      await client.request<unknown>('POST', route, { body, headers: { [dryRunHeader]: 'true' } });
    },
    can: () => client.permissions.can(scope),
    state: () => client.permissions.state(scope),
  };
  if (!descriptor.upload) {
    return handle;
  }
  const maxBytes = descriptor.upload.maxBytes;
  const upload: UploadMethodHandle<Body, Result> = {
    ...handle,
    upload: (body, files) => {
      const total = files.reduce((sum, file) => sum + file.size, 0);
      if (total > maxBytes) {
        throw new ApiError(
          'POST',
          handle.url(),
          413,
          `the upload exceeds the declared maximum of ${formatByteSize(maxBytes)}`,
        );
      }
      const form = new FormData();
      form.append(uploadRequestPart, new Blob([JSON.stringify(body)], { type: 'application/json' }));
      for (const file of files) {
        if (file instanceof File) {
          form.append(uploadFilePart, file, file.name);
        } else {
          form.append(uploadFilePart, file);
        }
      }
      return post(form);
    },
  };
  return upload;
}
