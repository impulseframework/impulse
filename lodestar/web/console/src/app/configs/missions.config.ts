import { Injector } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { Api, apiDescriptor } from '@app/service/zz_gen_api';
import { BriefingTemplates, ClientRosters, MissionBoards, Missions, Resources } from '@app/service/zz_gen_constants';
import { DomainRoutePath, resourceMeta } from '@app/service/zz_gen_resources';
import { sharingConfig } from '@impulseframework/resource-angular/identity-pages';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import {
  ColumnConfig,
  enumeratedConfig,
  field,
  FieldElement,
  listViewConfig,
  RecordData,
  RESOURCE_DOMAIN,
  rootConfig,
  section,
} from '@impulseframework/resource-angular/types';

/** The fields of a mission's page, the call sheet, with the labels the page gives them. */
const callSheet: FieldElement[] = [
  field({ name: Missions.fieldName.title, label: 'Title', cols: 6 }),
  field({
    name: Missions.fieldName.clientId,
    label: 'Client',
    cols: 6,
    enumeratedConfig: enumeratedConfig({
      listDisplay: [ClientRosters.fieldName.name, ClientRosters.fieldName.contactCount],
      viewDisplay: [ClientRosters.fieldName.name, ClientRosters.fieldName.contactCount],
      listConcatFn: 'hyphen-concat',
      viewConcatFn: 'hyphen-concat',
      sorts: [{ field: ClientRosters.fieldName.name, direction: 'asc' }],
      // The roster is paged and projects the clients' token column on Name, so the
      // picker's panel carries the search box: typing narrows the page on the server.
      searchable: true,
    }),
  }),
  field({ name: Missions.fieldName.kindId, label: 'Kind', cols: 4 }),
  field({ name: Missions.fieldName.hazard, label: 'Hazard', cols: 4 }),
  field({ name: Missions.fieldName.fee, label: 'Fee', cols: 4 }),
  field({ name: Missions.fieldName.deadline, label: 'Deadline', cols: 6 }),
  field({
    name: Missions.fieldName.briefingTemplateId,
    label: 'Briefing template',
    cols: 6,
    enumeratedConfig: enumeratedConfig({
      listDisplay: [BriefingTemplates.fieldName.name],
      viewDisplay: [BriefingTemplates.fieldName.name],
      searchable: true,
    }),
  }),
  field({ name: Missions.fieldName.statusId, label: 'Status', cols: 4 }),
  field({ name: Missions.fieldName.brief, label: 'Brief', cols: 12 }),
  field({ name: Missions.fieldName.notes, label: 'Notes', cols: 12 }),
];

/** The list's columns: the board's fields, under the headings the list gives them. */
const boardColumns: ColumnConfig[] = [
  { id: MissionBoards.fieldName.title },
  { id: MissionBoards.fieldName.clientId, header: 'Client' },
  { id: MissionBoards.fieldName.squadronId, header: 'Squadron' },
  { id: MissionBoards.fieldName.statusId, header: 'Status' },
  { id: MissionBoards.fieldName.briefingTemplateId, header: 'Briefing' },
  { id: MissionBoards.fieldName.deadline },
  { id: MissionBoards.fieldName.daysLeft, header: 'Days left' },
];

/**
 * The columns a mission's history names its fields by: the board's, then a column for each
 * field of the call sheet the board does not show, under the label the page gives it.
 */
const historyColumns: ColumnConfig[] = [
  ...boardColumns,
  { id: Missions.fieldName.kindId, header: 'Kind' },
  { id: Missions.fieldName.hazard, header: 'Hazard' },
  { id: Missions.fieldName.fee, header: 'Fee' },
  { id: Missions.fieldName.brief, header: 'Brief' },
  { id: Missions.fieldName.notes, header: 'Notes' },
];

/**
 * Opens a mission's history over the page, from its row on the list and from its own
 * page alike: the library's pop-up (`@impulseframework/resource-angular/change-history`),
 * loaded when a person first asks for one, titled with the mission's name. It reads the
 * mission's change records in the open sector, and hands the pop-up the Missions resource
 * and the page's columns (`historyColumns`: the list's, then the call sheet's other
 * fields), so each field is named and each value written as the list does, the deadline as
 * the list's Deadline cell writes a moment, and a field no column is on (the assigned
 * squadron) by its name in words; the fields a person can narrow the history to are the
 * ones the columns are on. Where a change record kept the name of the row a field pointed
 * at (the squadron, the status), From and To read the name as it was then.
 *
 * Demonstrates: change-history, change-history.names, change-tracking, event-source.
 */
export function openMissionHistory(row: RecordData, injector: Injector): void {
  const dialog = injector.get(MatDialog);
  const sector = injector.get(RESOURCE_DOMAIN)();
  if (sector === undefined) {
    throw new Error('no sector open');
  }
  const missions = (injector.get(RESOURCE_CLIENT) as Api).domain(sector).missions;
  const id = String(row['id'] ?? '');
  void import('@impulseframework/resource-angular/change-history').then((history) =>
    history.openChangeHistory(dialog, {
      title: `History of ${String(row['title'] ?? '')}`,
      read: (query) => missions.changes([id], query),
      resource: Resources.Missions,
      columns: historyColumns,
    }),
  );
}

