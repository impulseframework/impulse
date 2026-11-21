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
} from '@impulseframework/resource-angular/auth-frame';
import { featureFlagsRoute } from '@impulseframework/resource-angular/feature-flags';
import { identityRoutes } from '@impulseframework/resource-angular/identity-pages';
import { resourceRoutes } from '@impulseframework/resource-angular/resource-route-generator';
import { sectorRoute, watchDeskRoute } from '@components/sector/sector.routes';
import { clientsConfig } from './configs/clients.config';
import { commendationsConfig } from './configs/commendations.config';
import { distressCallsConfig } from './configs/distressCalls.config';
import { hangarsConfig } from './configs/hangars.config';
import { missionDocumentsConfig } from './configs/missionDocuments.config';
import { missionsConfig } from './configs/missions.config';
import { missionsEverySectorConfig } from './configs/missionsEverySector.config';
import { squadronsConfig } from './configs/squadrons.config';
import { pilotsConfig, sectorPilotsConfig } from './configs/pilots.config';
import { sectorsConfig } from './configs/sectors.config';
import { shipClassesConfig } from './configs/shipClasses.config';
import { shipsConfig } from './configs/ships.config';
import { standingOrdersConfig } from './configs/standingOrders.config';

// The dashboard is every place's first page (provideFrame's home): in Headquarters the
// service ledger and the global digest, in a sector that sector's digest.
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
    // Every signed-in page sits in the library's frame (provideFrame in app.config.ts): the
    // bar with the menus of the menu file (app.menus.ts), the place switcher between
    // Headquarters and each sector by name, and the person's own menu.
    path: '',
    component: FrameComponent,
    canActivate: [LoginAuthenticationGuard],
    children: [
      // Signing in arrives at the root, and the person is sent to the first page of the
      // place they land in: where they last worked, else Headquarters when they hold
      // something there, else their first sector by name.
      { path: '', pathMatch: 'full', canActivate: [homeGuard], children: [] },
      // Where a URL naming a sector the session cannot reach lands (the tenant guard): the
      // server's answer, under the sector's own URL, a concealed sector answering as one
      // that does not exist.
      refusedRoute(DomainRoutePath),
      // The device page, where a command-line client sends a crew member for approval
      // (/console/device?userCode=..., the crew auth's DevicePage). It is no menu's and no
      // place's: the link lands here whichever place the person works in. The crew
      // member's own tokens for the droids outlet are the frame's API keys page, which the
      // person's menu offers as Your API keys.
      //
      // Demonstrates: outlet.tokens.
      {
        path: 'device',
        loadComponent: () => import('./components/ui/device/device.component').then((comp) => comp.DeviceComponent),
      },
      // Headquarters, the place outside every sector. A person who holds nothing here is
      // sent to the place they land in. It comes before the sector segment, so the sector
      // record's own page (`sectors/<id>`, the record's address as the API has it) is
      // reached, while every longer address under a sector passes on to the segment.
      {
        path: '',
        canActivate: [outsideGuard],
        children: [
          dashboardRoute(),
          // Global resources are config-driven over the generated metadata; the
          // sector-scoped decks are hand-written (see SectorService), except the Missions
          // page and its siblings under the sector segment below, config-driven pages in the
          // sector the URL names (RESOURCE_DOMAIN, which the frame provides) whose pickers
          // list exactly the resources the metadata names.
          //
          // Every config-driven list and row route carries the library's canDeactivateGuard,
          // attached by resourceRoutes: leaving a page whose form is dirty (FormStateService)
          // opens the library's LeavePageConfirmationModalComponent, and a route to the
          // FRONTEND_LOGIN_PATH app.config.ts provides leaves without asking. The console
          // adds no leave-page guard of its own.
          //
          // Demonstrates: form.leave-page.
          resourceRoutes(clientsConfig, resourceMeta),
          resourceRoutes(shipClassesConfig, resourceMeta),
          // The standing orders are a key-less list: resourceRoutes builds the list route
          // alone, no `:uuid` row route, since a line has no key to open by.
          resourceRoutes(standingOrdersConfig, resourceMeta),
          resourceRoutes(pilotsConfig, resourceMeta),
          // The commendations desk sits behind the one feature flag. Nothing here says so:
          // resourceRoutes reads `feature` from the generated metadata and matches the route
          // only while the flag is on (the library's featureMatch guard), so a URL to the
          // desk while it is off falls to the wildcard below as an unknown URL does, and its
          // menu entry shows only while the flag is on.
          //
          // Demonstrates: @feature.
          resourceRoutes(commendationsConfig, resourceMeta),
          resourceRoutes(sectorsConfig, resourceMeta),
          // The missions of every sector the person reaches, on one page: Missions declares
          // @listAcrossTenants, so its list is also served without the sector's segment, and
          // the config says acrossTenants. resourceRoutes mounts the list alone, with no row
          // route (a row's link leads to the mission inside its sector) and no one sector's
          // List guard: the route opens for whoever may list missions in at least one sector
          // they reach, as the server tells the global digest, and turns anyone else away.
          //
          // Demonstrates: list.across-tenants.
          resourceRoutes(missionsEverySectorConfig, resourceMeta),
          // The crew's accounts across the whole service: the accounts with a person's row
          // page (the roles held, the linked identities and the sessions under it), every
          // mailed link, the sessions and the failed sign-ins. No page is written for them
          // here: the generator describes the management it writes for an auth
          // (GenerateIdentity) in the API description, and the library builds the pages from
          // the description, each a list whose rows offer the generated methods the caller
          // may run on them.
          //
          // Demonstrates: auth.generated-identity, auth.identity-pages, list.row-methods.
          ...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Crew', scope: 'global' }),
          // The clients' accounts, the same pages over the other auth's management: the
          // console serves it because the generator program says the console manages the
          // members auth (Manages). A directory writes the members' roles at every sign-in, so
          // the description names no method that gives or takes a role or invites a person,
          // and the pages draw none.
          //
          // Demonstrates: auth.generated-identity, auth.identity-pages.
          ...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Members', scope: 'global' }),
          // The watch desk lists every live impersonation in the service: a page of
          // Headquarters.
          watchDeskRoute(),
          // The feature flags: the library's list page over the flags Lodestar declares,
          // each with who last turned it on or off, a History button and, for the adjutant,
          // the menu that turns it on or off. A page of Headquarters.
          //
          // Demonstrates: feature.history.
          featureFlagsRoute(resourceMeta),
          // The person's account and API keys pages: the library's, in every place.
          ...frameRoutes(),
        ],
      },
      // The sector segment, sectors/:sectorID, mirroring the API's: every sector-scoped
      // page lives under it. The config-driven pages in the sector are registered by
      // resourceRoutes at the route their configs name, sectors/:sectorID/<page>, and
      // mounted under the segment with the decks; the Documents and Calls pages among
      // them carry the field shapes a form never types (bytes, an object) and the
      // write-only transcript, beside the hand-written call log, and the Pilots page is
      // served here a second time, where a pilot's assignments in the sector show.
      sectorRoute([
        resourceRoutes(sectorPilotsConfig, resourceMeta),
        resourceRoutes(missionsConfig, resourceMeta),
        resourceRoutes(shipsConfig, resourceMeta),
        // The hangars and, on a hangar's page, its passes: who the hangar is open to, a
        // person, a squadron or one of the sector's groups.
        resourceRoutes(hangarsConfig, resourceMeta),
        resourceRoutes(squadronsConfig, resourceMeta),
        resourceRoutes(missionDocumentsConfig, resourceMeta),
        resourceRoutes(distressCallsConfig, resourceMeta),
        // The sector's people and its invitations, with a person's row page (the roles
        // held here and the identities under it): the tenant's side of the same generated
        // management, built by the library from the same description. The Sector Marshal
        // sees Disable on a person the sector holds alone and no such entry on a person
        // who also holds a role elsewhere: the role file's condition on the method's
        // grant decides, row by row, and no page repeats it. The Marshal's grant on the
        // method that gives a role carries a condition on the role given
        // (new.role != 'SectorMarshal' in pkg/auth/crew/roles.json), so the dialog
        // rehearses the call with a dry run when a role is picked.
        //
        // Demonstrates: auth.generated-identity, auth.identity-pages, list.row-methods, method.rehearsal.
        ...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Crew', scope: 'domain' }),
      ]),
      { path: '**', redirectTo: 'dashboard' },
    ],
  },
];
