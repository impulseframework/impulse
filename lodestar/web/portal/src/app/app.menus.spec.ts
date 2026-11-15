import { unplacedPages } from '@impulseframework/resource-angular/auth-frame';
import { routes } from './app.routes';

describe('the menu file', () => {
  // The portal has no menus of its own: the tracker is the home page, and the frame draws
  // the place switcher, Help and the person's menu. A page with a menu label added to the
  // routes needs a menu here, and this names it.
  it('carries no page the menus would have to place', () => {
    expect(unplacedPages(routes, []).map((page) => page.label)).toEqual([]);
  });
});
