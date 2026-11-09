import { inject } from '@angular/core';
import { ActivatedRouteSnapshot, CanActivateFn, Router, RouterStateSnapshot } from '@angular/router';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { API_URL, BASE_URL } from '@impulseframework/resource-angular/types';
import { Observable, of } from 'rxjs';
import { catchError, map, switchMap } from 'rxjs/operators';

/**
 * Route guard that protects routes using OIDC (OpenID Connect) authentication.
 *
 * If the user is not authenticated, they are redirected to the external OIDC
 * login endpoint with the current URL encoded as a return URL parameter.
 *
 */
export const OIDCAuthenticationGuard = (
  _: ActivatedRouteSnapshot,
  routerState: RouterStateSnapshot,
): Observable<boolean> => {
  const authService = inject(AuthService);
  const baseUrl = inject(BASE_URL);
  const apiUrl = inject(API_URL);

  const authenticate = (): void => {
    const url = routerState.url;
    const absoluteUrl = baseUrl + (!url.toString().startsWith('/') ? '/' + url : url);
    const encodedUrl = encodeURIComponent(absoluteUrl);
    window.location.href = `${apiUrl}/user/login?returnUrl=${encodedUrl}`;
  };

  if (authService.authenticated()) {
    return of(true);
  }

  return authService.checkUserSession().pipe(
    map((sessionInfo) => {
      if (sessionInfo?.authenticated) {
        return true;
      }

      authenticate();
      return false;
    }),
    catchError(() => {
      authenticate();
      return of(false);
    }),
  );
};

/**
 * Route guard that protects routes using internal login-based authentication.
 *
 * If the user is not authenticated, they are redirected to the application's
 * internal FRONTEND_LOGIN_PATH route. The attempted URL is stored in the AuthService so the
 * user can be redirected back after successful login. A session whose operator-set
 * password must change (`passwordChangeRequired`) is sent there the same way: the server
 * serves the page nothing until the person sets a password of their own, and the login
 * page serves the change form.
 *
 */
export const LoginAuthenticationGuard: CanActivateFn = (
  _: ActivatedRouteSnapshot,
  routerState: RouterStateSnapshot,
) => {
  const router = inject(Router);
  const authService = inject(AuthService);

  if (authService.authenticated() && !authService.sessionInfo().passwordChangeRequired) {
    return of(true);
  }

  return authService.checkUserSession().pipe(
    switchMap((sessionInfo) => {
      if (sessionInfo?.authenticated && !sessionInfo.passwordChangeRequired) {
        return of(true);
      }

      return setRedirectAndGetLoginUrlTree(authService, router, routerState.url);
    }),
    catchError(() => {
      return setRedirectAndGetLoginUrlTree(authService, router, routerState.url);
    }),
  );
};

/**
 * Impure function used to handle redirecting user to login
 *
 * Has the side effect of storing the url the user wished to access
 * in AuthService
 */
const setRedirectAndGetLoginUrlTree = (authService: AuthService, router: Router, redirectUrl: string) => {
  authService.redirectUrl.set(redirectUrl);
  return of(router.createUrlTree([authService.loginRoute()]));
};
