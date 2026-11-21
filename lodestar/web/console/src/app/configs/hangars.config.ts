import {
  CrewSectorGroups,
  CrewSectorUsers,
  HangarPasses,
  Hangars,
  Resources,
  Squadrons,
} from '@app/service/zz_gen_constants';
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

// Hangars is a config-driven page in the open sector whose row page carries the hangar's
// passes: who the hangar is open to. A pass names one holder, and the form offers the
// three kinds side by side, each picked from a list the generated metadata names and
// nothing here repeats: a person from the sector's people, a squadron from the sector's
// squadrons, or a group from the sector's groups, the groups the Groups page under
// People manages. The group picker lists the groups kept in this sector alone, so a
// group of another sector, or one kept for the whole fleet, is never offered. A pass
// that names two holders, or none, is refused by the server in words the dialog shows.
//
// Both lists open their create in a dialog, as every list does: the button above the
// list opens the form over the page under the button's own words (createButtonLabel), a
// refusal stays in the dialog, and the list reads its rows again once the row is there.
//
// What a person sees here is the role file's: the marshal lists every hangar and writes
// its passes; an armorer lists only the hangars a pass opens to him, in his own name,
// through a squadron he flies with or through a group he is in, and re-zones the ones a
// pass lets him run; the sentry holds no pass and flies with no squadron, so the one
// hangar on his list is there through his group alone.
//
// Demonstrates: @subjectSet.on-groups, list.create-dialog.
export const hangarsConfig = rootConfig({
  page: { label: 'Hangars', icon: 'warehouse' },
  routeData: { route: `${DomainRoutePath}/hangars` },
  parentConfig: listViewConfig({
    title: 'Hangars',
    createTitle: 'Hangar',
    createButtonLabel: 'Add a hangar',
    primaryResource: Resources.Hangars,
    listColumns: [{ id: Hangars.fieldName.name }, { id: Hangars.fieldName.zone }],
    elements: [
      section({
        label: 'Hangar',
        children: [
          field({ name: Hangars.fieldName.name, label: 'Name', cols: 12 }),
          field({ name: Hangars.fieldName.zone, label: 'Zone', cols: 12 }),
        ],
      }),
    ],
  }),
  relatedConfigs: [
    listViewConfig({
      title: 'Passes',
      createTitle: 'Pass',
      createButtonLabel: 'Add a pass',
      primaryResource: Resources.HangarPasses,
      parentRelation: { parentKey: Hangars.fieldName.id, childKey: HangarPasses.fieldName.hangarId },
      // A pass is read whole in its row: there is no page of its own to open.
      showViewButton: false,
      // The holder by its name, in the column of its kind: the pass holds an id, which
      // nobody reads. Each column resolves its page of names with one request. The field
      // behind each column is the id, so a filter typed over it, or a sort by it, would
      // be asked of ids: the three columns offer neither. A person is read as people know
      // them, the name first and the login they sign in under beside it, and by the
      // login alone where the account carries no name.
      listColumns: [
        multiColumnConfig({
          id: HangarPasses.fieldName.userId,
          header: 'Person',
          additionalIds: [
            {
              id: CrewSectorUsers.fieldName.id,
              resource: Resources.CrewSectorUsers,
              field: CrewSectorUsers.fieldName.name,
            },
            {
              id: CrewSectorUsers.fieldName.id,
              resource: Resources.CrewSectorUsers,
              field: CrewSectorUsers.fieldName.username,
            },
          ],
          concatFn: 'hyphen-concat',
          filterable: false,
          sortable: false,
        }),
        multiColumnConfig({
          id: HangarPasses.fieldName.squadronId,
          header: 'Squadron',
          additionalIds: [
            { id: Squadrons.fieldName.id, resource: Resources.Squadrons, field: Squadrons.fieldName.name },
          ],
          concatFn: 'hyphen-concat',
          filterable: false,
          sortable: false,
        }),
        multiColumnConfig({
          id: HangarPasses.fieldName.groupId,
          header: 'Group',
          additionalIds: [
            {
              id: CrewSectorGroups.fieldName.id,
              resource: Resources.CrewSectorGroups,
              field: CrewSectorGroups.fieldName.name,
            },
          ],
          concatFn: 'hyphen-concat',
          filterable: false,
          sortable: false,
        }),
        { id: HangarPasses.fieldName.levelId, header: 'Level' },
        { id: HangarPasses.fieldName.expiresAt, header: 'Expires' },
      ],
      elements: [
        section({
          label: 'Pass',
          children: [
            field({
              name: HangarPasses.fieldName.hangarId,
              label: 'Hangar',
              cols: 12,
              readOnly: true,
              default: foreignKeyDefault({ parentId: Hangars.fieldName.id }),
            }),
            field({ name: HangarPasses.fieldName.levelId, label: 'Level', cols: 6 }),
            field({ name: HangarPasses.fieldName.expiresAt, label: 'Expires', cols: 6 }),
          ],
        }),
        section({
          label: 'Held by one of: a person, a squadron or a group',
          children: [
            field({
              name: HangarPasses.fieldName.userId,
              label: 'Person',
              cols: 12,
              // Each person by name, the login beside it: two people can share a name,
              // never a login.
              enumeratedConfig: enumeratedConfig({
                listDisplay: [CrewSectorUsers.fieldName.name, CrewSectorUsers.fieldName.username],
                viewDisplay: [CrewSectorUsers.fieldName.name, CrewSectorUsers.fieldName.username],
              }),
            }),
            field({
              name: HangarPasses.fieldName.squadronId,
              label: 'Squadron',
              cols: 12,
              enumeratedConfig: enumeratedConfig({
                listDisplay: [Squadrons.fieldName.name],
                viewDisplay: [Squadrons.fieldName.name],
              }),
            }),
            // The group, held by its id and picked by its name from the sector's groups,
            // which whoever writes a pass may list.
            field({
              name: HangarPasses.fieldName.groupId,
              label: 'Group',
              cols: 12,
              enumeratedConfig: enumeratedConfig({
                listDisplay: [CrewSectorGroups.fieldName.name],
                viewDisplay: [CrewSectorGroups.fieldName.name],
              }),
            }),
          ],
        }),
      ],
    }),
  ],
});
