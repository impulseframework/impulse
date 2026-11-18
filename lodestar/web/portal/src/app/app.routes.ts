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

// The tracker is every place's first page (provideFrame's home): in a sector, the missions
// the client's company booked there; outside every sector, where a client who holds no
// sector stays, it says so.
const trackerRoute = (): Route => ({
  path: 'tracker',
  loadComponent: () => import('./components/tracker/tracker.component').then((comp) => comp.TrackerComponent),
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
    // Every signed-in page sits in the library's frame (provideFrame in app.config.ts): the
    // bar with the sector the page is in and the way to the client's other sectors, Help,
    // and the person's own menu.
    path: '',
    component: FrameComponent,
    canActivate: [LoginAuthenticationGuard],
    children: [
      // Signing in arrives at the root, and the client is sent to the tracker of the sector
      // they last worked in, else their first sector by name.
      { path: '', pathMatch: 'full', canActivate: [homeGuard], children: [] },
      // Where a URL naming a sector the session cannot reach lands (tenantGuard): the
      // server's answer, under the sector's own URL.
      refusedRoute(DomainRoutePath),
      // Outside every sector: the portal's menus hold nothing here, so it is nobody's place
      // and the guard sends a client to their sector; one who holds no sector at all stays
      // on the tracker, which says so.
      {
        path: '',
        canActivate: [outsideGuard],
        children: [trackerRoute(), ...frameRoutes()],
      },
      // The sector segment, sectors/:sectorID, mirroring the API's: the tracker lives under
      // it, so a link to a sector's missions can be shared and a bookmark lands where it
      // points. The guard opens the sector the URL names before the page loads.
      {
        path: DomainRoutePath,
        canActivate: [tenantGuard],
        children: [trackerRoute(), ...frameRoutes(), { path: '**', redirectTo: 'tracker' }],
      },
      { path: '**', redirectTo: 'tracker' },
    ],
  },
];
