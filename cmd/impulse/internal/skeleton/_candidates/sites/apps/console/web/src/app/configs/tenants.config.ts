import { Resources, Tenants } from '@app/service/zz_gen_constants';
import { field, listViewConfig, rootConfig, section } from '@impulseframework/resource-angular/types';

// All tenants is the library's list page over the tenant record, in the place outside every
// tenant, for whoever may list tenants: each row is a tenant the application serves, and
// its row page (`tenants/<id>`, the record's address as the API has it) changes its name and
// whether its people enter through its directories only. The menu file places the page
// (app.menus.ts, under Tenants); the record is created through New tenant… beside it.
export const tenantsConfig = rootConfig({
  page: { label: 'All tenants', icon: 'domain' },
  parentConfig: listViewConfig({
    title: 'Tenants',
    createTitle: 'Tenant',
    primaryResource: Resources.Tenants,
    listColumns: [
      { id: Tenants.fieldName.id },
      { id: Tenants.fieldName.name },
      { id: Tenants.fieldName.directoryOnly, header: 'Directory only' },
    ],
    elements: [
      section({
        label: 'Tenant',
        children: [
          field({ name: Tenants.fieldName.id, label: 'Identifier', cols: 4 }),
          field({ name: Tenants.fieldName.name, label: 'Name', cols: 4 }),
          field({ name: Tenants.fieldName.directoryOnly, label: 'Directory only', cols: 4 }),
        ],
      }),
    ],
  }),
});
