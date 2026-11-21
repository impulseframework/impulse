import { computed, inject, Injectable, signal } from '@angular/core';
import { RESOURCE_CLIENT, storeSignal } from '@impulseframework/resource-angular/resource-client';
import {
  CHANGE_FEED,
  Domain,
  FRONTEND_LOGIN_PATH,
  ListAcrossTenantsPermission,
  LOGIN_REDIRECT_URL,
  LOGOUT_ACTION,
  Method,
  PermissionDigest,
  PermissionDigestState,
  PermissionScope,
  Resource,
  RESOURCE_DOMAIN,
  SESSION_PATH,
  SessionInfo,
} from '@impulseframework/resource-angular/types';
import {
  ClientBase,
  digestFailure,
  digestKey,
  domainsKnown,
  domainsUnread,
  FeatureState,
  fieldPermissionStates,
  permissionState,
  PermissionStore,
} from '@impulseframework/resource';
import { from, map, Observable, of, switchMap, tap } from 'rxjs';

/**
 * Session and permission state for the application.
 *
 * Permissions are owned by a @impulseframework/resource PermissionStore: the application's
 * client's (RESOURCE_CLIENT, provided through provideResourceClient; an application
 * that provides none fails at startup naming the way in). One cache serves the app's
 * own pages and the library's guard, directive, and forms. The store holds the per-scope
 * permission digest (the session user's structural grant enumeration — granted,
 * conditional, or absent for denied) and the user's domains (every tenant where they
 * hold at least one grant — the tenant picker's source). On authentication the global
 * digest and the domains load once; a tenant's digest loads on demand (`loadDigest`)
 * and stays cached. Answers are advisory UI material — what to render — and fail
 * closed: nothing loaded means nothing permitted. Enforcement stays server-side.
 *
 * Live pages ride the same session: when the application provides a change feed
 * (CHANGE_FEED) and the client's API serves live subscriptions, the first authenticated
 * answer starts the client's live session with it, and a logout stops it (unsubscribing
 * everything and revoking the feed's identity) before the session itself is logged out.
 *
 * Feature flags ride it too: the first authenticated answer loads the enabled set beside
 * the digest, `featureEnabled` answers from it for the `impulseFeature` directive, the
 * `featureMatch` route guard, and the application's own code, and a logout forgets it.
 * The set is loaded at sign-in and not polled: a flip by someone else is seen at the next
 * sign-in, while the person flipping, on the Feature flags page, has their own set and
 * digest refreshed once the write succeeds. An API that serves no feature flags (its
 * descriptor carries no features route) loads nothing: every flag answers off, so each
 * gate stays closed, and the first ask of each flag is announced through the client's
 * warn hook (the console by default) naming the regeneration that would serve it.
 */
@Injectable({
  providedIn: 'root',
})
export class AuthService {
  private loginUrl = inject(FRONTEND_LOGIN_PATH);
  private sessionUrl = inject(SESSION_PATH);
  private logoutAction = inject(LOGOUT_ACTION);
  /** The selected tenant, which a domain-scoped question naming no domain is asked in. */
  private domain = inject(RESOURCE_DOMAIN);

  /** The client every request of this service goes through: the application's. */
  private readonly client: ClientBase = inject(RESOURCE_CLIENT);

  /** The change feed live pages listen to, when the application provides one. */
  private readonly changeFeed = inject(CHANGE_FEED, { optional: true });

  /** The permission cache: the client's. */
  readonly permissions: PermissionStore = this.client.permissions;

  /** The enabled set of feature flags: the client's. */
  readonly features: FeatureState = this.client.features;

  private snapshot = storeSignal(this.permissions.snapshot);
  private featuresSnapshot = storeSignal(this.features.snapshot);
  private authenticatedSignal = signal(false);
  private sessionInfoSignal = signal({} as SessionInfo);
  private permissionsLoaded = false;

  // The scopes whose digest could not be read, by the digest's key: written when a read
  // fails, whoever asked for it, and dropped once the scope holds an answer.
  private unreadDigests = signal<ReadonlySet<string>>(new Set());

