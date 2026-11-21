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
    createButtonLabel: 'Add a ship class',
    primaryResource: Resources.ShipClasses,
    sorts: [{ field: ShipClasses.fieldName.designation, direction: 'asc' }],
    listColumns: [
      { id: ShipClasses.fieldName.designation },
      { id: ShipClasses.fieldName.roleId },
      { id: ShipClasses.fieldName.tonnage },
      { id: ShipClasses.fieldName.hardened },
    ],
    elements: [
      section({
        label: 'Hull',
        children: [
          field({ name: ShipClasses.fieldName.designation, label: 'Designation', cols: 3 }),
          field({ name: ShipClasses.fieldName.roleId, label: 'Role', cols: 3 }),
          field({ name: ShipClasses.fieldName.tonnage, label: 'Tonnage', cols: 3 }),
          // Hardened is a yes or no the table never leaves empty and gives no default of its
          // own, so the form starts it unchecked: a new class is not hardened until its form
          // says so.
          field({ name: ShipClasses.fieldName.hardened, label: 'Radiation hardened', cols: 3 }),
        ],
      }),
    ],
  }),
});
