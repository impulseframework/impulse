// Demonstrates: frame.menu-file.
import { TestBed } from '@angular/core/testing';
import { Route } from '@angular/router';
import { apiDescriptor } from '@app/service/zz_gen_api';
import { Resources } from '@app/service/zz_gen_constants';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { isPage, Menu, pageDataKey, unplacedPages } from '@impulseframework/resource-angular/auth-frame';
import { tenantsConfig } from '@impulseframework/resource-angular/identity-pages';
import { ListViewConfig } from '@impulseframework/resource-angular/types';
import { appMenus } from './app.menus';
import { routes } from './app.routes';
import { issueBulletin, sectorsPage } from './configs/issue-bulletin.config';

/** The menu file as the frame reads it: called once, in an injection context. */
function menus(): Menu[] {
  return TestBed.runInInjectionContext(() => appMenus());
}

describe('the menu file', () => {
  it('places every page the routes carry', () => {
    expect(unplacedPages(routes, menus()).map((page) => page.label)).toEqual([]);
  });

  it('names a page the routes carry and no menu places', () => {
    const withoutDecks = menus().filter((menu) => menu.label !== 'Decks');
    expect(unplacedPages(routes, withoutDecks).map((page) => page.label)).toEqual([
      'Salvage hold',
      'Call log',
      'Hazard board',
      'Expense manifests',
      'Flight deck',
      'Hangar deck',
      'Squadron roster',
      "Ship's log",
      'Briefing',
    ]);
  });

  it("places All sectors in Records: the library's page over the crew's list of sectors, the invitations waiting above it, and Issue Bulletin on a sector's page", () => {
    const records = menus().find((menu) => menu.label === 'Records');
    const placed = (records?.groups ?? [])
      .flatMap((group) => group.entries)
      .filter(isPage)
      .find((entry) => entry.label === 'All sectors');
    expect(placed?.route).toEqual(['sectors']);
    // The route the page lives at carries the same page: the menu file and the routes pass the same options.
    const carried = (found: readonly Route[]): Route[] =>
      found.flatMap((route) => [
        ...(route.data?.[pageDataKey] === placed ? [route] : []),
        ...carried(route.children ?? []),
      ]);
    expect(carried(routes).map((route) => route.path)).toEqual(['sectors']);

    const config = tenantsConfig(apiDescriptor, resourceMeta, sectorsPage);
    const list = config.parentConfig as ListViewConfig;
    expect([list.primaryResource, list.title]).toEqual([Resources.CrewListedSectors, 'Sectors']);
    expect(list.listColumns.filter((column) => !column.hidden).map((column) => column.header)).toEqual([
      'Name',
      'Region',
      'Established',
      'Directory',
      'Directory only',
      'People',
      'Since',
    ]);
    expect(list.above?.map((above) => [above.primaryResource, above.title])).toEqual([
      [Resources.CrewInvitedSectors, 'Invited sectors, waiting for a first sign-in'],
    ]);
    expect(config.rpcConfigs).toEqual([issueBulletin]);
  });
});
