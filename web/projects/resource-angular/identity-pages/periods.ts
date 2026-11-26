// The period of an assignment, as the pages over an Auth's people ask for it and show it:
// a role given for a set time, on the people pages and on a record's Sharing list.

/** The words for the two times of a method that gives something for a period. */
export const periodWords: Record<string, string> = { startsAt: 'From', endsAt: 'Until' };

/**
 * The fields that hold the end of a period. The server keeps an end as the first
 * instant the assignment no longer counts, and a person picks and reads a day: "until
 * 1 December" is through the end of 1 December. So a method's dialog sends the day
 * picked as the first instant of the day after it, in the viewer's time zone (the
 * method's `throughDay`), and a list shows the last day the assignment counts (lastDay).
 */
export const periodEnds: readonly string[] = ['endsAt'];

/**
 * The last day a period that ends at the instant counts, as a list shows it: the day of
 * the instant before the end, in the viewer's time zone, which the list writes as a
 * calendar date. For an end a dialog of these pages sent it is the day that was picked,
 * and for any other instant the last day some of the period falls on. Empty for no end.
 */
export function lastDay(end: unknown): string {
  if (typeof end !== 'string' && !(end instanceof Date)) {
    return '';
  }
  const instant = new Date(end).getTime();
  return Number.isNaN(instant) ? '' : new Date(instant - 1).toISOString();
}