  constructor() {
    // A read of what the person holds outside every tenant that does not answer is kept
    // as unread whichever way it failed, since nothing stands in for it. A tenant's is
    // kept when the read failed; a tenant the server refuses is an answer, that the
    // tenant is not there for this session.
    this.permissions.onUnread((domain, failure) => {
      if (domain && failure === 'refused') {
        return;
      }
      this.unreadDigests.update((unread) => new Set(unread).add(digestKey(domain)));
    });
    this.permissions.subscribe((snapshot) => {
      const unread = this.unreadDigests();
      if ([...unread].some((key) => snapshot.digests.has(key))) {
        this.unreadDigests.set(new Set([...unread].filter((key) => !snapshot.digests.has(key))));
      }
    });
  }

  /**
   * The URL the browser returns to after the next login (LOGIN_REDIRECT_URL): the login
   * guard writes the route it turned away from, the client's error hook writes the
   * attempted URL when a 401 sends the browser to the login page, and the login page
   * reads it once and clears it.
   */
  redirectUrl = inject(LOGIN_REDIRECT_URL);
  authenticated = this.authenticatedSignal.asReadonly();
  sessionInfo = this.sessionInfoSignal.asReadonly();

  /**
   * The domains where the session user holds at least one grant, sorted — the tenant
   * picker's source. Empty until the session authenticates and the list is read
   * (`hasDomains`): an empty list that has not been read is not a person in no tenant.
   */
  domains = computed<readonly Domain[]>(() => this.snapshot().domains);

  /**
   * The domains the person holds a seat in and this session cannot enter, each with the
   * server's reason: a tenant that admits only people who sign in through its own
   * directory, when the person signed in another way. Never among `domains`; the tenant
   * picker shows them marked, with the reason. Empty until the session authenticates.
   */
  closedDomains = computed<ReadonlyMap<Domain, string>>(() => this.snapshot().closed ?? new Map<Domain, string>());

  /**
   * The names of the feature flags that are on in this environment, as loaded when the
   * session authenticated and reloaded after the session user flipped one. Empty until
   * the session authenticates.
   */
  enabledFeatures = computed<ReadonlySet<string>>(() => this.featuresSnapshot().enabled);

  /**
   * Whether the session user may exercise the scope's permission: true when the digest
   * for the scope's domain (global when omitted) carries the target as granted or
   * conditional — conditional means render the surface and expect the server to
   * narrow it. False when the target is absent or the digest is not loaded (fail
   * closed). Synchronous and signal-backed, so effects and computeds that call it
   * re-evaluate when a digest loads. No scope means no requirement.
   */
  hasPermission(scope?: PermissionScope): boolean {
    if (!scope) {
      return true;
    }
    return this.permissionState(scope) !== undefined;
  }

  /** The digest state for one scope: granted, conditional, or undefined when absent or not loaded. */
  permissionState(scope: PermissionScope): PermissionDigestState | undefined {
    return permissionState(this.snapshot(), this.scoped(scope));
  }

  /**
   * The scope with its domain settled. A scope naming a domain is asked as written. One
   * naming none is asked in the selected tenant (RESOURCE_DOMAIN) when the client's
   * descriptor places the target in the domain scope, else in the global digest; a
   * domain-scoped target with no tenant selected stays global-keyed and answers false,
   * since no domain digest holds it. Whether a resource may be listed across tenants
   * (ListAcrossTenantsPermission) is asked in the global digest whatever tenant is
   * selected or named: the server answers it there, for the session's every tenant.
   */
  private scoped(scope: PermissionScope): PermissionScope {
    if (scope.permission === ListAcrossTenantsPermission) {
      return { resource: scope.resource, permission: scope.permission };
    }
    if (scope.domain !== undefined) {
      return scope;
    }
    const { resources, methods } = this.client.descriptor;
    const kind = resources[scope.resource as Resource]?.scope ?? methods[scope.resource as Method]?.scope;
    if (kind !== 'domain') {
      return scope;
    }
    const domain = this.domain();
    return domain ? { ...scope, domain } : scope;
  }

  /**
   * The digest's field-level entries for one resource and permission: JSON field names
   * mapped to granted or conditional — a denied field is absent, and an empty record
   * means the digest carries no field information for the target (see
   * fieldPermissionStates in @impulseframework/resource). Signal-backed, so computeds that call
   * it re-evaluate when a digest loads.
   */
  fieldPermissionStates(scope: PermissionScope): Record<string, PermissionDigestState> {
    return fieldPermissionStates(this.snapshot(), this.scoped(scope));
  }

  /** Whether the digest for the domain (global when omitted) has been loaded. */
  hasDigest(domain?: Domain): boolean {
    return this.snapshot().digests.has(domain ?? '');
  }

