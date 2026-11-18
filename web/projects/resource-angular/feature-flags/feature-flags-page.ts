import { Injector } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { Route } from '@angular/router';
import { featureFlagsResource, setFeatureMethod } from '@impulseframework/resource';
import { Page } from '@impulseframework/resource-angular/auth-frame';
import { resourcePage, resourceRoutes } from '@impulseframework/resource-angular/resource-route-generator';
import {
  dayAndMinuteFormat,
  FieldName,
  listViewConfig,
  MethodPresentation,
  nameInWords,
  RecordData,
  Resource,
  ResourceMeta,
  rootConfig,
  RootConfig,
} from '@impulseframework/resource-angular/types';

// The Feature flags page, every application's: the flags the application declares, each by
// its name in words with its description, whether it is on, and who last turned it on or
// off and when. It is the library's list over the feature flags resource and holds no
// drawing of its own. A row's menu turns the flag on or off through the generated flip
// method, asking first, for a person the digest grants the method; a person who may list
// the flags and not flip them sees the list without the menu. Each row carries a History
// button, which opens the flag's flips in a pop-up for anyone who sees the list.

/** Where the Feature flags page lives: a page of the place outside every tenant. */
export const featureFlagsPath = 'feature-flags';

/** What the page says of an application that declares no flag. */
export const noFeatureFlags = 'This application declares no feature flags.';

/** What the page says in place of a time for a flag nobody has turned on or off. */
export const neverChanged = 'Never';

/** A flag's name in words, as the page lists it: `cargo_manifest` is "Cargo manifest". */
export function flagInWords(row: RecordData): string {
  return nameInWords(String(row['name'] ?? ''));
}

/** The question asked before a flip: it applies to everyone in the environment at once. */
export function flipQuestion(row: RecordData, enabled: boolean): string {
  return `Turn ${flagInWords(row)} ${enabled ? 'on' : 'off'} for everyone in this environment?`;
}

/** The flip as a row offers it: on for a flag that is off, off for one that is on. */
function turn(enabled: boolean): MethodPresentation {
  return {
    method: setFeatureMethod,
    label: enabled ? 'Turn on' : 'Turn off',
    icon: enabled ? 'toggle_on' : 'toggle_off',
    when: (row) => (row['enabled'] === true) !== enabled,
    fill: (row) => ({ name: row['name'], enabled }),
    confirm: (row) => flipQuestion(row, enabled),
    successMessage: enabled
      ? 'The flag is on for everyone in this environment.'
      : 'The flag is off for everyone in this environment.',
  };
}

/**
 * Opens a flag's history over the page. The pop-up is the library's
 * (`@impulseframework/resource-angular/feature-flag-history`), loaded when a person first
 * asks for one.
 */
function openHistory(row: RecordData, injector: Injector): void {
  const dialog = injector.get(MatDialog);
  void import('@impulseframework/resource-angular/feature-flag-history').then((history) =>
    history.openFeatureFlagHistory(dialog, { name: String(row['name'] ?? ''), words: flagInWords(row) }),
  );
}

/** How wide the On column is drawn, in pixels: room for its heading and for a check. */
const onColumnWidth = 64;

const column = (id: string, header: string): { id: FieldName; header: string; filterable: false } => ({
  id: id as FieldName,
  header,
  filterable: false,
});

/**
 * The page's configuration: the one object its route carries and its place in the menus
 * is made from, so the two are the same page.
 */
export const featureFlagsConfig: RootConfig = rootConfig({
  page: { label: 'Feature flags', icon: 'flag' },
  routeData: { route: featureFlagsPath, hasViewRoute: false },
  parentConfig: listViewConfig({
    title: 'Feature flags',
    primaryResource: featureFlagsResource as Resource,
    elements: [],
    showViewButton: false,
    showRowCount: false,
    showSearchBox: false,
    emptyMessage: noFeatureFlags,
    listColumns: [
      { ...column('name', 'Flag'), valueGetter: flagInWords },
      column('description', 'Description'),
      // A check or a dash: the column is as narrow as its heading, and the words get the room.
      { ...column('enabled', 'On'), width: onColumnWidth },
      // The server answers when a flag was last flipped, and nothing for a flag nobody has.
      // It works the two out of the flag's history, and orders by neither: no sort is offered.
      {
        ...column('updatedAt', 'Last changed'),
        formatType: dayAndMinuteFormat,
        emptyDataValue: neverChanged,
        sortable: false,
      },
      { ...column('updatedBy', 'Changed by'), sortable: false },
    ],
    rowMethods: [turn(true), turn(false)],
    rowButtons: [{ label: 'History', icon: 'history', run: openHistory }],
  }),
});

/**
 * The Feature flags page for the application's menu file to place: needing List on the
 * feature flags resource, and a page of the place outside every tenant by its address.
 * It shows whether or not the application declares a flag; one that declares none reads
 * that it has none.
 */
export function featureFlagsPage(resourceMeta: (resource: Resource) => ResourceMeta): Page {
  return resourcePage(featureFlagsConfig, resourceMeta);
}

/**
 * The route of the Feature flags page, guarded by List on the feature flags resource and
 * carrying the page the menu file places (featureFlagsPage). Put it among the routes of
 * the place outside every tenant, or among an untenanted application's own.
 */
export function featureFlagsRoute(resourceMeta: (resource: Resource) => ResourceMeta): Route {
  return resourceRoutes(featureFlagsConfig, resourceMeta);
}
