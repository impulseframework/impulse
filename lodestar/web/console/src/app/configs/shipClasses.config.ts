import { Injector } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { Api } from '@app/service/zz_gen_api';
import { Resources, ShipClasses } from '@app/service/zz_gen_constants';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import {
  ColumnConfig,
  field,
  listViewConfig,
  RecordData,
  rootConfig,
  section,
} from '@impulseframework/resource-angular/types';

/** The list's columns, each under the heading the list gives a column that sets none. */
const classColumns: ColumnConfig[] = [
  { id: ShipClasses.fieldName.designation },
  { id: ShipClasses.fieldName.roleId },
  { id: ShipClasses.fieldName.tonnage },
  { id: ShipClasses.fieldName.hardened },
  { id: ShipClasses.fieldName.inService },
];

/**
 * Opens a ship class's history over the page, from its row on the list and from its own
 * page alike: the library's pop-up (`@impulseframework/resource-angular/change-history`),
 * loaded when a person first asks for one, titled with the class's designation. The
 * catalog is outside every sector, so the class's change records are read on the
 * generated client's root, and the pop-up is handed the ShipClasses resource and the
 * list's columns (`classColumns`), so each field reads under the list's heading and each
 * value as the list writes it.
 */
export function openShipClassHistory(row: RecordData, injector: Injector): void {
  const dialog = injector.get(MatDialog);
  const shipClasses = (injector.get(RESOURCE_CLIENT) as Api).shipClasses;
  const id = String(row['id'] ?? '');
  void import('@impulseframework/resource-angular/change-history').then((history) =>
    history.openChangeHistory(dialog, {
      title: `History of ${String(row['designation'] ?? '')}`,
      read: (query) => shipClasses.changes([id], query),
      resource: Resources.ShipClasses,
      columns: classColumns,
    }),
  );
}

// ShipClasses is the hull catalog: the global table Ship's `shipRole` attribute reaches
// through a join path. Designation is immutable. The catalog declares no @order, and
// every paged request carries one, so this page names its sort; without it the server
// refuses the first page naming the resource and the way out.
//
// Each class offers its History, on its row and on its own page: the library's pop-up,
// handed the ShipClasses resource and this list's columns, so a change of Hardened or In
// service reads as the check or the dash the list draws, the tonnage as the list's number,
// and each field under the list's own heading.
//
// Demonstrates: order.required, change-history, change-tracking.
export const shipClassesConfig = rootConfig({
  page: { label: 'Ship classes', icon: 'category' },
  parentConfig: listViewConfig({
    title: 'Ship Classes',
    createTitle: 'Ship Class',
    primaryResource: Resources.ShipClasses,
    sorts: [{ field: ShipClasses.fieldName.designation, direction: 'asc' }],
    listColumns: classColumns,
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
    rowButtons: [{ label: 'History', icon: 'history', run: openShipClassHistory }],
  }),
});
