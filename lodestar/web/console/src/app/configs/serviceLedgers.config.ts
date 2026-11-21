import { Resources, ServiceLedgers } from '@app/service/zz_gen_constants';
import { listViewConfig } from '@impulseframework/resource-angular/types';

// The service ledger is a computed resource, one row per sector: the missions open in it,
// the fees it is still owed, and the settlements made. It is a list of records, so the
// library's list draws it, as a section of the dashboard, for a person the digest grants
// List on it (the Governor). The ledger declares no read of one row and no create, update
// or delete, so a row opens no page and the list offers none; the search box and the
// header filters are the resource's own.
export const serviceLedgerList = listViewConfig({
  title: 'Service ledger',
  primaryResource: Resources.ServiceLedgers,
  showViewButton: false,
  emptyMessage: 'No sector has anything on its ledger yet.',
  listColumns: [
    { id: ServiceLedgers.fieldName.name, header: 'Sector' },
    { id: ServiceLedgers.fieldName.openMissions, header: 'Open missions' },
    { id: ServiceLedgers.fieldName.feesOutstanding, header: 'Fees outstanding' },
    { id: ServiceLedgers.fieldName.settlements, header: 'Settlements' },
  ],
  elements: [],
});
