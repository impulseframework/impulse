import { Type } from '@angular/core';
import { Route } from '@angular/router';
import { apiDescriptor } from '@app/service/zz_gen_api';
import { Methods, Permissions, Resources } from '@app/service/zz_gen_constants';
import { DomainRoutePath } from '@app/service/zz_gen_resources';
import { frameRoutes, page, pageRoute, tenantGuard } from '@impulseframework/resource-angular/auth-frame';
import { tenantSignInPage, tenantSignInRoute } from '@impulseframework/resource-angular/identity-pages';
import { DashboardComponent } from '@components/ui/dashboard/dashboard.component';
import { BriefingComponent } from './briefing/briefing.component';
import { FlightDeckComponent } from './flight-deck/flight-deck.component';
import { HangarDeckComponent } from './hangar-deck/hangar-deck.component';
import { RosterComponent } from './roster/roster.component';

const list = Permissions.List;

/**
 * The sector's decks that are Lodestar's own pages (see SectorService for why they are
 * hand-written), in the order the Decks menu lists them; the salvage hold, the call log
 * and the hazard board are the library's lists (their configs), placed in the same menu.
 * Each lives under the sector segment and shows under the List grant its deck reads,
 * asked of the sector's digest; the briefing under Execute on the method that compiles
 * it.
 */
export const deckPages = {
  flightDeck: page({
    label: 'Flight deck',
    icon: 'flight_takeoff',
    route: [DomainRoutePath, 'flight-deck'],
    permission: { resource: Resources.Missions, permission: list },
  }),
  hangarDeck: page({
    label: 'Hangar deck',
    icon: 'build',
    route: [DomainRoutePath, 'hangar-deck'],
    permission: { resource: Resources.Refits, permission: list },
  }),
  roster: page({
    label: 'Squadron roster',
    icon: 'groups',
    route: [DomainRoutePath, 'roster'],
    permission: { resource: Resources.Squadrons, permission: list },
  }),
  briefing: page({
    label: 'Briefing',
    icon: 'summarize',
    route: [DomainRoutePath, 'briefing'],
    permission: { resource: Methods.CompileBriefing, permission: Permissions.Execute },
  }),
} as const;

/**
 * Who may enter the sector: its declared directories and whether they are the only way in.
 *
 * Demonstrates: tenancy.declaration, tenancy.directory-only, tenancy.default-seat, tenancy.directory-second-factor.
 */
export const sectorSignInPage = tenantSignInPage(apiDescriptor, { auth: 'Crew' });

/**
 * sectorRoute is the sector segment, `sectors/:sectorID` (the generated DomainRoutePath,
 * mirroring the API's): every sector-scoped page lives under it, so a link to one can be
 * shared and a bookmark lands where it points. The library's tenant guard settles the
 * sector from the URL before any page here loads (TenantService, which SectorService
 * reads), and a sector the session cannot reach lands on the frame's refusal page, in the
 * server's words; leaving needs no guard, since the navigation that lands settles the
 * rest. Under it: the dashboard in the sector, the config-driven pages the caller hands in
 * (registered by resourceRoutes at the route their configs name,
 * `sectors/:sectorID/missions`, and mounted here by that route's last part), the decks,
 * the sector's sign-in page, and the library's pages, in every place.
 *
 * Demonstrates: frame.place-switcher, tenancy.concealed.
 */
export const sectorRoute = (pages: Route[]): Route => {
  // A config-driven page, mounted under the segment by the last part of the route its
  // config names.
  const inSector = (page: Route): Route => ({ ...page, path: page.path?.slice(DomainRoutePath.length + 1) });

  return {
    path: DomainRoutePath,
    canActivate: [tenantGuard],
    children: [
      {
        path: 'dashboard',
        loadComponent: (): Promise<Type<DashboardComponent>> =>
          import('@components/ui/dashboard/dashboard.component').then((comp) => comp.DashboardComponent),
      },
      ...pages.map(inSector),
      pageRoute(deckPages.flightDeck, {
        path: 'flight-deck',
        loadComponent: (): Promise<Type<FlightDeckComponent>> =>
          import('./flight-deck/flight-deck.component').then((comp) => comp.FlightDeckComponent),
      }),
      pageRoute(deckPages.hangarDeck, {
        path: 'hangar-deck',
        loadComponent: (): Promise<Type<HangarDeckComponent>> =>
          import('./hangar-deck/hangar-deck.component').then((comp) => comp.HangarDeckComponent),
      }),
      pageRoute(deckPages.roster, {
        path: 'roster',
        loadComponent: (): Promise<Type<RosterComponent>> =>
          import('./roster/roster.component').then((comp) => comp.RosterComponent),
      }),
      pageRoute(deckPages.briefing, {
        path: 'briefing',
        loadComponent: (): Promise<Type<BriefingComponent>> =>
          import('./briefing/briefing.component').then((comp) => comp.BriefingComponent),
      }),
      tenantSignInRoute(apiDescriptor, { auth: 'Crew' }),
      // The person's account and API keys pages: the library's, in every place.
      ...frameRoutes(),
      { path: '**', redirectTo: 'dashboard' },
    ],
  };
};
