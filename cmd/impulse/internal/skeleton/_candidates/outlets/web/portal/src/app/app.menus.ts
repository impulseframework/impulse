import { apiDescriptor } from '@app/service/zz_gen_api';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { Menu } from '@impulseframework/resource-angular/auth-frame';
import { identityMenuPages } from '@impulseframework/resource-angular/identity-pages';

/**
 * The application's menu file: the menus across the top, in order, which the library's
 * frame draws on the bar and, on a phone, in the side panel. Each page holds its own label,
 * icon, address and the grant it needs, and the routes carry the same page; this file says
 * only where each is placed. The menus show what lives in the place the person is in:
 * System in Configuration, the place outside every tenant; People inside a tenant; and
 * each entry only under its grant, so a menu left with nothing is not drawn. The library
 * adds Help last, ending with About and the application's name, and the person's own menu
 * at the right (Appearance and Sign out: a person who signs in through their company's
 * directory has nothing of their sign-in to change here). A page added to the routes and
 * forgotten here fails app.menus.spec.ts, naming the page.
 */
export const appMenus: Menu[] = [
  {
    label: 'People',
    groups: [
      {
        entries: [
          // The tenant's people and its invitations: the pages the library builds for the
          // tenant's side of the Members auth's generated management.
          ...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Members', scope: 'domain' }),
        ],
      },
    ],
  },
  {
    label: 'System',
    groups: [
      {
        entries: [
          // The members' accounts across the whole application, their mailed links,
          // sessions and failed sign-ins.
          ...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Members', scope: 'global' }),
        ],
      },
    ],
  },
];
