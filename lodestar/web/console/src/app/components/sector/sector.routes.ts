import { Type } from '@angular/core';
import { Route } from '@angular/router';
import { Methods, Permissions, Resources } from '@app/service/zz_gen_constants';
import { DomainRoutePath } from '@app/service/zz_gen_resources';
import { frameRoutes, page, pageRoute, tenantGuard } from '@impulseframework/resource-angular/auth-frame';
import { DashboardComponent } from '@components/ui/dashboard/dashboard.component';
import { BriefingComponent } from './briefing/briefing.component';
import { CallLogComponent } from './call-log/call-log.component';
import { FlightDeckComponent } from './flight-deck/flight-deck.component';
import { HangarDeckComponent } from './hangar-deck/hangar-deck.component';
import { HazardBoardComponent } from './hazard-board/hazard-board.component';
import { RosterComponent } from './roster/roster.component';
import { SalvageHoldComponent } from './salvage-hold/salvage-hold.component';
import { SectorSignInComponent } from './sector-sign-in/sector-sign-in.component';
import { ShipsLogComponent } from './ships-log/ships-log.component';
import { WatchDeskComponent } from './watch-desk/watch-desk.component';

const list = Permissions.List;

/**
 * The sector's decks, Lodestar's own pages (see SectorService for why they are
 * hand-written), in the order the Decks menu lists them. Each lives under the sector
 * segment and shows under the List grant its deck reads, asked of the sector's digest;
 * the briefing under Execute on the method that compiles it.
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
  salvageHold: page({
    label: 'Salvage hold',
    icon: 'inventory_2',
    route: [DomainRoutePath, 'salvage-hold'],
    permission: { resource: Resources.Consignments, permission: list },
  }),
  callLog: page({
    label: 'Call log',
    icon: 'call',
    route: [DomainRoutePath, 'call-log'],
    permission: { resource: Resources.DistressCalls, permission: list },
  }),
  roster: page({
    label: 'Squadron roster',
    icon: 'groups',
    route: [DomainRoutePath, 'roster'],
    permission: { resource: Resources.Squadrons, permission: list },
  }),
  hazardBoard: page({
    label: 'Hazard board',
    icon: 'warning',
    route: [DomainRoutePath, 'hazard-board'],
    permission: { resource: Resources.SectorHazardBoards, permission: list },
  }),
  shipsLog: page({
    label: "Ship's log",
    icon: 'history',
    route: [DomainRoutePath, 'ships-log'],
    permission: { resource: Resources.ShipsLogEntries, permission: list },
  }),
  briefing: page({
    label: 'Briefing',
    icon: 'summarize',
    route: [DomainRoutePath, 'briefing'],
    permission: { resource: Methods.CompileBriefing, permission: Permissions.Execute },
  }),
} as const;

/** Who may enter the sector: its declared directories and whether they are the only way in. */
export const sectorSignInPage = page({
  label: 'Sign-in settings',
  icon: 'login',
  route: [DomainRoutePath, 'sign-in'],
  permission: { resource: Resources.SectorDirectories, permission: list },
});

/** The watch desk: every live impersonation in the service, a page of Headquarters. */
export const watchDeskPage = page({
  label: 'Watch desk',
  icon: 'visibility',
  route: ['watch-desk'],
  permission: { resource: Methods.ViewAsUser, permission: Permissions.Execute },
});

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
      pageRoute(deckPages.salvageHold, {
        path: 'salvage-hold',
        loadComponent: (): Promise<Type<SalvageHoldComponent>> =>
          import('./salvage-hold/salvage-hold.component').then((comp) => comp.SalvageHoldComponent),
      }),
      pageRoute(deckPages.callLog, {
        path: 'call-log',
        loadComponent: (): Promise<Type<CallLogComponent>> =>
          import('./call-log/call-log.component').then((comp) => comp.CallLogComponent),
      }),
      pageRoute(deckPages.roster, {
        path: 'roster',
        loadComponent: (): Promise<Type<RosterComponent>> =>
          import('./roster/roster.component').then((comp) => comp.RosterComponent),
      }),
      pageRoute(deckPages.hazardBoard, {
        path: 'hazard-board',
        loadComponent: (): Promise<Type<HazardBoardComponent>> =>
          import('./hazard-board/hazard-board.component').then((comp) => comp.HazardBoardComponent),
      }),
      pageRoute(deckPages.shipsLog, {
        path: 'ships-log',
        loadComponent: (): Promise<Type<ShipsLogComponent>> =>
          import('./ships-log/ships-log.component').then((comp) => comp.ShipsLogComponent),
      }),
      pageRoute(deckPages.briefing, {
        path: 'briefing',
        loadComponent: (): Promise<Type<BriefingComponent>> =>
          import('./briefing/briefing.component').then((comp) => comp.BriefingComponent),
      }),
      pageRoute(sectorSignInPage, {
        path: 'sign-in',
        loadComponent: (): Promise<Type<SectorSignInComponent>> =>
          import('./sector-sign-in/sector-sign-in.component').then((comp) => comp.SectorSignInComponent),
      }),
      // The person's account, API keys and About pages: the library's, in every place.
      ...frameRoutes(),
      { path: '**', redirectTo: 'dashboard' },
    ],
  };
};

/** watchDeskRoute is the watch desk, a page of Headquarters (every live impersonation in the service). */
export const watchDeskRoute = (): Route =>
  pageRoute(watchDeskPage, {
    path: 'watch-desk',
    loadComponent: (): Promise<Type<WatchDeskComponent>> =>
      import('./watch-desk/watch-desk.component').then((comp) => comp.WatchDeskComponent),
  });
