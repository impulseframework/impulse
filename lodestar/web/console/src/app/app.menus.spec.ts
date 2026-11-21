// Demonstrates: frame.menu-file.
import { TestBed } from '@angular/core/testing';
import { Menu, unplacedPages } from '@impulseframework/resource-angular/auth-frame';
import { appMenus } from './app.menus';
import { routes } from './app.routes';

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
      'Flight deck',
      'Hangar deck',
      'Squadron roster',
      "Ship's log",
      'Briefing',
    ]);
  });
});
