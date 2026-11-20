import { computed, effect, inject, Injectable, signal, untracked } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import {
  ActivatedRouteSnapshot,
  PRIMARY_OUTLET,
  RedirectCommand,
  ResolveEnd,
  Router,
  UrlSegment,
  UrlSegmentGroup,
} from '@angular/router';
import { Domain, Method, Permission, PermissionDigestState, Resource } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { filter } from 'rxjs';
import { Frame } from './frame';

/**
 * The segment under a tenant where the tenant guard sends a navigation it cannot open: a
 * URL naming a tenant the session cannot reach, or a tenant whose permission digest could
 * not be read.
 */
export const refusedSegment = 'refused';

/**
 * What the guard's redirect carries, as the navigation's `info`, when a tenant's permission
 * digest could not be read: the address that was asked for.
 */
class UnreadTenant {
  constructor(readonly url: string) {}
}

/**
 * TenantService holds the one piece of state the tenant's pages share, the tenant the page
 * is open in, and answers permission questions in it. The tenant is the permission domain
 * of every request those pages make, and every library page reads it (RESOURCE_DOMAIN,
 * which provideFrame provides from here).
 *
 * The tenant is a consequence of the route, never a choice. Every tenant's page lives under
 * the tenant segment (`tenants/:tenantID`, the generated DomainRoutePath, mirroring the
 * API's). The tenant guard on that segment writes `current` from the URL before the page
 * loads, and the navigation that lands has the last word: once every guard has passed
 * (ResolveEnd), the landing URL settles the tenant, and a URL outside the segment clears
 * it. A navigation a guard turns away or redirects writes nothing lasting, so no guard is
 * needed for leaving. The place switcher sets no tenant: it navigates to the same page
 * under the chosen tenant (`open`). So a link to a tenant's page can be shared, and a
 * bookmark lands where it points.
 *
 * The tenant guard reads the tenant's permission digest into the client's cache before
 * it enters the tenant, so the digest is in when a page under the segment first draws. A
 * tenant entered with no digest in the cache loads it on entering. `can` and `state`
 * answer from the digest, and a question asked inside reactive code is asked again when
 * it loads.
 *
 * A tenant whose digest could not be read is not entered: the guard sends the navigation
 * to the refusal page with the address that was asked for (`unreadRedirect`), and `unread`
 * holds that address while the page stands in for it, so the page can say the read failed
 * and offer to try again. Like the tenant, it is settled by the navigation that lands.
 */
@Injectable({ providedIn: 'root' })
export class TenantService {
  private client = inject(RESOURCE_CLIENT);
  private auth = inject(AuthService);
  private router = inject(Router);
  private frame = inject(Frame);

  /** The tenants the session holds at least one grant in and reaches, from the tenant list. */
  readonly tenants = computed<readonly string[]>(() => this.auth.domains());

  // The tenant the URL names: written by the tenant guard (enter) and by the landing
  // navigation (enter, leave), nothing else.
  private routed = signal('');

  /** The tenant the page is open in; empty outside the tenant segment. */
  readonly current = this.routed.asReadonly();

  // The address the landed navigation was asked for and could not open, because the
  // tenant's digest could not be read: written by the landing navigation, nothing else.
  private unreadUrl = signal('');

  /**
   * The address of the page that could not be opened because what the session holds in its
   * tenant could not be read, while the refusal page stands in for it; empty otherwise.
   */
  readonly unread = this.unreadUrl.asReadonly();

  constructor() {
    // Entering a tenant re-scopes every permission question. The tenant guard has read
    // the tenant's digest by then; a tenant entered with none in the cache loads it here.
    // The check and the load run untracked, so the effect never adopts the reads they make
    // as dependencies.
    effect(() => {
      const tenant = this.current();
      if (!tenant) return;
      untracked(() => {
        if (this.client.permissions.hasDigest(tenant as Domain)) return;
        void this.client.permissions.loadDigest(tenant as Domain).catch(() => undefined);
      });
    });

    // The landing navigation settles the tenant: past every guard, the URL it lands on is
    // under the segment or it is not. A reused segment (the same tenant, reached again
    // through a redirect) runs no guard, so this is what keeps it open.
    this.router.events
      .pipe(
        filter((event): event is ResolveEnd => event instanceof ResolveEnd),
        takeUntilDestroyed(),
      )
      .subscribe((event) => {
        const tenant = this.landedTenant(event.state.root);
        if (tenant && tenant !== this.routed()) {
          this.enter(tenant);
        } else if (!tenant && this.routed()) {
          this.leave();
        }
        // Only the guard's redirect for an unread tenant carries the address; any other
        // landing, the page opening on a second try among them, clears it.
        const info = this.router.currentNavigation()?.extras.info;
        this.unreadUrl.set(info instanceof UnreadTenant ? info.url : '');
      });
  }

