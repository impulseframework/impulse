import { TestBed } from '@angular/core/testing';
import { Menu, unplacedPages } from '@impulseframework/resource-angular/auth-frame';
import { appMenus } from './app.menus';
import { routes } from './app.routes';

/** The menu file as the frame reads it: called once, in an injection context. */
function menus(): Menu[] {
  return TestBed.runInInjectionContext(() => appMenus({ featureFlags: () => undefined }));
}

describe('the menu file', () => {
  it('places every page the routes carry', () => {
    expect(unplacedPages(routes, menus()).map((page) => page.label)).toEqual([]);
  });

  it('names a page the routes carry and no menu places', () => {
    const withoutSystem = menus().filter((menu) => menu.label !== 'System');
    expect(unplacedPages(routes, withoutSystem).map((page) => page.label)).toEqual([
      'Accounts',
      'Groups',
      'Mailed links',
      'Sessions',
      'Failed sign-ins',
    ]);
  });
});
