import { CrewSessionImpersonations, Methods, Resources } from '@app/service/zz_gen_constants';
import {
  dayAndMinuteFormat,
  listViewConfig,
  RecordData,
  roleInWords,
  rootConfig,
} from '@impulseframework/resource-angular/types';
import { format, isValid, parseISO } from 'date-fns';

const record = CrewSessionImpersonations.fieldName;

/** A moment a record carries, as it travels (text) or as a caller holds it (a date); nothing where it carries none. */
function moment(value: unknown): Date | undefined {
  if (!value) {
    return undefined;
  }
  const at = value instanceof Date ? value : parseISO(String(value));
  return isValid(at) ? at : undefined;
}

/** Whether the time a session must end by has passed, by the reader's clock as the page was read. */
function timeRanOut(row: RecordData): boolean {
  const end = moment(row[record.expiresAt]);
  return !!end && end.getTime() <= Date.now();
}

/**
 * Whether a session held as someone else is still open: its record has no end yet, and
 * the time it must end by has not passed. The record of a session nobody used after its
 * time ran out never gets an end (the end is written when the session's next request is
 * refused), and nothing is left of it to revoke.
 */
export function stillOpen(row: RecordData): boolean {
  return !row[record.endedAt] && !timeRanOut(row);
}

/**
 * What the Ended column says: when the session ended; "Still open" while it has no end
 * and its time has not passed; and "Time ran out" for a session whose time passed with no
 * end written, so no row reads as open after the time it had to end by.
 */
export function ended(row: RecordData): string {
  const at = moment(row[record.endedAt]);
  if (at) {
    return format(at, dayAndMinuteFormat);
  }
  return timeRanOut(row) ? 'Time ran out' : 'Still open';
}

/**
 * Who a session is held as, where the record keeps no username: the role in words that
 * say it is a role ("the Dispatcher role", "the Sector marshal role") for a session
 * held as a role, and the id the record keeps for a person.
 */
export function heldAs(row: RecordData): string {
  const role = row[record.principalRole];
  if (role) {
    return `the ${roleInWords(String(role))} role`;
  }
  return String(row[record.principalUserId] || '');
}

/** What Revoke asks before it ends a session, naming who holds it. */
export function revokeQuestion(row: RecordData): string {
  return `End the session ${String(row[record.actorUsername] || 'this person')} holds as someone else? Their next request is refused.`;
}

// The watch desk: the sessions people hold as someone else across the whole service,
// drawn by the library's list over the crew auth's generated impersonation records,
// newest first as the resource orders them. Each row says who established the session,
// as whom (the username the record keeps for the person, or "the Dispatcher role" for a
// role), what it may do (its mask, in the server's words),
// why, when it began, when its two-hour cap ends it, and when and why it ended; a
// session that has not ended reads "Still open", and one whose cap passed with no end
// written reads "Time ran out". A row's menu offers Revoke while the session is open
// and the server's Execute answer for the row lists the generated method that ends it:
// the revoked console's next request is refused and its banner explains. The page shows
// under List on the records and the menu under Execute on the method, both the role
// file's to give (the command staff hold them, so the marshal keeps the desk as the
// governor does).
//
// The list is live, and a revoke from a desk reaches every desk that is open, since the
// generated method publishes the record it wrote. A session started elsewhere, and one
// its holder ends, are written by the session library's own store, which tells the
// change feed nothing: those show when the desk is next read (opened again, sorted,
// filtered, or after a revoke).
//
// Demonstrates: impersonation.active-list, impersonation.revoke, impersonation.max-duration, list.row-methods.
export const crewSessionImpersonationsConfig = rootConfig({
  page: { label: 'Watch desk', icon: 'visibility' },
  routeData: { route: 'watch-desk', hasViewRoute: false },
  parentConfig: listViewConfig({
    title: 'Watch desk',
    primaryResource: Resources.CrewSessionImpersonations,
    showViewButton: false,
    emptyMessage: 'Nobody has used the console as someone else.',
    listColumns: [
      { id: record.actorUsername, header: 'Established by' },
      // The person by the username the record keeps, which the column sorts and filters
      // by as Established by does; a session held as a role says the role.
      {
        id: record.principalUsername,
        header: 'As',
        valueGetter: (row: RecordData): string => String(row[record.principalUsername] ?? '') || heldAs(row),
      },
      // The account's id and the role ride along unseen: the As column says them where
      // the record keeps no username.
      { id: record.principalUserId, hidden: true },
      { id: record.principalRole, hidden: true },
      // The session's mask as the server says it: "reading only", "everything", or what a
      // session that acts as a person is refused.
      { id: record.mask, header: 'May do' },
      { id: record.reason, header: 'Reason' },
      { id: record.startedAt, header: 'Started', formatType: dayAndMinuteFormat },
      { id: record.expiresAt, header: 'Must end by', formatType: dayAndMinuteFormat },
      // The end as the record has it, and words where it has none: see ended.
      { id: record.endedAt, header: 'Ended', valueGetter: ended },
      { id: record.endReason, header: 'How it ended' },
    ],
    rowMethods: [
      {
        method: Methods.EndCrewSessionImpersonation,
        label: 'Revoke',
        icon: 'block',
        when: stillOpen,
        confirm: revokeQuestion,
        successMessage: 'The session was ended.',
      },
    ],
    elements: [],
  }),
});
