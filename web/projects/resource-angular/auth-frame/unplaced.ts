import { Routes } from '@angular/router';
import { isPage, Menu, MenuGroup, Page, pageDataKey } from './menu';

/**
 * The pages the routes declare (pageRoute, resourceRoutes) that the menu file places
 * nowhere, in the order the routes declare them: a page given a label and an icon for the
 * menus that no menu reaches. An application ships a test that expects none:
 *
 *     expect(unplacedPages(routes, menus).map((page) => page.label)).toEqual([]);
 *
 * A page placed in two menus is the application's choice and passes. A route that carries
 * no page (the device approval page, the refusal page) is not a menu's, and passes too. The
 * routes are walked through their children; a lazily loaded child list (loadChildren) is
 * not loaded, so its pages are not seen.
 */
export function unplacedPages(routes: Routes, menus: readonly Menu[]): Page[] {
  const placed = new Set<Page>();
  const place = (groups: readonly MenuGroup[]): void => {
    for (const group of groups) {
      for (const entry of group.entries) {
        if (isPage(entry)) {
          placed.add(entry);
        } else if ('groups' in entry) {
          place(entry.groups);
        }
      }
    }
  };
  for (const menu of menus) {
    place(menu.groups);
  }

  const unplaced: Page[] = [];
  const walk = (list: Routes): void => {
    for (const route of list) {
      const declared = route.data?.[pageDataKey] as Page | undefined;
      if (declared && !placed.has(declared) && !unplaced.includes(declared)) {
        unplaced.push(declared);
      }
      walk(route.children ?? []);
    }
  };
  walk(routes);

  return unplaced;
}
