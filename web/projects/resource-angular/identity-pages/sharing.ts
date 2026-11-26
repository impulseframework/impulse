import { ApiDescriptor, IdentityRecordSharing, Resource } from '@impulseframework/resource';
import {
  ChildResourceConfig,
  ColumnConfig,
  FieldName,
  listViewConfig,
  Method,
  MethodPresentation,
  RecordData,
  ResourceMeta,
  roleInWords,
} from '@impulseframework/resource-angular/types';
import { lastDay, periodEnds, periodWords } from './periods';

// A record's Sharing: who holds a role on one record of a tenant, a person or a group, and
// the methods that give one and take it away, drawn by the library's list on the record's
// page. The server describes it on the tenant's side of the Auth (the side's `sharing`):
// the record's words, its list of holders, the list a role is picked from, and the
// methods. The list and each button are offered as the list offers any: by the caller's
// grant on the list and on each method, so a caller who cannot share sees the list with no
// buttons, and one who cannot list it sees no list.

/** What a holder of a role on a record is, as the list of holders says it (`memberKind`). */
const person = 'person';
const group = 'group';

// The configurations of a record's Sharing, made for each description, Auth and record, so
// every caller of one request reads the same configuration.
const made = new WeakMap<ApiDescriptor, Map<string, ChildResourceConfig | undefined>>();

/**
 * The configuration of a record's Sharing, for the record's page (`relatedConfigs`): the
 * roles held on the page's record, a row for each, with the buttons that give a person or
 * a group a role on it, and Remove on each row. Undefined where the tenant's side of the
 * Auth shares no such record. The same description, Auth and record answer the same
 * configuration. Refused for an Auth the description does not carry.
 */
export function sharingConfig(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta | undefined,
  options: { auth: string; record: string },
): ChildResourceConfig | undefined {
  const requests = made.get(descriptor) ?? new Map<string, ChildResourceConfig | undefined>();
  made.set(descriptor, requests);
  const key = `${options.auth}\u0000${options.record}`;
  if (requests.has(key)) {
    return requests.get(key);
  }
  const identity = (descriptor.identity ?? []).find((candidate) => candidate.auth === options.auth);
  if (!identity) {
    const known = (descriptor.identity ?? []).map((candidate) => candidate.auth).join(', ') || 'none';
    throw new Error(
      `sharingConfig: the API description carries no generated management of the auth ${options.auth} (it carries: ${known}); the auth is named as GenerateIdentity names it`,
    );
  }
  const entry = identity.sides
    .find((side) => side.scope === 'domain')
    ?.sharing?.find((candidate) => candidate.record === options.record);
  const config = entry ? sharing(entry, resourceMeta) : undefined;
  requests.set(key, config);
  return config;
}

/** The Sharing list of one kind of record. */
function sharing(
  entry: IdentityRecordSharing,
  resourceMeta: (resource: Resource) => ResourceMeta | undefined,
): ChildResourceConfig {
  const key =
    resourceMeta(entry.record as Resource)?.fields.find((field) => field.primaryKey !== undefined)?.fieldName ?? 'id';
  // The page's record, which every method of the list names.
  const onRecord = (row: RecordData): Record<string, unknown> => ({ recordId: row[key] });
  const holder = (row: RecordData): string => String(row['name'] ?? '');
  const ask = (row: RecordData): string =>
    `Remove ${holder(row)}'s ${roleInWords(String(row['role'] ?? ''))} role on this ${entry.one}?`;
  const giving = (method: string, label: string, icon: string): MethodPresentation => ({
    method: method as Method,
    label,
    icon,
    fill: onRecord,
    labels: { userId: 'Person', groupId: 'Group', role: 'Role', ...periodWords },
    throughDay: periodEnds,
    // The roles the list offers are the ones the caller could give with this method.
    narrows: { role: () => `method:eq:${method}` },
  });
  const removing = (method: string, kind: string, filled: string, icon: string): MethodPresentation => ({
    method: method as Method,
    label: 'Remove',
    icon,
    when: (row) => row['memberKind'] === kind,
    fill: (row) => ({ recordId: row['recordId'], [filled]: row['memberId'], role: row['role'] }),
    confirm: ask,
  });
  const columns: ColumnConfig[] = [
    { id: 'name' as FieldName, header: 'Name' },
    {
      id: 'memberKind' as FieldName,
      header: 'Kind',
      filterable: false,
      valueGetter: (row: RecordData) => (row['memberKind'] === group ? 'Group' : 'Person'),
    },
    { id: 'role' as FieldName, header: 'Role' },
    { id: 'endsAt' as FieldName, header: 'Ends', valueGetter: (row: RecordData) => lastDay(row['endsAt']) },
    // Read with each row and not drawn: what Remove sends.
    ...['recordId', 'memberId'].map((name) => ({ id: name as FieldName, hidden: true })),
  ];
  return listViewConfig({
    title: 'Sharing',
    primaryResource: entry.holders as Resource,
    parentRelation: { parentKey: key as FieldName, childKey: 'recordId' as FieldName },
    collapsible: false,
    emptyMessage: `Nobody holds a role on this ${entry.one} alone.`,
    listColumns: columns,
    rowMethods: [
      removing(entry.take, person, 'userId', 'person_remove'),
      ...(entry.takeGroup ? [removing(entry.takeGroup, group, 'groupId', 'group_remove')] : []),
    ],
    listMethods: [
      giving(entry.give, 'Share with a person', 'person_add'),
      ...(entry.giveGroup ? [giving(entry.giveGroup, 'Share with a group', 'group_add')] : []),
    ],
    elements: [],
  });
}

/**
 * What the Roles cell of a tenant's people list says of a person who holds no role in the
 * tenant and a role on some of its records: one record with a name, the record's word and
 * its name ("Voyage Apollo only"); one without, "1 voyage only"; several of one kind, "3
 * voyages only"; several of more than one kind, "3 records only". The words are the
 * description's (`sharing`); a kind it does not share is a record. Undefined for a person
 * who holds a role on none.
 */
export function recordsOnly(row: RecordData, shared: readonly IdentityRecordSharing[] | undefined): string | undefined {
  const held = Number(row['recordsHeld'] ?? 0);
  if (!(held > 0)) {
    return undefined;
  }
  const kind = String(row['recordsHeldKind'] ?? '');
  const entry = kind === '' ? undefined : shared?.find((candidate) => candidate.record === kind);
  if (held === 1) {
    const one = entry?.one ?? 'record';
    const name = String(row['recordHeldName'] ?? '');
    return name === '' ? `1 ${one} only` : `${one.charAt(0).toUpperCase()}${one.slice(1)} ${name} only`;
  }
  return `${held} ${entry?.many ?? 'records'} only`;
}
