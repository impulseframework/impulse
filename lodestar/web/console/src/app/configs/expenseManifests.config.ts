import { ExpenseManifests, Resources } from '@app/service/zz_gen_constants';
import { DomainRoutePath } from '@app/service/zz_gen_resources';
import { listViewConfig, rootConfig } from '@impulseframework/resource-angular/types';

// The expense manifests page is the Purser's and the Flight Lead's window on the
// manifests: one row per mission, with how many sorties flew and what they cost, computed
// server-side and drawn by the library's list. The Purser holds the manifests' fields
// outright and is offered the search box and a sort on each column. The Flight Lead holds
// them on the rows of the missions their squadrons fly, under the one condition he holds
// the list under, and is offered the same: the server orders and searches a computed list
// by its rows' real values, and a row he is not shown is dropped before either reads it. A
// role that held a field under any other condition would be offered no sort, no filter and
// no search by it. A manifest has no row page here; the page is the list alone.
export const expenseManifestsConfig = rootConfig({
  page: { label: 'Expense manifests', icon: 'receipt_long' },
  routeData: { route: `${DomainRoutePath}/expense-manifests`, hasViewRoute: false },
  parentConfig: listViewConfig({
    title: 'Expense manifests',
    primaryResource: Resources.ExpenseManifests,
    showViewButton: false,
    emptyMessage: 'No mission in this sector has a manifest yet.',
    listColumns: [
      { id: ExpenseManifests.fieldName.title, header: 'Mission' },
      { id: ExpenseManifests.fieldName.sorties, header: 'Sorties' },
      { id: ExpenseManifests.fieldName.expenses, header: 'Expenses' },
    ],
    elements: [],
  }),
});
