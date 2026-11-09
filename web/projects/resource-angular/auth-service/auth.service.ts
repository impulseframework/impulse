import { computed, inject, Injectable, signal } from '@angular/core';
import { RESOURCE_CLIENT, storeSignal } from '@impulseframework/resource-angular/resource-client';
import {
  CHANGE_FEED,
  Domain,
  FRONTEND_LOGIN_PATH,
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
import { ClientBase, FeatureState, fieldPermissionStates, permissionState, PermissionStore } from '@impulseframework/resource';
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
 * sign-in, while the person flipping, through the feature flags dialog, has their own set
 * and digest refreshed once the write succeeds. An API that serves no feature flags (its
 * descriptor carries no features route) loads nothing: every flag answers off, so each
 * gate stays closed, the dialog says the API serves none, and the first ask of each flag
 * is announced through the client's warn hook (the console by default) naming the
 * regeneration that would serve it.
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
   * picker's source. Empty until the session authenticates.
   */
  domains = computed<readonly Domain[]>(() => this.snapshot().domains);

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
   * since no domain digest holds it.
   */
  private scoped(scope: PermissionScope): PermissionScope {
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
   * cached — the asynchronous form for route guards.
   */
  ensurePermission(scope?: PermissionScope): Observable<boolean> {
    if (!scope) {
      return of(true);
    }
    return from(this.permissions.ensure(this.scoped(scope)).catch(() => false));
  }

  /**
   * Loads (or reloads) the permission digest for a domain — global when omitted —
   * into the cache. Call it when the user selects a tenant, before rendering that
   * tenant's pages. A failed load caches an empty digest (every question answers false).
   */
  loadDigest(domain?: Domain): Observable<PermissionDigest> {
    return from(this.permissions.loadDigest(domain).catch(() => ({}) as PermissionDigest));
  }

  /** Loads the user's domain list from the generated user-domains endpoint. */
  loadDomains(): Observable<readonly Domain[]> {
    return from(this.permissions.loadDomains().catch(() => [] as Domain[]));
  }

  /** Reloads the domains and every cached digest — after a role change, say. */
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
   * set, and the feed alone. An unauthenticated answer ends them all, the feed without a
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
            .catch(() => undefined)
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
  }
}
