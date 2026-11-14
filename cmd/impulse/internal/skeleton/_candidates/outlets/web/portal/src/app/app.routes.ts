import { Route, Routes } from '@angular/router';
import { apiDescriptor } from '@app/service/zz_gen_api';
import { DomainRoutePath, resourceMeta } from '@app/service/zz_gen_resources';
import { tenantGuard, tenantLeave } from '@app/tenant/tenant.guard';
import { refusedSegment } from '@app/tenant/tenant.service';
import { LoginAuthenticationGuard } from '@impulseframework/resource-angular/auth-authentication-guard';
import { identityRoutes } from '@impulseframework/resource-angular/identity-pages';
import { UiComponent } from '@components/ui/ui.component';

// The dashboard lives in both places: outside a tenant it shows the global digest, under
// a tenant the digest there.
const dashboardRoute = (): Route => ({
  path: 'dashboard',
  loadComponent: () => import('./components/ui/dashboard/dashboard.component').then((comp) => comp.DashboardComponent),
});

// A page the library builds for a tenant names its whole route, the tenant segment
// included, which is what its navigation item links to; under the segment below it is
// mounted by the rest of that route.
const underTenant = (page: Route): Route => ({ ...page, path: page.path?.slice(DomainRoutePath.length + 1) });

export const routes: Routes = [
  {
    path: 'login',
    loadComponent: () => import('./components/login/login.component').then((comp) => comp.LoginComponent),
  },
  {
    path: 'logout',
    loadComponent: () => import('@impulseframework/resource-angular/auth-sign-out').then((comp) => comp.SignOutComponent),
  },
  {
    path: '',
    component: UiComponent,
    canActivate: [LoginAuthenticationGuard],
    children: [
      dashboardRoute(),
      // The members' accounts across the whole application: the accounts with a person's
      // row page (the roles held, the sign-in identities and the sessions under it), the
      // mailed links, the sessions and the failed sign-ins. No page is written for them
      // here: the generator describes the management it writes for the auth
      // (GenerateIdentity) in the API description, and the library builds the pages from
      // it, each listed in the navigation behind the List grant on its resource, each row
      // offering the methods the caller may run on it.
      ...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Members', scope: 'global' }),
      // Where a URL naming a tenant the session cannot reach lands (tenantGuard): the
      // server's answer, under the tenant's own URL. Declared ahead of the segment, so the
      // guard is not asked again on the way there.
      {
        path: `${DomainRoutePath}/${refusedSegment}`,
        loadComponent: () =>
          import('./components/ui/tenant-refused/tenant-refused.component').then((comp) => comp.TenantRefusedComponent),
      },
      // The tenant segment, tenants/:tenantID, mirroring the API's: every tenant-scoped
      // page lives under it, so a link to one can be shared and a bookmark lands where it
      // points. The guard settles the tenant from the URL before any page here loads and
      // clears it on the way out; nothing else writes it.
      {
        path: DomainRoutePath,
        canActivate: [tenantGuard],
        canDeactivate: [tenantLeave],
        children: [
          dashboardRoute(),
          // The tenant's people and its invitations, with a person's row page (the roles
          // held here and the sign-in identities under it): the tenant's side of the same
          // generated management, built by the library from the same description. A row
          // offers a method where the role file's condition on its grant holds for that
          // row, so the tenant's administrator is offered nothing that changes the account
          // of a person who also holds a role in another tenant or for the whole
          // application.
          ...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Members', scope: 'domain' }).map(underTenant),
          { path: '**', redirectTo: 'dashboard' },
        ],
      },
      { path: '**', redirectTo: 'dashboard' },
    ],
  },
];
