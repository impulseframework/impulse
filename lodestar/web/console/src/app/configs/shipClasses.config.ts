import { Resources, ShipClasses } from '@app/service/zz_gen_constants';
import { field, listViewConfig, rootConfig, section } from '@impulseframework/resource-angular/types';

// ShipClasses is the hull catalog: the global table Ship's `shipRole` attribute reaches
// through a join path. Designation is immutable. The catalog declares no @order, and
// every paged request carries one, so this page names its sort; without it the server
// refuses the first page naming the resource and the way out.
//
// Demonstrates: order.required.
export const shipClassesConfig = rootConfig({
  page: { label: 'Ship classes', icon: 'category' },
  parentConfig: listViewConfig({
    title: 'Ship Classes',
    createTitle: 'Ship Class',
    primaryResource: Resources.ShipClasses,
    sorts: [{ field: ShipClasses.fieldName.designation, direction: 'asc' }],
    listColumns: [
      { id: ShipClasses.fieldName.designation },
      { id: ShipClasses.fieldName.roleId },
      { id: ShipClasses.fieldName.tonnage },
      { id: ShipClasses.fieldName.hardened },
      { id: ShipClasses.fieldName.inService },
    ],
    elements: [
      section({
        label: 'Hull',
        children: [
          field({ name: ShipClasses.fieldName.designation, label: 'Designation', cols: 3 }),
          field({ name: ShipClasses.fieldName.roleId, label: 'Role', cols: 3 }),
          field({ name: ShipClasses.fieldName.tonnage, label: 'Tonnage', cols: 3 }),
          // Hardened is a yes or no the table never leaves empty and gives no default of its
          // own, so a create must be given it: the create form draws it as a choice of yes or
          // no that starts at No, and a new class is not hardened until its form says so.
          field({ name: ShipClasses.fieldName.hardened, label: 'Radiation hardened', cols: 3 }),
          // In service is a yes or no the table gives a default of its own: the column
          // declares yes, the generated metadata carries it, and the form starts the box
          // checked with nothing written here. A class enters the catalog in service, and a
          // person unchecks the box for a hull the yards no longer build.
          field({ name: ShipClasses.fieldName.inService, label: 'In service', cols: 3 }),
        ],
      }),
    ],
  }),
});
