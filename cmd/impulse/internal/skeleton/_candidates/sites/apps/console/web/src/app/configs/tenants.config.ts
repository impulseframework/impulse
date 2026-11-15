import { Resources, Tenants } from '@app/service/zz_gen_constants';
import { field, listViewConfig, rootConfig, section } from '@impulseframework/resource-angular/types';

// All tenants is the library's list page over the tenant record, in the place outside every
// tenant, for whoever may list tenants: each row is a tenant the application serves, by its
// name, and its row page (`tenants/<id>`, the record's address as the API has it) changes
// its name and whether its people enter through its directories only. A tenant's key is an
// id the server generates, which nobody types: the list leaves it out, and the row page
// shows it read only, for whoever supports the application. The menu file places the page
// (app.menus.ts, under Tenants); the record is created through New tenant… beside it.
export const tenantsConfig = rootConfig({
  page: { label: 'All tenants', icon: 'domain' },
  parentConfig: listViewConfig({
    title: 'Tenants',
    createTitle: 'Tenant',
    primaryResource: Resources.Tenants,
    listColumns: [{ id: Tenants.fieldName.name }, { id: Tenants.fieldName.directoryOnly, header: 'Directory only' }],
    elements: [
      section({
        label: 'Tenant',
        children: [
          field({ name: Tenants.fieldName.name, label: 'Name', cols: 4 }),
          field({ name: Tenants.fieldName.directoryOnly, label: 'Directory only', cols: 4 }),
          field({ name: Tenants.fieldName.id, label: 'Id', cols: 4, readOnly: true }),
        ],
      }),
    ],
  }),
});
