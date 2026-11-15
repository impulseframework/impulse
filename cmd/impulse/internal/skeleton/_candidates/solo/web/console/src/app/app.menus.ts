import { apiDescriptor } from '@app/service/zz_gen_api';
import { Permissions, Resources } from '@app/service/zz_gen_constants';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { FrameActions, Menu } from '@impulseframework/resource-angular/auth-frame';
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
 * The frame calls the function once, in an injection context, handing it what the frame
 * does: here the feature flags dialog.
 */
export function appMenus(actions: FrameActions): Menu[] {
  return [
    {
      label: 'System',
      groups: [
        {
          entries: [
            // The staff's accounts, their mailed links, sessions and failed sign-ins.
            ...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'global' }),
          ],
        },
        {
          entries: [
            {
              label: 'Feature flags…',
              icon: 'flag',
              run: () => actions.featureFlags(),
              permission: { resource: Resources.FeatureFlags, permission: Permissions.List },
            },
          ],
        },
      ],
    },
  ];
}
