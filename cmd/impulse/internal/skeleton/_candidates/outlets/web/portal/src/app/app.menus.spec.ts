import { Menu, unplacedPages } from '@impulseframework/resource-angular/auth-frame';
import { appMenus } from './app.menus';
import { routes } from './app.routes';

describe('the menu file', () => {
  it('places every page the routes carry', () => {
    expect(unplacedPages(routes, appMenus).map((page) => page.label)).toEqual([]);
  });

  it('names a page the routes carry and no menu places', () => {
    const withoutPeople: Menu[] = appMenus.filter((menu) => menu.label !== 'People');
    expect(unplacedPages(routes, withoutPeople).map((page) => page.label)).toEqual(['People', 'Invitations']);
  });
});
