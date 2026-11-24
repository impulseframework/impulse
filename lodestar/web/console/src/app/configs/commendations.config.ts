import { Commendations, Pilots, Resources } from '@app/service/zz_gen_constants';
import { enumeratedConfig, field, listViewConfig, rootConfig, section } from '@impulseframework/resource-angular/types';

// Commendations is the commendations desk: a citation on a pilot's record for a sortie
// flown well, filed by headquarters and read by the crew. It is the one resource behind a
// feature flag, and this config says nothing about the flag: the generated metadata
// carries `feature: 'commendations'`, and resourceRoutes reads it, puts the library's
// featureMatch guard on the page's route (so while the flag is off a URL to the desk
// falls to the wildcard as an unknown URL does), and resourcePage carries the flag onto
// the menu entry (so the frame leaves it out while the flag is off). The permission digest
// hides the entry too while the flag is off, since the server leaves a gated-off resource
// out of it. Turn the flag on in Feature flags (System, in Headquarters) as the adjutant
// and the desk is there, its
// three seeded citations newest first (the resource's @order).
//
// AwardedBy and AwardedAt are the server's (output_only): the metadata marks them read
// only, so the create form offers the pilot and the citation alone, and the row page
// shows who filed the citation and when.
//
// Demonstrates: @feature.
export const commendationsConfig = rootConfig({
  page: { label: 'Commendations', icon: 'military_tech' },
  parentConfig: listViewConfig({
    title: 'Commendations',
    createTitle: 'Commendation',
    primaryResource: Resources.Commendations,
    listColumns: [
      // The pilot by the name on the registry: a citation holds the pilot's id, which
      // nobody reads, and the list asks the server for the pilot's name beside it with the
      // page of citations. A header click sorts by the name and the column's filter
      // narrows by it: the pilots' names are indexed.
      { id: Commendations.fieldName.pilotId, header: 'Pilot' },
      { id: Commendations.fieldName.citation },
      { id: Commendations.fieldName.awardedByName, header: 'Awarded by' },
      { id: Commendations.fieldName.awardedAt, header: 'Awarded' },
    ],
    elements: [
      section({
        label: 'Citation',
        children: [
          field({
            name: Commendations.fieldName.pilotId,
            label: 'Pilot',
            cols: 4,
            enumeratedConfig: enumeratedConfig({
              listDisplay: [Pilots.fieldName.displayName],
              viewDisplay: [Pilots.fieldName.displayName],
            }),
          }),
          field({ name: Commendations.fieldName.citation, label: 'Citation', cols: 8 }),
          field({ name: Commendations.fieldName.awardedByName, label: 'Awarded by', cols: 6 }),
          field({ name: Commendations.fieldName.awardedAt, label: 'Awarded', cols: 6 }),
        ],
      }),
    ],
  }),
});