  /**
   * Whether the digest for the domain (global when omitted) could not be read and nothing
   * is known of it: its last read failed, and the cache holds no answer from before. Not a
   * person who holds nothing, which is an answer: what is drawn from the scope says it
   * could not be loaded. False again once a read answers. Signal-backed.
   */
  digestUnread(domain?: Domain): boolean {
    return this.unreadDigests().has(digestKey(domain)) && !this.hasDigest(domain);
  }

  /**
   * Whether the person's list of tenants has been read. Until it has, an empty `domains()`
   * means nothing is known, not that the person is in no tenant. Signal-backed.
   */
  hasDomains(): boolean {
    return domainsKnown(this.snapshot());
  }

  /**
   * Whether the person's list of tenants could not be read and nothing is known of it: its
   * last read failed, and no list is held from before. Not a person in no tenant, which is
   * an answer: what is drawn from the list says it could not be loaded. False again once a
   * read answers. Signal-backed.
   */
  domainsUnread(): boolean {
    return domainsUnread(this.snapshot());
  }

  /**
   * Whether the digest that answers for the scope has loaded: the selected tenant's for a
   * domain-scoped target, the global one otherwise. Until it has, a false hasPermission
   * and an empty fieldPermissionStates mean nothing is known, not that the target is
   * denied.
   */
  hasDigestFor(scope: PermissionScope): boolean {
    return this.hasDigest(this.scoped(scope).domain);
  }

  /**
   * Whether a feature flag is on: true when the enabled set holds the name. False before
   * the set has loaded and for every name it does not hold, so consumers fail closed.
   * Synchronous and signal-backed, so effects and computeds that call it re-evaluate when
   * the set loads or is refreshed. No name means no requirement. On an API that serves no
   * feature flags the set never changes, and the ask goes to the client's feature state,
   * which answers off and announces the absence once under the flag's name.
   */
  featureEnabled(feature?: string): boolean {
    if (!feature) {
      return true;
    }
    const { enabled } = this.featuresSnapshot();
    return this.features.served ? enabled.has(feature) : this.features.enabled(feature);
  }

  /**
   * Answers whether a feature flag is on, loading the enabled set first if it has not
   * loaded — the asynchronous form for the route match guard, which may run before the
   * session's first check. A load the server refuses answers false.
   */
  ensureFeature(feature?: string): Observable<boolean> {
    if (!feature) {
      return of(true);
    }
    return from(this.features.ensure(feature));
  }

  /** Reloads the enabled set of feature flags; a failed load leaves every flag off. */
  refreshFeatures(): Observable<readonly string[]> {
    return from(this.features.refresh().catch(() => [] as readonly string[]));
  }

  /**
   * Answers a permission question, loading the scope's digest first if it is not
   * cached — the asynchronous form for route guards. A scope the server refuses to read
   * (its 403 or 404, or a tenant that has left the person's list) answers false: it is
   * not there for this session. A read that fails any other way is no answer, and the
   * observable errors: a failure is not a person who holds nothing.
   */
  ensurePermission(scope?: PermissionScope): Observable<boolean> {
    if (!scope) {
      return of(true);
    }
    return from(
      this.permissions.ensure(this.scoped(scope)).catch((error: unknown) => {
        if (digestFailure(error) === 'refused') {
          return false;
        }
        throw error;
      }),
    );
  }

  /**
   * Loads (or reloads) the permission digest for a domain — global when omitted —
   * into the cache. A read that fails caches nothing: the scope stays as it was (unloaded,
   * or holding its last answer), since a failure is not a person who holds nothing, and
   * the observable errors with what the read threw (`digestFailure` says whether it was a
   * refusal). No empty digest stands in for a read that failed.
   */
  loadDigest(domain?: Domain): Observable<PermissionDigest> {
    return from(this.permissions.loadDigest(domain));
  }

  /**
   * Loads the person's list of tenants from the generated user-domains endpoint. A read
   * that fails leaves the list as it was (unread, or holding its last answer), since a
   * failure is not a person in no tenant, and the observable errors with what the read
   * threw. No empty list stands in for a read that failed.
   */
  loadDomains(): Observable<readonly Domain[]> {
    return from(this.permissions.loadDomains());
  }