// Missions is the config-driven page in the open sector: the library's list, form,
// and pickers over a domain-scoped resource (RESOURCE_DOMAIN is the sector the URL
// names), where the flight deck is the hand-written deck over the same rows. The list
// is the MissionBoards view, which carries what Missions lacks: the client's name, the
// squadron's name, and the days left, computed in the SQL. The view declares its
// backing table, a create goes into the table, and the new row shows up in the view on
// the next list because the view's SQL reads that table: the page names the view alone,
// and the metadata's rowsOf sends its create, edit, and delete to Missions, whose form
// the elements build, and opens a row on the mission's own page by the key the view
// carries under the table's column name. Its two declared pickers list exactly what the
// generated metadata names — ClientRosters for the client key, a sector-scoped view
// over Clients carrying the contact count, and BriefingTemplates for the plain template
// column, a computed catalog with no read route, so a picked template's name resolves
// from the option list — and the configuration only narrows and presents those rows. A
// persona without List on the view sees that request refused, never a substitute
// resource. A persona with nothing on a picker's resource (the liaison in Cinder, an
// Archivist, who reads a mission and not the client roster) is asked nothing of it: the
// Client field shows the stored id, read-only, and says she has no access to that list.
//
// The list is paged by the server, and only there: the page asks for one page of the
// board at the size the view's descriptor declares (no pageSize here, so the @page
// default applies), the grid shows it with First, Previous, and Next following the
// server's cursors and the first page's total kept while turning, and a header click or
// a column filter is a request parameter, so the server orders and narrows every row of
// the board. The filter controls follow the generated metadata: the indexed columns
// (title, client, squadron, status, deadline) filter always, and days left, computed in
// the SQL and indexed by nothing but declared allow_filter, filters only beside one of
// them — its control waits, with a hint, until an indexed filter is in the request, and
// clearing the last indexed filter clears it too. The Briefing column is on an id whose
// resource declares no name, so it shows the stored key, sorts by it, and filters by it
// as days left does, beside an indexed filter. It is drawn for a role that lists the
// key on the board, the sector marshal alone: the dispatcher reads a mission's template
// until the mission is completed, failed or stood down, and the board lists those
// missions too, so the dispatcher's board carries no Briefing column. A page size over
// the view's maximum, or a filter the server refuses, is shown in the server's words.
//
// The two pickers read on their sources' maximums, from the generated descriptor. The
// client roster declares one, so the client picker pages the roster one server page at
// a time, Previous and Next inside the panel, in the roster's own @order, and reads the
// chosen client by the roster's key. The briefing template catalog declares none, so the
// template picker reads it whole with limit=all and resolves the chosen sheet from that
// list, which is why the catalog needs no read route.
//
// The board's box sits above the grid, beside the column filters, and both narrow one
// request: the page sends the complete column filters and the box as conjuncts of one
// AND, `(statusId:eq:open),*:search:"survey"`, so the box, which searches the mission's
// title, and the filters narrow the same page, with its count and its declared order. A
// client is found by the Client column's own filter, which matches the client's name or
// part of it. The box counts as the indexed touch the days-left control waits for, so
// a days-left filter rides beside the box alone and leaves when the last indexed touch,
// the box or a status filter, is cleared. The deadline's menu takes a date as a person
// writes it, 2026-12-15 or 12/15/2026, a month or a minute, and the server reads the
// whole span it names in the person's zone, which the browser sends with the request.
//
// Demonstrates: picker.config-driven, picker.paged, picker.whole, @enumerate.plain-column, @enumerate.key-view, picker.read-disabled, @rowsOf.same-row, list.server-paged, metadata.filterable, search.beside-filters, filter.written-dates.
// A mission's Sharing, under its call sheet: who holds a role on the mission alone, a person
// or a group of the sector, with the buttons that share it and Remove on each row, built
// from the crew's generated management (sharingConfig). A caller whose grants list none of
// it sees no list, and one who may not share sees the list without its buttons.
//
// Demonstrates: @holdsRoles.
const missionSharing = sharingConfig(apiDescriptor, resourceMeta, { auth: 'Crew', record: Resources.Missions });

export const missionsConfig = rootConfig({
  page: { label: 'Missions', icon: 'flag' },
  routeData: { route: `${DomainRoutePath}/missions` },
  parentConfig: listViewConfig({
    title: 'Missions',
    createTitle: 'Mission',
    primaryResource: Resources.MissionBoards,
    listColumns: boardColumns,
    elements: [
      section({
        label: 'Call sheet',
        children: callSheet,
      }),
    ],
    rowButtons: [{ label: 'History', icon: 'history', run: openMissionHistory }],
  }),
  relatedConfigs: missionSharing ? [missionSharing] : [],
});
