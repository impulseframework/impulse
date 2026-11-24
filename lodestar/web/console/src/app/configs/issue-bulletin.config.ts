import { IssueBulletin, Methods } from '@app/service/zz_gen_constants';
import { TenantPagesOptions } from '@impulseframework/resource-angular/identity-pages';
import { field, rpcConfig } from '@impulseframework/resource-angular/types';

// Issue Bulletin is the global, row-free Execute gated for the Bulletin Officer by
// `now < ...`. It is offered on a sector's page, the library's tenants page (tenantsPage),
// as the console's one method of its own there.
export const issueBulletin = rpcConfig({
  label: 'Issue Bulletin',
  method: Methods.IssueBulletin,
  conditions: [],
  elements: [field({ name: IssueBulletin.fieldName.announcement, label: 'Announcement' })],
  methodBodyTemplate: {},
  successMessage: 'Bulletin issued to every sector',
});

// The sectors page is the library's: every sector with its own fields, its directory, its
// people and since when, the invitations still waiting above the list (nobody creates a
// sector directly: headquarters and the surveyor bring one in by inviting the person who
// will run it, and that person's first sign-in makes the sector), and a sector's page with
// its administrators under its fields. The menu file and the routes pass these same
// options (tenantsPage and tenantsRoute), so both make the one page.
export const sectorsPage: TenantPagesOptions = { auth: 'Crew', rpcConfigs: [issueBulletin] };