  /**
   * Reloads the domains and every cached digest — after a role change, say. A digest that
   * could not be read keeps its last answer, and so does the list of tenants, so no page is
   * drawn again as the page of a person who holds nothing, or is in no tenant, because a
   * read failed; where it is the open tenant's, the
   * frame's TenantService puts the refusal page in the open page's stead, saying the page
   * could not be loaded, or 404 Not Found where the server refused the read.
   */
  refreshPermissions(): Observable<void> {
    return from(this.permissions.refresh().catch(() => undefined));
  }

  /**
   * Logs a user out and calls the configured logout action. The live session ends first
   * (every subscription of the principal deleted, the feed's identity revoked, the feed
   * stopped, a fresh seed minted), then the session is logged out.
   *
   * @returns Observable with a boolean indicating whether they were logged out.
   */
  logout(): Observable<boolean> {
    return from(this.stopLive().then(() => this.client.request<unknown>('DELETE', this.sessionUrl)))
      .pipe(map(() => true))
      .pipe(
        tap(() => {
          try {
            this.logoutAction();
          } catch (error) {
            console.error('Error during logout action (LOGOUT_ACTION token):', error);
          }
          this.authenticatedSignal.set(false);
          this.sessionInfoSignal.set({} as SessionInfo);
          this.clearPermissions();
        }),
      );
  }

  /**
   * Checks a user's session with the server. The first authenticated answer of a
   * session also loads the global permission digest, the user's domains, and the enabled
   * set of feature flags when the API serves them, and starts the live session when a
   * change feed is provided; later checks (keepalives) leave the cached permissions, the
   * set, and the feed alone. A load that fails is not taken for a person who holds
   * nothing or is in no tenant: the session's answer still arrives, `digestUnread()` says
   * the global digest could not be read and `domainsUnread()` that the list of tenants
   * could not (the frame then draws that in the page's place), and the next check loads
   * again. An unauthenticated answer ends them all, the feed without a
   * word to the server: the session is gone, so no unsubscribe could be accepted, and the
   * server's expiry ends the subscriptions. An authenticated
   * answer carrying `passwordChangeRequired` loads nothing: the server refuses every
   * request but the password change until the person sets a password of their own, and
   * the first check after the change loads them.
   *
   * @returns Observable with the user session info
   */
  checkUserSession(): Observable<SessionInfo> {
    return from(this.client.request<SessionInfo>('GET', this.sessionUrl)).pipe(
      tap((sessionInfo) => {
        this.authenticatedSignal.set(!!sessionInfo?.authenticated);
        this.sessionInfoSignal.set(sessionInfo);
      }),
      switchMap((sessionInfo) => {
        if (!sessionInfo?.authenticated) {
          this.clearPermissions();
          return from(this.stopLive({ sessionGone: true })).pipe(map(() => sessionInfo));
        }
        if (sessionInfo.passwordChangeRequired || this.permissionsLoaded) return of(sessionInfo);

        this.permissionsLoaded = true;
        return from(
          Promise.all([
            this.permissions.loadDigest(),
            this.permissions.loadDomains(),
            ...(this.features.served ? [this.features.refresh()] : []),
          ])
            .catch(() => {
              // A load that failed is not done: the next check of the session loads again.
              this.permissionsLoaded = false;
            })
            .then(() => this.startLive()),
        ).pipe(map(() => sessionInfo));
      }),
    );
  }

  /**
   * Starts the client's live session with the provided change feed, when there is one and
   * the API serves live subscriptions. A feed that fails to start is reported on the
   * console and the pages stay plain; nothing about the session itself depends on it.
   */
  private async startLive(): Promise<void> {
    if (!this.changeFeed || !this.client.live.enabled || this.client.live.started) {
      return;
    }
    try {
      await this.client.live.start(this.changeFeed);
    } catch (error) {
      console.error('The change feed could not start; pages stay plain:', error);
    }
  }

  /**
   * Ends the live session, best effort: a feed that was never started sends nothing, and
   * neither does one whose session is already gone.
   */
  private async stopLive(options: { sessionGone?: boolean } = {}): Promise<void> {
    try {
      await this.client.live.stop(options);
    } catch (error) {
      console.error('The live session could not be stopped cleanly:', error);
    }
  }

  loginRoute(): string {
    return this.loginUrl;
  }

  private clearPermissions(): void {
    this.permissionsLoaded = false;
    this.permissions.clear();
    this.features.clear();
    this.unreadDigests.set(new Set());
  }
}
