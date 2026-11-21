import { Resources, SectorHazardBoards } from '@app/service/zz_gen_constants';
import { DomainRoutePath, SectorHazardBoards as HazardRow } from '@app/service/zz_gen_resources';
import { dayAndMinuteFormat, listViewConfig, rootConfig } from '@impulseframework/resource-angular/types';
import { format, isValid, parseISO } from 'date-fns';

/** How bad a reading is, in a word: the two lines the board has always drawn its colors at. */
export function severityOf(reading: number): string {
  if (reading >= 0.8) {
    return 'critical';
  }
  if (reading >= 0.5) {
    return 'elevated';
  }
  return 'nominal';
}

/** The worst reading with its severity beside it, as the cell reads: `0.95, critical`. */
export function worstReadingInWords(row: HazardRow): string {
  return row.worstReading === undefined || row.worstReading === null
    ? ''
    : `${row.worstReading}, ${severityOf(row.worstReading)}`;
}

/** One reading with its severity and when it was recorded: `0.4, nominal (Oct 2, 2026, 1:05 PM)`. */
function readingInWords(reading: HazardRow.Reading): string {
  const words = `${reading.value}, ${severityOf(reading.value)}`;
  // The row travels as JSON, so the time is the server's text; a caller holding a date gives a date.
  const at = reading.recordedAt instanceof Date ? reading.recordedAt : parseISO(String(reading.recordedAt));
  return isValid(at) ? `${words} (${format(at, dayAndMinuteFormat)})` : words;
}

/**
 * The readings behind the worst one, newest first as the server sent them, each with its
 * severity and when it was recorded:
 * `0.95, critical (Oct 2, 2026, 2:05 PM); 0.4, nominal (Oct 2, 2026, 1:05 PM)`.
 */
export function recentReadingsInWords(row: HazardRow): string {
  return (row.recent ?? []).map(readingInWords).join('; ');
}

// The hazard board is the only human window onto droid telemetry: the worst reading per
// ship and subsystem, computed server-side from DroidReports that have no browser route
// at all, drawn by the library's list. The Hazard Analyst's List grant carries a row-free
// expiry, so the board itself goes dark when the certification lapses: the list then
// shows the server's refusal in the server's words. Each row also carries the recent
// readings behind the worst one, a nested field the generator typed as
// SectorHazardBoards.Reading, opaque to filtering and to sorting and selected whole: the
// cell writes each with its severity and when it was recorded, in the order the server
// sent them (the list cuts a cell wider than its column and offers the whole of it under
// the pointer or the keyboard's focus), and its header offers no sort. A row is one
// ship's one subsystem, a compound key, so the list draws no row page; the board
// declares no create, update or delete, and the page offers none. The search box and the
// Ship and Subsystem filters are the resource's own, answered by the fold.
// The board pages by the size its descriptor declares; `limit=all`, legal here because
// the board declares no maximum, is the served suite's to ask.
//
// Demonstrates: computed.fold, rpc.nested-shape, computed.conditional-grant, machine-identity, paging.descriptor-sizes.
export const sectorHazardBoardsConfig = rootConfig({
  page: { label: 'Hazard board', icon: 'warning' },
  routeData: { route: `${DomainRoutePath}/hazard-board`, hasViewRoute: false },
  parentConfig: listViewConfig({
    title: 'Hazard board',
    primaryResource: Resources.SectorHazardBoards,
    showViewButton: false,
    emptyMessage: 'No droid has reported a reading in this sector yet.',
    listColumns: [
      { id: SectorHazardBoards.fieldName.shipName, header: 'Ship' },
      { id: SectorHazardBoards.fieldName.subsystem, header: 'Subsystem' },
      { id: SectorHazardBoards.fieldName.worstReading, header: 'Worst reading', valueGetter: worstReadingInWords },
      { id: SectorHazardBoards.fieldName.recordedAt, header: 'Recorded', formatType: dayAndMinuteFormat },
      {
        id: SectorHazardBoards.fieldName.recent,
        header: 'Recent readings',
        valueGetter: recentReadingsInWords,
        sortable: false,
      },
    ],
    elements: [],
  }),
});