  /**
   * enter opens a tenant: the guard calls it with the tenant the URL names once the session
   * is known to reach it, before any page under the segment loads.
   */
  enter(tenant: string): void {
    this.routed.set(tenant);
  }

  /** leave closes the tenant: the navigation landed outside the segment. */
  leave(): void {
    this.routed.set('');
  }

  /**
   * unreadRedirect is where the tenant guard sends a navigation whose tenant's digest could
   * not be read: the refusal page under the tenant, carrying the address that was asked
   * for (`url`), which `unread` then holds. The browser's address is left as it is, so a
   * deep link keeps its address and a reload asks for the page again.
   */
  unreadRedirect(tenant: string, url: string): RedirectCommand {
    return new RedirectCommand(this.router.createUrlTree(['/', this.frame.segment, tenant, refusedSegment]), {
      skipLocationChange: true,
      info: new UnreadTenant(url),
    });
  }

  /**
   * open navigates to the page the browser is on, under the given tenant: the place
   * switcher's move between tenants. A page under a tenant keeps its path with the tenant
   * replaced; a page outside one enters the tenant on that path (`/users` opens
   * `/tenants/acme/users`); the refusal page opens the tenant's first page. Query and
   * fragment travel along, and the guard on the segment settles the tenant from the new URL.
   */
  open(tenant: string): Promise<boolean> {
    const segment = this.frame.segment;
    const tree = this.router.parseUrl(this.router.url);
    const primary = tree.root.children[PRIMARY_OUTLET];
    const segments = primary?.segments ?? [];
    const underTenant = segments[0]?.path === segment && segments.length > 1;
    let page = underTenant ? segments.slice(2) : segments;
    if (page[0]?.path === refusedSegment) {
      page = [new UrlSegment(this.frame.config.home, {})];
    }
    const under = [new UrlSegment(segment, {}), new UrlSegment(tenant, {}), ...page];
    if (primary) {
      primary.segments = under;
    } else {
      tree.root.children[PRIMARY_OUTLET] = new UrlSegmentGroup(under, {});
    }
    return this.router.navigateByUrl(tree);
  }

  /**
   * can answers one permission question from the digest. The client resolves the scope: a
   * global resource asks the global digest, a tenant's resource or method asks the open
   * tenant's. Conditional grants answer true: render, and let the server narrow per row.
   */
  can(permission: Permission, target: Resource | Method): boolean {
    return this.client.can(permission, target, (this.current() || undefined) as Domain | undefined);
  }

  /** The digest's tri-state for one target in its scope. */
  state(permission: Permission, target: Resource | Method): PermissionDigestState | undefined {
    const tenant = this.current() || undefined;
    const scope = this.client.descriptor.resources[target]?.scope ?? this.client.descriptor.methods[target]?.scope;
    if (scope === 'domain' && !tenant) return undefined;
    return this.client.permissions.state({
      resource: target,
      permission,
      domain: scope === 'domain' ? (tenant as Domain) : undefined,
    });
  }

  /**
   * The tenant a landed URL is open in: the parameter of the tenant segment's own route
   * (the one the tenant guard sits on), or empty when the URL is not under it. The refusal
   * page names a tenant in its URL but is not under the segment, so it opens none.
   */
  private landedTenant(root: ActivatedRouteSnapshot): string {
    for (let route: ActivatedRouteSnapshot | null = root; route; route = route.firstChild) {
      if (route.routeConfig?.path === this.frame.tenantPath) {
        return route.paramMap.get(this.frame.param) ?? '';
      }
    }
    return '';
  }
}
