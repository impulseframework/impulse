import { Clients, Consignments, Methods, Resources } from '@app/service/zz_gen_constants';
import { ReleaseConsignmentResult } from '@app/service/zz_gen_methods';
import { DomainRoutePath } from '@app/service/zz_gen_resources';
import {
  dayAndMinuteFormat,
  enumeratedConfig,
  field,
  listViewConfig,
  multiColumnConfig,
  RecordData,
  rootConfig,
  section,
} from '@impulseframework/resource-angular/types';
import { format, isValid, parseISO } from 'date-fns';

/** Whether a consignment is still in bond: its record carries no release yet. */
export function inBond(row: RecordData): boolean {
  return !row[Consignments.fieldName.releasedAt];
}

/** What Release asks before it runs, naming the consignment by its bond code. */
export function releaseQuestion(row: RecordData): string {
  return `Release ${String(row[Consignments.fieldName.bondCode] || 'this consignment')} to its owner? It can be released once.`;
}

/** The receipt of a release, said from the method's typed answer: the bond code and when it was released. */
export function releaseReceipt(answer: unknown): { message: string } | undefined {
  const released = answer as Partial<ReleaseConsignmentResult> | undefined;
  if (!released?.bondCode) {
    return undefined;
  }
  // The answer travels as JSON, so the time is the server's text; a caller holding a date gives a date.
  const at = released.releasedAt instanceof Date ? released.releasedAt : parseISO(String(released.releasedAt));
  return {
    message: isValid(at)
      ? `${released.bondCode} released on ${format(at, dayAndMinuteFormat)}`
      : `${released.bondCode} released`,
  };
}

// The salvage hold: cargo in bond until its owner claims it, drawn by the library's list
// over Consignments. Release is the plain located-row method, and cargo is released
// once: the method itself refuses a consignment already released, whoever asks. So a
// row's menu offers Release on cargo in bond alone (inBond), and there only where the
// row's Execute answer lists the method; the Supercargo's grant carries releasedAt IS
// NULL as well, so the server answers her nothing on a released row, while the Sector
// Marshal's grant carries no condition and the server answers hers on every row. The
// method answers a typed receipt, which the page says in the notice that follows it,
// and its question says the release happens once. A consignment is disposed of from
// its own page, where Delete shows by the row's answer: the Supercargo's grant carries a
// date literal, so only bond that expired before it can go.
//
// The hold lists in its declared order, releasedAt desc, a nullable sort column in
// Spanner's own NULL placement: cargo still in bond comes last descending and first
// ascending. A click on the Released header turns the order, and the pager walks across
// the boundary in both directions without a repeat or a skip, a page at a time by the
// size the generated descriptor declares. Mass and Bond expires filter through
// allow_filter: the server accepts them only beside a filter on an indexed column, so
// their filter controls wait until Bond, Owner or Released carries one (Bond "is not
// empty", then Mass "at least 100", lists the heavy cargo).
//
// Demonstrates: paging.nullable-sort, paging.descriptor-sizes, allow_filter, @attribute.date, execute-condition, rpc.typed-result.
export const consignmentsConfig = rootConfig({
  page: { label: 'Salvage hold', icon: 'inventory_2' },
  routeData: { route: `${DomainRoutePath}/salvage-hold` },
  parentConfig: listViewConfig({
    title: 'Salvage hold',
    createTitle: 'Consignment',
    primaryResource: Resources.Consignments,
    emptyMessage: 'Nothing is in the hold.',
    listColumns: [
      { id: Consignments.fieldName.bondCode, header: 'Bond' },
      { id: Consignments.fieldName.description },
      // The owner by name: the column holds the client's id, which nobody reads. A
      // person who may not list the client reads nothing here.
      multiColumnConfig({
        id: Consignments.fieldName.clientId,
        header: 'Owner',
        additionalIds: [{ id: Clients.fieldName.id, resource: Resources.Clients, field: Clients.fieldName.name }],
        concatFn: 'hyphen-concat',
      }),
      { id: Consignments.fieldName.mass, header: 'Mass (t)' },
      { id: Consignments.fieldName.expiresOn, header: 'Bond expires' },
      {
        id: Consignments.fieldName.releasedAt,
        header: 'Released',
        formatType: dayAndMinuteFormat,
        emptyDataValue: 'In bond',
      },
    ],
    rowMethods: [
      {
        method: Methods.ReleaseConsignment,
        label: 'Release',
        icon: 'local_shipping',
        when: inBond,
        confirm: releaseQuestion,
        answered: releaseReceipt,
      },
    ],
    elements: [
      section({
        label: 'Consignment',
        children: [
          field({ name: Consignments.fieldName.bondCode, label: 'Bond code', cols: 4 }),
          field({
            name: Consignments.fieldName.clientId,
            label: 'Owner',
            cols: 4,
            enumeratedConfig: enumeratedConfig({
              listDisplay: [Clients.fieldName.name],
              viewDisplay: [Clients.fieldName.name],
            }),
          }),
          field({ name: Consignments.fieldName.mass, label: 'Mass (t)', cols: 4 }),
          field({ name: Consignments.fieldName.description, label: 'Description', cols: 12 }),
          field({ name: Consignments.fieldName.expiresOn, label: 'Bond expires', cols: 6 }),
          field({ name: Consignments.fieldName.releasedAt, label: 'Released', cols: 6 }),
        ],
      }),
    ],
  }),
});
