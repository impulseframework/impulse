import { Missions, Resources } from '@app/service/zz_gen_constants';
import { listViewConfig, rootConfig } from '@impulseframework/resource-angular/types';

// Missions in every sector is the LIST ACROSS TENANTS: a page of Headquarters over
// Missions, a sector-scoped resource that declares @listAcrossTenants, so its list is
// also served without the sector's segment (/console/api/missions). One request reads
// the missions of every sector the person reaches in which they may list them, each row
// checked under the person's roles in its own sector, with one sort, one page and one
// count across them. `acrossTenants` is all the page says: the library's list asks that
// one route, names each row's sector in a first column under the application's word for
// a tenant, and opens a row on the mission itself inside its own sector: the mission's
// own route there, which asks Read on the mission and nothing of the board the sector's
// Missions page lists, so a row she was listed opens for her. A row in a sector where a
// person may list missions and read none draws no link. The page asks
// for no column list, since the server decides sector by sector what a row shows: the
// liaison holds the Cadet's board in Anvil and the Archivist's ledger in Cinder, so her
// Anvil rows are the low-hazard missions with the Fee cell empty and her Cinder row is
// the closed sweep with its fee, and Bastion, where she holds no role, is not on the
// page. The page reads: a mission is booked, changed and claimed inside its sector.
//
// No one sector's grants speak for the page, and the config names no permission for it:
// the server tells the page's menu entry and its route whether the person may list
// missions in at least one sector they reach. The liaison sees the entry; a person who
// may list missions in no sector does not, and the route turns them away
// (missionsEverySector.config.spec.ts).
//
// A list across tenants is plain unless its page asks, and this one asks (`live: true`):
// the page holds one subscription over the sectors its list reads, so a mission booked or
// changed in Anvil or in Cinder shows up on the liaison's page without a reload, and one
// changed in Bastion never wakes it.
//
// Demonstrates: list.across-tenants, list.across-tenants.live.
export const missionsEverySectorConfig = rootConfig({
  page: { label: 'Missions in every sector', icon: 'travel_explore' },
  routeData: { route: 'missions' },
  parentConfig: listViewConfig({
    title: 'Missions in every sector',
    primaryResource: Resources.Missions,
    acrossTenants: true,
    live: true,
    listColumns: [
      { id: Missions.fieldName.title },
      { id: Missions.fieldName.kindId, header: 'Kind' },
      { id: Missions.fieldName.hazard },
      { id: Missions.fieldName.statusId, header: 'Status' },
      { id: Missions.fieldName.deadline },
      { id: Missions.fieldName.fee },
    ],
    elements: [],
  }),
});
