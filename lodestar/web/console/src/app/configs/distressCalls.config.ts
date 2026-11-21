import { DistressCalls, DistressRelays, Resources } from '@app/service/zz_gen_constants';
import { DomainRoutePath } from '@app/service/zz_gen_resources';
import {
  enumeratedConfig,
  field,
  foreignKeyDefault,
  listViewConfig,
  multiColumnConfig,
  rootConfig,
  section,
} from '@impulseframework/resource-angular/types';

// Calls is the config-driven page over the distress calls in the open sector, beside
// the hand-written call log over the same rows. It carries the write-only field and an
// imported object type. Transcript is input_only on the struct, writeOnly in the
// metadata: the server accepts it on create and update and never returns it, so the row
// page draws nothing for it in view mode and the create form draws a blank input whose
// value travels only when typed; a list column naming it would fail when the page is
// built, since a list never returns it, so none does. The row page's edit mode draws the
// same blank input: the row is read with its capability envelope, which the server plans
// over every field the caller may write, projected or not, so a caller whose Update grant
// covers the transcript sees it named although no read returns it, and the library takes
// the envelope as the positive list of editable fields. The hand-written call log writes
// the transcript too.
// Position is a GeoJSON Point, a type whose TypeScript shape is imported from the
// geojson package: the list's cell shows the JSON on one line, the row page prints it
// indented, and no mode offers an input; a call relayed by voice has none, the
// placeholder. The case number and the filer are server-owned and read-only everywhere.
//
// A call's row page carries its relays: the messages passed along about the call and the
// answers to them. A relay names its call by the case number, not by the call's key, so
// the list is the relays whose case number is the call's, and a new relay starts with it.
// An answer names the relay it answers, picked by its message. Deleting a relay that has
// answers, or a call that has relays, asks first: the dialog shows the server's question,
// which counts what goes with it, and the second press deletes them all.
//
// Demonstrates: field.object, field.write-only, @deleteCascade.own-kind, @deleteCascade.other-columns.
export const distressCallsConfig = rootConfig({
  page: { label: 'Calls', icon: 'sos' },
  routeData: { route: `${DomainRoutePath}/calls` },
  parentConfig: listViewConfig({
    title: 'Distress calls',
    createTitle: 'Distress call',
    createButtonLabel: 'File a distress call',
    primaryResource: Resources.DistressCalls,
    listColumns: [
      { id: DistressCalls.fieldName.caseNumber, header: 'Case' },
      { id: DistressCalls.fieldName.summary },
      { id: DistressCalls.fieldName.severity },
      { id: DistressCalls.fieldName.filedByName, header: 'Filed by' },
      { id: DistressCalls.fieldName.position },
    ],
    elements: [
      section({
        label: 'Call',
        children: [
          field({ name: DistressCalls.fieldName.summary, label: 'Summary', cols: 8 }),
          field({ name: DistressCalls.fieldName.severity, label: 'Severity', cols: 4 }),
          field({ name: DistressCalls.piiFieldName.callerContact, label: 'Caller contact', cols: 6 }),
          field({ name: DistressCalls.fieldName.transcript, label: 'Transcript', cols: 6 }),
          field({ name: DistressCalls.fieldName.caseNumber, label: 'Case number', cols: 6 }),
          field({ name: DistressCalls.fieldName.filedByName, label: 'Filed by', cols: 6 }),
          field({ name: DistressCalls.fieldName.position, label: 'Position', cols: 12 }),
        ],
      }),
    ],
  }),
  relatedConfigs: [
    listViewConfig({
      title: 'Relays',
      createTitle: 'Relay',
      createButtonLabel: 'Relay a message',
      primaryResource: Resources.DistressRelays,
      parentRelation: { parentKey: DistressCalls.fieldName.caseNumber, childKey: DistressRelays.fieldName.caseNumber },
      // A relay is read whole in its row: there is no page of its own to open.
      showViewButton: false,
      listColumns: [
        { id: DistressRelays.fieldName.message },
        // The relay answered, by its message: the row holds an id, which nobody reads.
        // The field behind the column is the id, so the column offers no filter or sort.
        multiColumnConfig({
          id: DistressRelays.fieldName.answersRelayId,
          header: 'Answers',
          additionalIds: [
            {
              id: DistressRelays.fieldName.id,
              resource: Resources.DistressRelays,
              field: DistressRelays.fieldName.message,
            },
          ],
          concatFn: 'hyphen-concat',
          filterable: false,
          sortable: false,
        }),
        { id: DistressRelays.fieldName.relayedByName, header: 'Relayed by' },
        { id: DistressRelays.fieldName.relayedAt, header: 'Relayed' },
      ],
      elements: [
        section({
          label: 'Relay',
          children: [
            field({
              name: DistressRelays.fieldName.caseNumber,
              label: 'Case number',
              cols: 12,
              readOnly: true,
              default: foreignKeyDefault({ parentId: DistressCalls.fieldName.caseNumber }),
            }),
            field({ name: DistressRelays.fieldName.message, label: 'Message', cols: 12 }),
            field({
              name: DistressRelays.fieldName.answersRelayId,
              label: 'Answers',
              cols: 12,
              enumeratedConfig: enumeratedConfig({
                listDisplay: [DistressRelays.fieldName.message],
                viewDisplay: [DistressRelays.fieldName.message],
              }),
            }),
          ],
        }),
      ],
    }),
  ],
});
