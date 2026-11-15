import { inject } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { apiDescriptor } from '@app/service/zz_gen_api';
import { Permissions, Resources } from '@app/service/zz_gen_constants';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { FrameActions, Menu } from '@impulseframework/resource-angular/auth-frame';
import { identityMenuPages } from '@impulseframework/resource-angular/identity-pages';
import { resourcePage } from '@impulseframework/resource-angular/resource-route-generator';
import { openNewTenantDialog } from '@components/ui/new-tenant/new-tenant-dialog.component';
import { tenantSignInPage } from '@components/ui/sign-in/sign-in.routes';
import { tenantsConfig } from './configs/tenants.config';

/**
 * The application's menu file: the menus across the top, in order, which the library's
 * frame draws on the bar and, on a phone, in the side panel. Each page holds its own label,
 * icon, address and the grant it needs, and the routes carry the same page; this file says
 * only where each is placed. The menus show what lives in the place the person is in:
 * Tenants and System in Configuration, the place outside every tenant; People and Tenant
 * inside a tenant; and each entry only under its grant, so a menu left with nothing is not
 * drawn. The library adds Help last, ending with About and the application's name, and the
 * person's own menu at the right (Your account, Appearance, Sign out). A page added to the
 * routes and forgotten here fails app.menus.spec.ts, naming the page.
 *
 * The frame calls the function once, in an injection context, handing it what the frame
 * does (the feature flags dialog), so an entry of the application's own may use a service
 * it injects: New tenant… opens the new-tenant dialog.
 */
export function appMenus(actions: FrameActions): Menu[] {
  const dialog = inject(MatDialog);
  return [
    {
      label: 'Tenants',
      groups: [
        {
          entries: [
            resourcePage(tenantsConfig, resourceMeta),
            {
              label: 'New tenant…',
              icon: 'add_business',
              run: () => void openNewTenantDialog(dialog),
              place: 'outside',
              permission: { resource: Resources.Tenants, permission: Permissions.Create },
            },
          ],
        },
      ],
    },
    {
      label: 'People',
      groups: [
        {
          entries: [
            // The tenant's people and its invitations: the pages the library builds for the
            // tenant's side of the Staff auth's generated management.
            ...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'domain' }),
          ],
        },
      ],
    },
    {
      label: 'Tenant',
      groups: [{ entries: [tenantSignInPage] }],
    },
    {
      label: 'System',
      groups: [
        {
          entries: [
            // The staff's accounts across the whole application, their mailed links,
            // sessions and failed sign-ins.
            ...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'global' }),
          ],
        },
        {
          entries: [
            {
              label: 'Feature flags…',
              icon: 'flag',
              run: () => actions.featureFlags(),
              place: 'outside',
              permission: { resource: Resources.FeatureFlags, permission: Permissions.List },
            },
          ],
        },
      ],
    },
  ];
}
