import { Routes } from '@angular/router';
import { apiDescriptor } from '@app/service/zz_gen_api';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { LoginAuthenticationGuard } from '@impulseframework/resource-angular/auth-authentication-guard';
import { FrameComponent, frameRoutes } from '@impulseframework/resource-angular/auth-frame';
import { featureFlagsRoute } from '@impulseframework/resource-angular/feature-flags';
import { identityRoutes } from '@impulseframework/resource-angular/identity-pages';

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
      {
        path: 'dashboard',
        loadComponent: () =>
          import('./components/ui/dashboard/dashboard.component').then((comp) => comp.DashboardComponent),
      },
      // The staff's accounts: the accounts with a person's row page (the roles held, the
      // sign-in identities and the sessions under it), the mailed links, the sessions and
      // the failed sign-ins. No page is written for them here: the generator describes the
      // management it writes for the auth (GenerateIdentity) in the API description, and
      // the library builds the pages from it, each placed in the menus behind the List
      // grant on its resource, each row offering the methods the caller may run on it.
      ...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'global' }),
      // The feature flags: the library's list page over the flags the application declares.
      featureFlagsRoute(resourceMeta),
      // The person's account page: the library's.
      ...frameRoutes(),
      { path: '**', redirectTo: 'dashboard' },
    ],
  },
];
