import { Menu } from '@impulseframework/resource-angular/auth-frame';

/**
 * The application's menu file: the menus across the top, in order, which the library's
 * frame draws on the bar and, on a phone, in the side panel. The portal has no pages of
 * its own yet, so the bar holds Help alone, which the library adds, ending with About and
 * the application's name; the place switcher and the person's own menu (Your account,
 * Appearance, Sign out) show all the same. Each page the portal gains holds its own label,
 * icon, address and the grant it needs, and is placed here; a page added to the routes and
 * forgotten here fails app.menus.spec.ts, naming the page.
 */
export const appMenus: Menu[] = [];
