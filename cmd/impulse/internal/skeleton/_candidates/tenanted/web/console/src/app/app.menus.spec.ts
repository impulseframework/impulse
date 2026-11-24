import { TestBed } from '@angular/core/testing';
import { Methods } from '@app/service/zz_gen_constants';
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

  it("places the staff's pages for the whole application under System, and no page of the invitations that make a tenant", () => {
    // The library builds the pages from the generated description, which names the list of
    // the invitations that make a tenant: that list is no page of these, but is shown on All
    // tenants, above the tenants.
    const system = menus().find((menu) => menu.label === 'System');
    expect((system?.groups ?? []).flatMap((group) => group.entries).map((entry) => entry.label)).toEqual([
      'Accounts',
      'Groups',
      'Mailed links',
      'Sessions',
      'Failed sign-ins',
      'Sign-in',
      'Break glass',
      'Feature flags',
    ]);
  });

  it("places Invite a tenant… after All tenants: an action outside every tenant that opens the page with the invitation's dialog", () => {
    const tenants = menus().find((menu) => menu.label === 'Tenants');
    const entries = (tenants?.groups ?? []).flatMap((group) => group.entries);
    expect(entries.map((entry) => entry.label)).toEqual(['All tenants', 'Invite a tenant…']);
    const action = entries[1];
    expect(action && 'run' in action ? [action.icon, action.place, action.permission] : action).toEqual([
      'send',
      'outside',
      { resource: Methods.InviteStaffTenant, permission: 'Execute' },
    ]);
  });
});
