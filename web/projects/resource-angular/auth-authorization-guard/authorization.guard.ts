import { inject } from '@angular/core';
import { ActivatedRouteSnapshot, CanActivateFn, Router } from '@angular/router';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { PermissionScope } from '@impulseframework/resource-angular/types';
import { map } from 'rxjs';

/**
 * Route guard requiring the permission named by the route's `data.scope` (a
 * PermissionScope). The answer comes from the permission digest: a granted or
 * conditional entry admits, absence redirects to the root route. The digest for the
 * scope's domain loads on demand, so a deep link into a tenant route resolves without
 * the application prefetching anything.
 */
export const AuthorizationGuard: CanActivateFn = (route: ActivatedRouteSnapshot) => {
  const router = inject(Router);
  const auth = inject(AuthService);
  const scope = route.data['scope'] as PermissionScope | undefined;

  return auth.ensurePermission(scope).pipe(map((allowed) => (allowed ? true : router.createUrlTree(['/']))));
};
