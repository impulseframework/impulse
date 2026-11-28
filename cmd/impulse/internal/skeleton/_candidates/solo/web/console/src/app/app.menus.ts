import { apiDescriptor } from '@app/service/zz_gen_api';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { Menu } from '@impulseframework/resource-angular/auth-frame';
import { featureFlagsPage } from '@impulseframework/resource-angular/feature-flags';
import { identityMenuPages } from '@impulseframework/resource-angular/identity-pages';

/**
 * The application's menu file: the menus across the top, in order, which the library's
 * frame draws on the bar and, on a phone, in the side panel. Each page holds its own label,
 * icon, address and the grant it needs, and the routes carry the same page; this file says
 * only where each is placed. Each entry shows only under its grant, so a menu left with
 * nothing is not drawn. The library adds Help last, ending with About and the
 * application's name, and the person's own menu at the right (Your account, Appearance,
 * Sign out). A page added to the routes and forgotten here fails app.menus.spec.ts, naming
 * the page.
 *
 * The frame calls the function once, in an injection context, so an entry of the
 * application's own may use a service it injects.
 */
export function appMenus(): Menu[] {
  return [
    {
      label: 'System',
      groups: [
        {
          entries: [
            // The staff's accounts, their mailed links, sessions, failed sign-ins and audit log.
            ...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'global' }),
          ],
        },
        {
          entries: [
            // The feature flags the application declares, each with who last turned it on or
            // off: the library's page, shown under List on the flags.
            featureFlagsPage(resourceMeta),
          ],
        },
      ],
    },
  ];
}
