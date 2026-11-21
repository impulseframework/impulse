import { inject } from '@angular/core';
import { ActivatedRouteSnapshot, CanActivateFn, Router, RouterStateSnapshot } from '@angular/router';
import { AuthService, unreadPageRedirect, unreadPageUnderWay } from '@impulseframework/resource-angular/auth-service';
import { PermissionScope } from '@impulseframework/resource-angular/types';
import { catchError, map, of } from 'rxjs';

/**
 * Route guard requiring the permission named by the route's `data.scope` (a
 * PermissionScope). The answer comes from the permission digest: a granted or
 * conditional entry admits, absence redirects to the root route. The digest for the
 * scope's domain loads on demand, so a deep link into a tenant route resolves without
 * the application prefetching anything.
 *
 * A digest that could not be read is no answer: the person is not turned away as one who
 * holds nothing. The navigation is sent to the root address carrying the address that was
 * asked for (unreadPageRedirect), where the library's frame draws "This page could not be
 * loaded" with Try again in the page's place; trying again runs this guard again. Where
 * the root address itself leads to a route this guard stands on, the guard lets that
 * navigation land instead of sending it round again: the frame draws no page under it
 * until a read answers.
 */
export const AuthorizationGuard: CanActivateFn = (route: ActivatedRouteSnapshot, state: RouterStateSnapshot) => {
  const router = inject(Router);
  const auth = inject(AuthService);
  const scope = route.data['scope'] as PermissionScope | undefined;

  return auth.ensurePermission(scope).pipe(
    map((allowed) => (allowed ? true : router.createUrlTree(['/']))),
    catchError(() => of(unreadPageUnderWay(router) ? true : unreadPageRedirect(router, state.url))),
  );
};
