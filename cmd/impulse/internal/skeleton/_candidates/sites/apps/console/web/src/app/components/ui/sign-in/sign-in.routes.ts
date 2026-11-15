import { Type } from '@angular/core';
import { Route } from '@angular/router';
import { Permissions, Resources } from '@app/service/zz_gen_constants';
import { DomainRoutePath } from '@app/service/zz_gen_resources';
import { page, pageRoute } from '@impulseframework/resource-angular/auth-frame';
import { TenantSignInComponent } from './tenant-sign-in.component';

/**
 * The tenant's sign-in page as the menus know it: who may enter the tenant, as its
 * declaration says. Its address names the tenant segment's parameter, so it is a tenant's
 * page: the menus show it while a tenant is open, filled with that tenant, and only where
 * that tenant's digest holds the List grant on its directories. The menu file places it
 * (app.menus.ts); the route below carries the same page.
 */
export const tenantSignInPage = page({
  label: 'Sign-in settings',
  icon: 'login',
  route: [DomainRoutePath, 'sign-in'],
  permission: { resource: Resources.TenantDirectories, permission: Permissions.List },
});

/** tenantSignInRoute wires the tenant's sign-in page under the tenant segment. */
export const tenantSignInRoute = (): Route =>
  pageRoute(tenantSignInPage, {
    path: 'sign-in',
    loadComponent: (): Promise<Type<TenantSignInComponent>> =>
      import('./tenant-sign-in.component').then((comp) => comp.TenantSignInComponent),
  });
