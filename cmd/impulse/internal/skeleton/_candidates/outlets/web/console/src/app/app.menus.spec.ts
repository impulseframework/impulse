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
    const withoutTenant = menus().filter((menu) => menu.label !== 'Tenant');
    expect(unplacedPages(routes, withoutTenant).map((page) => page.label)).toEqual(['Sign-in settings']);
  });

  it("places the Invited tenants page among the staff's pages under System, where a tenant is brought in", () => {
    // The library builds the page from the generated description, which names the list of
    // the invitations that make a tenant; the routes carry it and System places it.
    const withoutSystem = menus().filter((menu) => menu.label !== 'System');
    expect(unplacedPages(routes, withoutSystem).map((page) => page.label)).toContain('Invited tenants');
  });
});
