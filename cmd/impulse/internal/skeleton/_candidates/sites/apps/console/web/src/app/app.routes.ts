import { Route, Routes } from '@angular/router';
import { apiDescriptor } from '@app/service/zz_gen_api';
import { DomainRoutePath, resourceMeta } from '@app/service/zz_gen_resources';
import { LoginAuthenticationGuard } from '@impulseframework/resource-angular/auth-authentication-guard';
import {
  FrameComponent,
  frameRoutes,
  homeGuard,
  outsideGuard,
  refusedRoute,
  tenantGuard,
} from '@impulseframework/resource-angular/auth-frame';
import { identityRoutes } from '@impulseframework/resource-angular/identity-pages';
import { resourceRoutes } from '@impulseframework/resource-angular/resource-route-generator';
import { tenantSignInRoute } from '@components/ui/sign-in/sign-in.routes';
import { tenantsConfig } from './configs/tenants.config';

// The dashboard is every place's first page (provideFrame's home): outside every tenant it
// shows the global digest, under a tenant the digest there.
const dashboardRoute = (): Route => ({
  path: 'dashboard',
  loadComponent: () => import('./components/ui/dashboard/dashboard.component').then((comp) => comp.DashboardComponent),
});

// A page the library builds for a tenant names its whole route, the tenant segment
// included, which is what its menu entry links to; under the segment below it is mounted
// by the rest of that route.
const underTenant = (page: Route): Route => ({ ...page, path: page.path?.slice(DomainRoutePath.length + 1) });

export const routes: Routes = [
  {
    path: 'login',
    loadComponent: () => import('./components/login/login.component').then((comp) => comp.LoginComponent),
  },
  {
    path: 'logout',
    loadComponent: () =>
      import('@impulseframework/resource-angular/auth-sign-out').then((comp) => comp.SignOutComponent),
  },
  {
    // Every signed-in page sits in the library's frame (provideFrame in app.config.ts).
    path: '',
    component: FrameComponent,
    canActivate: [LoginAuthenticationGuard],
    children: [
      // Signing in arrives at the root, and the person is sent to the first page of the
      // place they land in: where they last worked, else Configuration when they hold
      // something there, else their first tenant by name.
      { path: '', pathMatch: 'full', canActivate: [homeGuard], children: [] },
      // Where a URL naming a tenant the session cannot reach lands (tenantGuard): the
      // server's answer, under the tenant's own URL.
      refusedRoute(DomainRoutePath),
      // Configuration, the place outside every tenant. A person who holds nothing here is
      // sent to the place they land in; a person with no place at all stays, and the
      // dashboard says they hold nothing. It comes before the tenant segment, so the
      // tenant record's own page (`tenants/<id>`, the record's address as the API has it)
      // is reached, while every longer address under a tenant passes on to the segment.
      {
        path: '',
        canActivate: [outsideGuard],
        children: [
          dashboardRoute(),
          // All tenants: the library's list page over the tenant record.
          resourceRoutes(tenantsConfig, resourceMeta),
          // The staff's accounts across the whole application: the accounts with a
          // person's row page (the roles held, the sign-in identities and the sessions
          // under it), the mailed links, the sessions and the failed sign-ins. No page is
          // written for them here: the generator describes the management it writes for
          // the auth (GenerateIdentity) in the API description, and the library builds the
          // pages from it, each placed in the menus behind the List grant on its resource,
          // each row offering the methods the caller may run on it.
          ...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'global' }),
          // The person's account and About pages: the library's, in every place.
          ...frameRoutes(),
        ],
      },
      // The tenant segment, tenants/:tenantID, mirroring the API's: every tenant-scoped
      // page lives under it, so a link to one can be shared and a bookmark lands where it
      // points. The guard opens the tenant the URL names before any page here loads; the
      // navigation that lands settles the rest, so leaving needs no guard.
      {
        path: DomainRoutePath,
        canActivate: [tenantGuard],
        children: [
          dashboardRoute(),
          // The tenant's people and its invitations, with a person's row page (the roles
          // held here and the sign-in identities under it): the tenant's side of the same
          // generated management, built by the library from the same description. A row
          // offers a method where the role file's condition on its grant holds for that
          // row, so the tenant's administrator is offered nothing that changes the account
          // of a person who also holds a role in another tenant or for the whole
          // application.
          ...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'domain' }).map(underTenant),
          // Who may enter the tenant: its directories and whether they are the only way in.
          tenantSignInRoute(),
          ...frameRoutes(),
          { path: '**', redirectTo: 'dashboard' },
        ],
      },
      { path: '**', redirectTo: 'dashboard' },
    ],
  },
];
