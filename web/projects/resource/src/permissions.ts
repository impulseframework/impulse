import { Domain, PermissionScope } from './brands';
import { PermissionDigest, PermissionDigestState } from './digest';
import { Store } from './store';
import { ApiError, HttpMethod } from './transport';

/** Issues one request against the API; the client supplies it. */
export type Requester = <T>(method: HttpMethod, path: string, options?: RequestOptions) => Promise<T>;

/** A response as a handle reads it: the status, the decoded body, and the headers by lower-cased name. */
export interface ClientResponse<T> {
  status: number;
  body: T;
  headers: Record<string, string>;
}

/** Like Requester, resolving with the headers too; paged lists read Link and Total-Count. */
export type ResponseRequester = <T>(
  method: HttpMethod,
  path: string,
  options?: RequestOptions,
) => Promise<ClientResponse<T>>;

export interface RequestOptions {
  query?: URLSearchParams;
  body?: unknown;
  /** Extra request headers, such as the dry-run header a method handle sends. */
  headers?: Record<string, string>;
  /**
   * Statuses at or above 400 the requester resolves rather than throws: a method's
   * declared answers. Every other 4xx or 5xx is still an ApiError.
   */
  accept?: readonly number[];
  /**
   * The path is a complete URL reference the server handed out (a Link relation),
   * carrying the API prefix already: it is resolved against baseUrl's origin, never
   * appended under it.
   */
  absolute?: boolean;
}

/** The permission cache at one instant: digests by scope key, and the user's domains with their names. */
export interface PermissionsSnapshot {
  readonly digests: ReadonlyMap<string, PermissionDigest>;
  /** The domains the session reaches: the tenants it may enter. */
  readonly domains: readonly Domain[];
  /** Each listed domain's name, as the tenant list answers it; absent until loadDomains has run. */
  readonly names?: ReadonlyMap<Domain, string>;
  /**
   * The domains the person holds a seat in and this session cannot enter, each with the
   * server's reason; absent until loadDomains has run, and empty when none is closed.
   */
  readonly closed?: ReadonlyMap<Domain, string>;
}

/**
 * One tenant of the user-domains answer: the domain and its name (the domain itself
 * where the record holds none). `closed` marks a tenant the person holds a seat in and
 * this session cannot enter, with the reason in a sentence the server writes ("Acme
 * admits only people who sign in through its own directory. You signed in with a
 * password."); it is absent for a tenant the session reaches.
 */
export interface UserDomain {
  readonly domain: Domain;
  readonly name: string;
  readonly closed?: string;
}

/** No domain is closed: what closedDomains answers before the list has loaded. */
const noneClosed: ReadonlyMap<Domain, string> = new Map();

/** The digest cache key for a scope: the domain, or '' for the global scope. */
export function digestKey(domain?: Domain): string {
  return domain ?? '';
}

/** Answers one permission question from a snapshot; undefined when the target is absent or the scope not loaded. */
export function permissionState(
  snapshot: PermissionsSnapshot,
  scope: PermissionScope,
): PermissionDigestState | undefined {
  return snapshot.digests.get(digestKey(scope.domain))?.[scope.resource]?.[scope.permission];
}

/**
 * The digest's field-level entries for one resource and permission: every JSON field
 * name whose dotted `Resource.field` target carries the permission in the scope's
 * digest, mapped to its state. Granted and conditional both appear — conditional means
 * render the input and let the server judge the write. A denied field is absent, so
 * consumers fail closed per field. An empty record means the digest carries no
 * field-level entries at all for this resource and permission: the permission is
 * denied outright (the base entry is absent too) or the resource has no grant-bearing
 * fields (a keys-only resource) — ask the base entry to tell them apart.
 */
export function fieldPermissionStates(
  snapshot: PermissionsSnapshot,
  scope: PermissionScope,
): Record<string, PermissionDigestState> {
  const states: Record<string, PermissionDigestState> = {};
  const digest = snapshot.digests.get(digestKey(scope.domain));
  if (!digest) {
    return states;
  }
  const prefix = `${scope.resource}.`;
  for (const [target, permissions] of Object.entries(digest)) {
    if (!target.startsWith(prefix)) {
      continue;
    }
    const state = permissions?.[scope.permission];
    if (state !== undefined) {
      states[target.slice(prefix.length)] = state;
    }
  }
  return states;
}

/**
 * How a read of a digest failed. `refused` is the server's refusal, its 403 or 404: the
 * scope is not there for this session, and asking again will not change that. `failed` is
 * everything else, a 500 or a request that never reached the server: nothing is known of
 * what the person holds, and the read can be tried again. Neither is a person who holds
 * nothing, which is an answer: a digest with no entries.
 */
export type DigestFailure = 'refused' | 'failed';

/** digestFailure says which of the two a failed digest read was, from what the read threw. */
export function digestFailure(error: unknown): DigestFailure {
  return error instanceof ApiError && (error.status === 403 || error.status === 404) ? 'refused' : 'failed';
}

/**
 * What an `onUnread` listener is called with: the scope's domain (undefined for the global
 * scope) and how its read failed.
 */
export type UnreadListener = (domain: Domain | undefined, failure: DigestFailure) => void;

/**
 * PermissionStore owns the session user's digest cache. One digest per scope (global,
 * or one per domain), loaded on demand and cached for the session; `can` answers
 * synchronously from the cache and never fetches. `granted` and `conditional` both
 * answer true: render the surface, and let the server narrow it per row. A scope that
 * has not been loaded answers false — fail closed — so load the digest for a domain
 * before rendering its pages.
 *
 * A read that fails is not an answer, and the cache never holds one as if it were: a
 * scope whose first read fails stays unloaded (`hasDigest` is false: nothing is known),
 * and a scope read again keeps its last answer when the new read fails. The failure goes
 * to whoever asked (`loadDigest` rejects) and to every `onUnread` listener, so a page
 * drawn from the scope can say that it could not be read.
 */
