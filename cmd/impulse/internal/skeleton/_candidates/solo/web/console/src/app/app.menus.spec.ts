import { Menu, unplacedPages } from '@impulseframework/resource-angular/auth-frame';
import { appMenus } from './app.menus';
import { routes } from './app.routes';

const menus: Menu[] = appMenus({ featureFlags: () => undefined });

describe('the menu file', () => {
  it('places every page the routes carry', () => {
    expect(unplacedPages(routes, menus).map((page) => page.label)).toEqual([]);
  });

  it('names a page the routes carry and no menu places', () => {
    expect(unplacedPages(routes, []).map((page) => page.label)).toEqual([
      'Accounts',
      'Mailed links',
      'Sessions',
      'Failed sign-ins',
    ]);
  });
});
