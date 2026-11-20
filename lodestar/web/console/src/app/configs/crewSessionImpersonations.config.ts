import { CrewSessionImpersonations, CrewSessionUsers, Methods, Resources } from '@app/service/zz_gen_constants';
import {
  dayAndMinuteFormat,
  listViewConfig,
  multiColumnConfig,
  RecordData,
  rootConfig,
} from '@impulseframework/resource-angular/types';

const record = CrewSessionImpersonations.fieldName;

/** Whether a session held as someone else is still open: its record has no end yet. */
export function stillOpen(row: RecordData): boolean {
  return !row[record.endedAt];
}

/**
 * Who a session is held as, where no account answers by name: the role's name for a
 * session held as a role, and the id the record keeps for a person whose account the
 * caller may not list or which is gone.
 */
export function heldAs(row: RecordData): string {
  return String(row[record.principalRole] || row[record.principalUserId] || '');
}

/** What Revoke asks before it ends a session, naming who holds it. */
export function revokeQuestion(row: RecordData): string {
  return `End the session ${String(row[record.actorUsername] || 'this person')} holds as someone else? Their next request is refused.`;
}

// The watch desk: the sessions people hold as someone else across the whole service,
// drawn by the library's list over the crew auth's generated impersonation records,
// newest first as the resource orders them. Each row says who established the session,
// as whom (the person's username, read from the accounts by the id the record keeps, or
// the role's name), why, when it began, when its two-hour cap ends it, and when and why
// it ended; a session that has not ended reads "Still open". A row's menu offers Revoke
// while the session is open and the server's Execute answer for the row lists the
// generated method that ends it: the revoked console's next request is refused and its
// banner explains. The page shows under List on the records and the menu under Execute
// on the method, both the role file's to give; the list is live, so a session minted or
// ended elsewhere shows without asking again.
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
      multiColumnConfig({
        id: record.principalUserId,
        header: 'As',
        additionalIds: [
          {
            id: CrewSessionUsers.fieldName.id,
            resource: Resources.CrewSessionUsers,
            field: CrewSessionUsers.fieldName.username,
          },
        ],
        concatFn: 'hyphen-concat',
        fallback: heldAs,
        // The server filters this field by the account's id, which nobody types: the
        // column shows a name and offers no filter. Established by filters by username.
        filterable: false,
      }),
      // The role rides along unseen: the As column says it where the session is held as a role.
      { id: record.principalRole, hidden: true },
      { id: record.reason, header: 'Reason' },
      { id: record.startedAt, header: 'Started', formatType: dayAndMinuteFormat },
      { id: record.expiresAt, header: 'Must end by', formatType: dayAndMinuteFormat },
      {
        id: record.endedAt,
        header: 'Ended',
        formatType: dayAndMinuteFormat,
        emptyDataValue: 'Still open',
      },
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