export class PermissionStore {
  readonly snapshot = new Store<PermissionsSnapshot>({ digests: new Map(), domains: [] });
  private inflight = new Map<string, Promise<PermissionDigest>>();
  private unreadListeners = new Set<UnreadListener>();

  constructor(
    private request: Requester,
    private routes: { digest: string; domains: string },
  ) {}

  get(): PermissionsSnapshot {
    return this.snapshot.get();
  }

  subscribe(listener: (snapshot: PermissionsSnapshot) => void): () => void {
    return this.snapshot.subscribe(listener);
  }

  /** The domains where the user holds at least one grant and that the session reaches, once loadDomains has run. */
  domains(): readonly Domain[] {
    return this.snapshot.get().domains;
  }

  /**
   * The domains the person holds a seat in and this session cannot enter, each with the
   * server's reason, once loadDomains has run: a tenant that admits only people who sign
   * in through its own directory, when the person signed in another way. They are never
   * among domains(): the tenant's routes answer the session as a tenant that does not
   * exist, and a picker shows them marked, not as places to open.
   */
  closedDomains(): ReadonlyMap<Domain, string> {
    return this.snapshot.get().closed ?? noneClosed;
  }

  /**
   * A listed domain's name, as the tenant record holds it, for a picker to show; the
   * domain itself for one the list does not name or before loadDomains has run.
   */
  domainName(domain: Domain): string {
    return this.snapshot.get().names?.get(domain) ?? domain;
  }

  digest(domain?: Domain): PermissionDigest | undefined {
    return this.snapshot.get().digests.get(digestKey(domain));
  }

  hasDigest(domain?: Domain): boolean {
    return this.snapshot.get().digests.has(digestKey(domain));
  }

  state(scope: PermissionScope): PermissionDigestState | undefined {
    return permissionState(this.snapshot.get(), scope);
  }

  /** The cached digest's field-level entries for one resource and permission — see fieldPermissionStates. */
  fieldStates(scope: PermissionScope): Record<string, PermissionDigestState> {
    return fieldPermissionStates(this.snapshot.get(), scope);
  }

  can(scope: PermissionScope): boolean {
    return this.state(scope) !== undefined;
  }

  /**
   * onUnread calls the listener each time a read of a digest fails, with the scope's
   * domain and how the read failed, before the read's own caller sees the rejection. The
   * function it returns stops the calls.
   */
  onUnread(listener: UnreadListener): () => void {
    this.unreadListeners.add(listener);
    return () => {
      this.unreadListeners.delete(listener);
    };
  }

  /**
   * Loads (or reloads) one scope's digest. Concurrent calls for the same scope share
   * one request. A read that fails leaves the cache as it was (a scope never read stays
   * unloaded, a scope read before keeps its last answer), tells the `onUnread` listeners
   * and rethrows, so callers see the failure; `digestFailure` says of what was thrown
   * whether the server refused the read.
   */
  loadDigest(domain?: Domain): Promise<PermissionDigest> {
    const key = digestKey(domain);
    const pending = this.inflight.get(key);
    if (pending) {
      return pending;
    }
    const query = new URLSearchParams();
    if (domain) {
      query.set('domain', domain);
    }
    const load = this.request<PermissionDigest | null>('GET', this.routes.digest, { query })
      .finally(() => {
        this.inflight.delete(key);
      })
      .then(
        (digest) => {
          const read = digest ?? {};
          this.setDigest(key, read);
          return read;
        },
        (error: unknown) => {
          const failure = digestFailure(error);
          for (const listener of this.unreadListeners) {
            listener(domain, failure);
          }
          throw error;
        },
      );
    this.inflight.set(key, load);
    return load;
  }

  /** Loads the user's domain list, each domain with its name, from the generated user-domains endpoint. */
  async loadDomains(): Promise<readonly Domain[]> {
    const listed = (await this.request<UserDomain[] | null>('GET', this.routes.domains)) ?? [];
    const domains = listed.filter((entry) => !entry.closed).map((entry) => entry.domain);
    const names = new Map(listed.map((entry) => [entry.domain, entry.name] as const));
    const closed = new Map(
      listed.filter((entry) => entry.closed).map((entry) => [entry.domain, entry.closed ?? ''] as const),
    );
    this.snapshot.update((snapshot) => ({ ...snapshot, domains, names, closed }));
    return domains;
  }

  /** Answers a question, loading the scope's digest first if it is not cached. */
  async ensure(scope: PermissionScope): Promise<boolean> {
    if (!this.hasDigest(scope.domain)) {
      await this.loadDigest(scope.domain);
    }
    return this.can(scope);
  }

  /** Reloads the domain list and every cached digest — after a role change, say. */
  async refresh(): Promise<void> {
    const scopes = [...this.snapshot.get().digests.keys()];
    await Promise.all([
      this.loadDomains(),
      ...scopes.map((key) => this.loadDigest(key === '' ? undefined : (key as Domain))),
    ]);
  }

  /** Forgets everything — on logout. */
  clear(): void {
    this.snapshot.set({ digests: new Map(), domains: [] });
  }

  private setDigest(key: string, digest: PermissionDigest): void {
    this.snapshot.update((snapshot) => ({ ...snapshot, digests: new Map(snapshot.digests).set(key, digest) }));
  }
}
