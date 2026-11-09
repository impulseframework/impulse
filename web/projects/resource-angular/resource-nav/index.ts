import { inject } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { ActivatedRoute, ActivatedRouteSnapshot, NavigationEnd, Router } from '@angular/router';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { MenuItem, Resource, RESOURCE_DOMAIN } from '@impulseframework/resource-angular/types';
import { filter } from 'rxjs';

/**
 * Where each resource's config-driven page lives, registered by resourceRoutes: a
 * list's row route opens its target on that page. The first registration for a
 * resource stands, and a resource with no page opens on its metadata route. The route
 * is recorded as the config wrote it, so it may carry the tenant parameter
 * (`sectors/:sectorID/missions`); a link built from it goes through the resolver
 * `pageRouteResolver` builds, which puts the page's tenant in the parameter's place.
 */
const resourcePages = new Map<Resource, string>();

/** Records the page a resource's rows open on, as the config wrote its route. */
export function registerResourcePage(resource: Resource, route: string): void {
  if (!resourcePages.has(resource)) {
    resourcePages.set(resource, route);
  }
}

/** The page a resource's rows open on, undefined when none is registered. */
export function resourcePageRoute(resource: Resource): string | undefined {
  return resourcePages.get(resource);
}

/** Resolves a registered route into the path a link or a navigation uses; `pageRouteResolver` builds one. */
export type PageRouteResolver = (route: string) => string;

/**
 * Builds the resolver of registered routes into concrete paths. A registered route is
 * the path a config-driven page lives at as its config wrote it, and it may carry the
 * API's tenant parameter as the router names it (`sectors/:sectorID/missions`: the
 * descriptor's `domainRoute.param` after a colon). The resolver puts the page's tenant
 * in the parameter's place, so a row link is the row's real URL, one that opens in a
 * new tab, and an after-create navigation lands on the new row. The tenant is the
 * activated route's value for the parameter when the page is under it, else the
 * selected tenant (RESOURCE_DOMAIN). A route naming no parameter is returned as
 * written, as is any route on an API that declares no domain route. A route naming the
 * parameter when no tenant is known is refused, naming the route and the parameter:
 * the link cannot be drawn.
 *
 * Call it in an injection context (a component's field initializer). The resolver
 * reads the last navigation, so one read in a computed follows the URL: a navigation
 * that changes the tenant re-resolves the links.
 */
export function pageRouteResolver(): PageRouteResolver {
  const param = inject(RESOURCE_CLIENT).descriptor.domainRoute?.param;
  const activatedRoute = inject(ActivatedRoute);
  const domain = inject(RESOURCE_DOMAIN);
  const navigated = toSignal(inject(Router).events.pipe(filter((event) => event instanceof NavigationEnd)));

  return (route: string): string => {
    if (!param) {
      return route;
    }
    const marker = `:${param}`;
    const segments = route.split('/');
    if (!segments.includes(marker)) {
      return route;
    }
    navigated();
    const tenant = routeParam(activatedRoute.snapshot, param) ?? domain();
    if (!tenant) {
      throw new Error(
        `${route}: the route names the tenant parameter ${marker}, and no tenant is known: the page is not under the parameter and no tenant is selected (RESOURCE_DOMAIN)`,
      );
    }
    return segments.map((segment) => (segment === marker ? tenant : segment)).join('/');
  };
}

/**
 * The parameter's value on the page's route or the nearest ancestor carrying it, so the
 * tenant is found whether the page's route declares the parameter or sits under a
 * segment that does, whatever the router's parameter inheritance. A snapshot with no path
 * (one a spec stubs) is read alone.
 */
function routeParam(snapshot: ActivatedRouteSnapshot | undefined, param: string): string | undefined {
  const path = snapshot?.pathFromRoot ?? (snapshot ? [snapshot] : []);
  for (let i = path.length - 1; i >= 0; i--) {
    const value = path[i].params?.[param] as string | undefined;
    if (value) {
      return value;
    }
  }
  return undefined;
}

export const generatedNavItems: MenuItem[] = [];
export const generatedNavGroups: string[] = [];

/**
 * Adds a navigation item to the generated nav items array.
 */
export function addNavItem(
  nav: {
    navItem: MenuItem;
    group?: string;
  },
  route: string,
): void {
  nav.navItem.route = [route];

  if (!nav.group) {
    generatedNavItems.push(nav.navItem);
    return;
  }

  if (!generatedNavGroups.includes(nav.group)) {
    generatedNavGroups.push(nav.group);
  }

  let groupItem = generatedNavItems.find((item) => item.label === nav.group);

  if (!groupItem) {
    groupItem = { label: nav.group, children: [] };
    generatedNavItems.push(groupItem);
  }

  groupItem.children = groupItem.children || [];
  groupItem.children.push(nav.navItem);

  generatedNavItems.sort((a, b) => (a.label > b.label ? -1 : 1));
}
