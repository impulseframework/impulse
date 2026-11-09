import { Domain, PermissionScope } from './brands';
import { PermissionDigest, PermissionDigestState } from './digest';
import { Store } from './store';
import { HttpMethod } from './transport';

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

/** The permission cache at one instant: digests by scope key, and the user's domains. */
export interface PermissionsSnapshot {
  readonly digests: ReadonlyMap<string, PermissionDigest>;
  readonly domains: readonly Domain[];
}

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
 * PermissionStore owns the session user's digest cache. One digest per scope (global,
 * or one per domain), loaded on demand and cached for the session; `can` answers
 * synchronously from the cache and never fetches. `granted` and `conditional` both
 * answer true: render the surface, and let the server narrow it per row. A scope that
 * has not been loaded answers false — fail closed — so load the digest for a domain
 * before rendering its pages.
 */
export class PermissionStore {
  readonly snapshot = new Store<PermissionsSnapshot>({ digests: new Map(), domains: [] });
  private inflight = new Map<string, Promise<PermissionDigest>>();

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

  /** The domains where the user holds at least one grant, once loadDomains has run. */
  domains(): readonly Domain[] {
    return this.snapshot.get().domains;
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
   * Loads (or reloads) one scope's digest. Concurrent calls for the same scope share
   * one request. A failed load caches an empty digest for the scope — every question
   * answers false — and rethrows, so callers see the failure.
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
      .then((digest) => digest ?? {})
      .catch((error: unknown) => {
        this.setDigest(key, {});
        throw error;
      })
      .then((digest) => {
        this.setDigest(key, digest);
        return digest;
      })
      .finally(() => {
        this.inflight.delete(key);
      });
    this.inflight.set(key, load);
    return load;
  }

  /** Loads the user's domain list from the generated user-domains endpoint. */
  async loadDomains(): Promise<readonly Domain[]> {
    const domains = (await this.request<Domain[] | null>('GET', this.routes.domains)) ?? [];
    this.snapshot.update((snapshot) => ({ ...snapshot, domains }));
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
