import { apiDescriptor } from '@app/service/zz_gen_api';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { Menu } from '@impulseframework/resource-angular/auth-frame';
import { featureFlagsPage } from '@impulseframework/resource-angular/feature-flags';
import {
  identityMenuPages,
  inviteTenantAction,
  tenantSignInPage,
  tenantsPage,
} from '@impulseframework/resource-angular/identity-pages';

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
 * The frame calls the function once, in an injection context, so an entry of the
 * application's own may use a service it injects.
 */
export function appMenus(): Menu[] {
  return [
    {
      label: 'Tenants',
      groups: [
        {
          // The tenants the application serves, on the library's page, and Invite a tenant…,
          // which opens the invitation on that page. Nobody creates one here: an
          // administrator brings an organization in by inviting one person, from that page,
          // which lists the invitations still waiting above the tenants, and that person's
          // first sign-in makes the tenant.
          entries: [
            tenantsPage(apiDescriptor, resourceMeta, { auth: 'Staff' }),
            inviteTenantAction(apiDescriptor, resourceMeta, { auth: 'Staff' }),
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
      groups: [{ entries: [tenantSignInPage(apiDescriptor, { auth: 'Staff' })] }],
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
            // The feature flags the application declares, each with who last turned it on or
            // off: the library's page, shown under List on the flags, in the place outside
            // every tenant.
            featureFlagsPage(resourceMeta),
          ],
        },
      ],
    },
  ];
}
