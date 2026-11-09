import { Route } from '@angular/router';
import { AuthorizationGuard } from '@impulseframework/resource-angular/auth-authorization-guard';
import { featureMatch } from '@impulseframework/resource-angular/auth-feature';
import { canDeactivateGuard } from '@impulseframework/resource-angular/guards';
import { addNavItem, registerResourcePage } from '@impulseframework/resource-angular/resource-nav';
import {
  keyFields,
  ListPermission,
  PermissionScope,
  ReadPermission,
  Resource,
  ResourceMeta,
  RootConfig,
  RouteResourceData,
  writeResource,
} from '@impulseframework/resource-angular/types';

/**
 * Builds the routes for a config-driven resource and lists it in the navigation, in the
 * group the config names or at the top level when it names none.
 *
 * The routes are guarded by the resource's List permission, answered from the permission
 * digest by the AuthorizationGuard, and the navigation item carries the same scope so a
 * `impulseHasPermission`-gated menu hides what the user cannot open. The scope names no
 * domain: a global resource is asked in the global digest, and a domain-scoped one in
 * the selected tenant's (RESOURCE_DOMAIN), which AuthService settles. The row route
 * (`:uuid`) is guarded on Read as well, since a role may list a resource without reading
 * its rows — Read on the resource whose row the page opens: the listed resource, or its
 * table when the list is a view declaring one (the metadata's `rowsOf`), since a view
 * lists and never reads. A config may set `nav.navItem.permission` to gate on something
 * else. A domain-scoped resource's metadata route carries the tenant parameter in
 * braces, so such a page sets `routeData.route` to the path it should live at, which may
 * carry the tenant parameter as the router names it (`sectors/:sectorID/missions`) when
 * the page lives under the tenant segment. The page is registered as where the
 * resource's rows open (resourcePageRoute), for the listed resource and for its table,
 * so another list's row route lands here; the route is recorded as written, and every
 * link built from it (the list's View column, the after-create navigation) puts the
 * page's tenant in the parameter's place (pageRouteResolver). A listed resource
 * with no key field (a `@computed` or `@virtual` struct with no `@primarykey`) is served
 * whole and has no row: its page gets no row route and is registered as nowhere a row
 * opens, as `hasViewRoute: false` says for a keyed one. A resource behind a feature flag
 * (the metadata's `feature`) gets a match guard from it: while the flag is off the router
 * passes over the page and a dark URL falls to the application's wildcard, and the
 * navigation item carries the flag too, so a `impulseFeature`-gated menu hides it.
 */
export const resourceRoutes = (config: RootConfig, resourceMeta: (resource: Resource) => ResourceMeta): Route => {
  const resource = config.parentConfig.primaryResource as Resource;
  const meta = resourceMeta(resource);
  if (!meta) {
    return {} as Route;
  }

  const scope: PermissionScope = { resource, permission: ListPermission };
  config.nav.navItem.permission ??= scope;
  if (meta.feature) {
    config.nav.navItem.feature ??= meta.feature;
  }
  // The page is matched only while its flag is on; an ungated page is matched as any other.
  const gated = (route: Route): Route => (meta.feature ? { ...route, canMatch: [featureMatch(meta.feature)] } : route);

  const pageRoute = config.routeData.route || meta.route;
  // The page is listed in the navigation at its route: in the group the config names, or
  // at the top level when it names none.
  addNavItem(config.nav, pageRoute);
  const rowResource = writeResource(resource, meta);
  const keyless = keyFields(meta).length === 0;
  if (!keyless) {
    registerResourcePage(resource, pageRoute);
    registerResourcePage(rowResource, pageRoute);
  }

  const data = { config, scope } satisfies RouteResourceData;
  const viewData = { config, scope: { resource: rowResource, permission: ReadPermission } } satisfies RouteResourceData;

  if (config.routeData.route) {
    const baseRoute: Route = {
      path: config.routeData.route,
      data,
      canActivate: [AuthorizationGuard],
      children: [
        {
          path: '',
          loadComponent: () => import('@impulseframework/resource-angular/resource').then((mod) => mod.ResourceListCreateComponent),
          canDeactivate: [canDeactivateGuard],
        },
      ],
    };
    if (config.routeData.hasViewRoute !== false && !keyless) {
      baseRoute.children?.push({
        path: ':uuid',
        data: viewData,
        canActivate: [AuthorizationGuard],
        loadComponent: () => import('@impulseframework/resource-angular/resource').then((mod) => mod.CompoundResourceComponent),
        canDeactivate: [canDeactivateGuard],
      });
    }
    // A configured route stands with or without its row route; falling through would
    // discard it for the meta route and add the row route the config switched off.
    return gated(baseRoute);
  }

  const rowRoute: Route = {
    path: ':uuid',
    data: viewData,
    canActivate: [AuthorizationGuard],
    loadComponent: () => import('@impulseframework/resource-angular/resource').then((mod) => mod.CompoundResourceComponent),
    canDeactivate: [canDeactivateGuard],
  };
  const listRoute: Route = {
    path: '',
    loadComponent: () => import('@impulseframework/resource-angular/resource').then((mod) => mod.ResourceListCreateComponent),
    canDeactivate: [canDeactivateGuard],
  };

  return gated({
    path: meta.route,
    data,
    canActivate: [AuthorizationGuard],
    children: keyless ? [listRoute] : [rowRoute, listRoute],
  } satisfies Route);
};
