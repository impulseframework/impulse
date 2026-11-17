import { Methods, Resources, IssueBulletin, Sectors } from '@app/service/zz_gen_constants';
import { field, listViewConfig, rootConfig, rpcConfig, section } from '@impulseframework/resource-angular/types';

// Sectors is the tenant record itself: rows here ARE the permission domains, and every
// instance holds a roster of them that keeps up without a restart, so a new sector is
// served at once; the list of every sector here comes from this resource's own generated
// (permission-checked) List. Nobody creates a sector on this page: headquarters and the
// surveyor bring one in by inviting the person who will run it (the crew's Invited
// sectors page, which the library builds), and that person's first sign-in makes the
// sector. The record's metadata says so (createDisabled), and the list draws no Create
// button. A sector's key is an id the server generates, which nobody types: the list
// names a sector, and the row page shows the id read only, for whoever supports the
// application. Issue
// Bulletin is the global, row-free Execute gated for the Bulletin Officer by `now < ...`.
export const sectorsConfig = rootConfig({
  page: { label: 'Sectors', icon: 'public' },
  rpcConfigs: [
    rpcConfig({
      label: 'Issue Bulletin',
      method: Methods.IssueBulletin,
      conditions: [],
      elements: [field({ name: IssueBulletin.fieldName.announcement, label: 'Announcement' })],
      methodBodyTemplate: {},
      successMessage: 'Bulletin issued to every sector',
    }),
  ],
  parentConfig: listViewConfig({
    title: 'Sectors',
    primaryResource: Resources.Sectors,
    listColumns: [
      { id: Sectors.fieldName.name },
      { id: Sectors.fieldName.region },
      { id: Sectors.fieldName.established },
    ],
    elements: [
      section({
        label: 'Sector',
        children: [
          field({ name: Sectors.fieldName.name, label: 'Name', cols: 4 }),
          field({ name: Sectors.fieldName.region, label: 'Region', cols: 4 }),
          field({ name: Sectors.fieldName.established, label: 'Established', cols: 4 }),
          field({ name: Sectors.fieldName.id, label: 'Id', cols: 4, readOnly: true }),
        ],
      }),
    ],
  }),
});
