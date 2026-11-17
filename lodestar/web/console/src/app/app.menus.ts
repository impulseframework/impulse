import { apiDescriptor } from '@app/service/zz_gen_api';
import { Permissions, Resources } from '@app/service/zz_gen_constants';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { FrameActions, Menu } from '@impulseframework/resource-angular/auth-frame';
import { identityMenuPages } from '@impulseframework/resource-angular/identity-pages';
import { resourcePage } from '@impulseframework/resource-angular/resource-route-generator';
import { deckPages, sectorSignInPage, watchDeskPage } from '@components/sector/sector.routes';
import { clientsConfig } from './configs/clients.config';
import { commendationsConfig } from './configs/commendations.config';
import { distressCallsConfig } from './configs/distressCalls.config';
import { missionDocumentsConfig } from './configs/missionDocuments.config';
import { missionsConfig } from './configs/missions.config';
import { pilotsConfig, sectorPilotsConfig } from './configs/pilots.config';
import { sectorsConfig } from './configs/sectors.config';
import { shipClassesConfig } from './configs/shipClasses.config';
import { shipsConfig } from './configs/ships.config';
import { squadronsConfig } from './configs/squadrons.config';
import { standingOrdersConfig } from './configs/standingOrders.config';

/**
 * The console's menu file: the menus across the top, in order, which the library's frame
 * draws on the bar and, on a phone, in the side panel. Each page holds its own label,
 * icon, address and the grant it needs, and the routes carry the same page; this file says
 * only where each is placed. The menus show what lives in the place the person is in:
 * Records and System in Headquarters, the place outside every sector; Operations, Decks,
 * People and Sector inside a sector; and each entry only under its grant, so a menu left
 * with nothing is not drawn. The library adds Help last, ending with About Lodestar, and
 * the person's own menu at the right (Your account, Your API keys, Appearance, Sign out).
 * A page added to the routes and forgotten here fails app.menus.spec.ts, naming the page.
 *
 * The frame calls the function once, in an injection context, handing it what the frame
 * does: here the feature flags dialog.
 *
 * Demonstrates: frame.menu-file.
 */
export function appMenus(actions: FrameActions): Menu[] {
  return [
    {
      label: 'Records',
      groups: [
        {
          entries: [
            resourcePage(clientsConfig, resourceMeta),
            resourcePage(shipClassesConfig, resourceMeta),
            resourcePage(standingOrdersConfig, resourceMeta),
            resourcePage(pilotsConfig, resourceMeta),
            // Behind the commendations flag: the entry shows only while it is on.
            resourcePage(commendationsConfig, resourceMeta),
            resourcePage(sectorsConfig, resourceMeta),
          ],
        },
      ],
    },
    {
      label: 'Operations',
      groups: [
        {
          entries: [
            resourcePage(missionsConfig, resourceMeta),
            resourcePage(shipsConfig, resourceMeta),
            resourcePage(squadronsConfig, resourceMeta),
            resourcePage(missionDocumentsConfig, resourceMeta),
            resourcePage(distressCallsConfig, resourceMeta),
            resourcePage(sectorPilotsConfig, resourceMeta),
          ],
        },
      ],
    },
    {
      label: 'Decks',
      groups: [
        {
          entries: [
            deckPages.flightDeck,
            deckPages.hangarDeck,
            deckPages.salvageHold,
            deckPages.callLog,
            deckPages.roster,
            deckPages.hazardBoard,
            deckPages.shipsLog,
            deckPages.briefing,
          ],
        },
      ],
    },
    {
      label: 'People',
      groups: [
        {
          entries: [
            // The sector's crew and its invitations: the pages the library builds for the
            // sector's side of the crew auth's generated management.
            ...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Crew', scope: 'domain' }),
          ],
        },
      ],
    },
    {
      label: 'Sector',
      groups: [{ entries: [sectorSignInPage] }],
    },
    {
      label: 'System',
      groups: [
        {
          // The crew's accounts across the whole service, their mailed links, sessions and
          // failed sign-ins, and the Sign-in page, which lists the crew's sign-in methods.
          // Its Open sign-up action runs a method that declares a warning: the library's
          // dialog asks the server what opening that method will do, shows the answer in
          // the warning style, and holds its button until the word `open` is typed.
          //
          // Demonstrates: @warning.
          label: 'Crew',
          entries: [...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Crew', scope: 'global' })],
        },
        {
          // The clients' accounts, through the members auth the console manages.
          label: 'Members',
          entries: [...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Members', scope: 'global' })],
        },
        {
          entries: [
            {
              label: 'Feature flags…',
              icon: 'flag',
              run: () => actions.featureFlags(),
              place: 'outside',
              permission: { resource: Resources.FeatureFlags, permission: Permissions.List },
            },
            watchDeskPage,
          ],
        },
      ],
    },
  ];
}
