import { Route, Routes } from '@angular/router';
import { DomainRoutePath } from '@app/service/zz_gen_resources';
import { LoginAuthenticationGuard } from '@impulseframework/resource-angular/auth-authentication-guard';
import {
  FrameComponent,
  frameRoutes,
  homeGuard,
  outsideGuard,
  refusedRoute,
  tenantGuard,
} from '@impulseframework/resource-angular/auth-frame';

// The dashboard is every place's first page (provideFrame's home): outside every tenant it
// shows the global digest, under a tenant the digest there.
const dashboardRoute = (): Route => ({
  path: 'dashboard',
  loadComponent: () => import('./components/ui/dashboard/dashboard.component').then((comp) => comp.DashboardComponent),
});

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
      // dashboard says they hold nothing. It comes before the tenant segment, and every
      // address under a tenant passes on to the segment.
      {
        path: '',
        canActivate: [outsideGuard],
        children: [
          dashboardRoute(),
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
        children: [dashboardRoute(), ...frameRoutes(), { path: '**', redirectTo: 'dashboard' }],
      },
      { path: '**', redirectTo: 'dashboard' },
    ],
  },
];
