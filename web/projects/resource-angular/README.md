# @impulseframework/resource-angular

**@impulseframework/resource-angular** is the Angular binding of [`@impulseframework/resource`](../resource/README.md), the framework-neutral client for generated APIs. It is a comprehensive Angular library designed to provide a consistent and configurable foundation for building enterprise-level data-driven applications. By defining a configuration, you can dynamically generate entire application pages.

## Core Features

- **Dynamic Page Generation**: Define a `resourceConfig` to dynamically render components and build complex layouts. For example, the `resource-resolver` can switch between components based on your data.
- **Authentication & Authorization**: A set of services and guards for managing user authentication and permissions.
- **Utility Functions**: A set of helper functions for various tasks, such as data manipulation and request customization. Also included are tools for handling PATCH requests per the JSON Patch standard [RFC 6902](https://tools.ietf.org/html/rfc6902).

## Getting Started

To install it in your project, run the following command:

```bash
npm install @impulseframework/resource @impulseframework/resource-angular
```

`@impulseframework/resource` is a peer dependency: the application installs it once and both the generated client and this library resolve the same copy.

An application built on the library runs without zone.js, Angular's default since version 21, as the skeletons' applications and Lodestar do. The page is drawn again when a signal a template reads changes, when an input changes, or when the template handles an event, and not because a timer or a request finished. A component that changes a plain field from a timer, from a subscription or after an `await` therefore holds the value in a signal, or calls `ChangeDetectorRef.markForCheck()`.

Then import the library's stylesheet once, from the application's global styles (`src/styles.scss`):

```scss
@use '@impulseframework/resource-angular/styles';
```

The form and list components request its classes by name — the twelve-column field grid, the read-only and edit-mode treatments, the sticky list header — and it declares the color tokens the components draw with, which the application's theme sets (see Theming below). Without the import, fields lose their grid and section labels lose their styling.

The stylesheet also brings the fonts the components render in, served by the application itself: Roboto in the three weights the Material typography uses (300, 400, 500; the latin and latin-ext subsets) and the filled Material Icons, as files of this package the application's build carries into its bundle as same-origin assets. The application's `index.html` carries no Google Fonts link and its content security policy no font host (`style-src` and `font-src` stay at `'self'`): a progressive web app's service worker fetches the page's requests under the policy delivered with its own script, whose `connect-src` names no font host, so a third-party font fails on every worker-controlled load, while a same-origin font is fetched under `connect-src 'self'` and cached by the worker's asset group, so the application renders offline and during an outage. The Material typography tokens, which the prebuilt themes set to `Roboto` alone, get a fallback chain (`Roboto, 'Helvetica Neue', Arial, sans-serif`), so a font that fails to load never leaves a component in the browser's serif default. An application that wants the fonts alone imports `@impulseframework/resource-angular/styles/fonts`. The outlined icon set is not shipped; a component that needs it adds it here.

## Theming

The look of the components is the application's Material theme plus a small set of color tokens the library reads. An application themes the library in two steps: it defines a Material theme, and it hands the library's frame to that theme. A root that does neither keeps the look the library has always had.

**The Material theme** colors every Material control the components compose: the buttons, fields, tables, menus and dialogs. The skeletons define it in `theme.scss` with Angular Material's `mat.theme`, from a palette, a type face and a density, and name that file in `angular.json` in place of a prebuilt stylesheet (a prebuilt one, `@angular/material/prebuilt-themes/azure-blue.css`, is the same thing with a fixed palette):

```scss
@use '@angular/material' as mat;
@use '@impulseframework/resource-angular/theme';

html {
  color-scheme: light;
  @include mat.theme(
    (
      color: (
        primary: mat.$azure-palette,
        tertiary: mat.$blue-palette,
      ),
      typography: Roboto,
      density: 0,
    )
  );
}
```

**The library's frame** is what the components draw themselves: the navigation's links, headers and active bar, a section's border and label, a notice's border and background, a dialog's row separators, the unsaved-changes word. Each of those colors is a CSS variable the component reads, declared in the `styles` sheet with the library's own color as its default, inside a cascade layer (`impulse-defaults`) so that any declaration outside it wins. The `theme` sheet, the second `@use` above, sets every one of them from the Material system tokens the theme emits, so the frame follows the theme's roles: primary for what invites a click, error for what warns, the surface and outline roles for the rest. Change the palette and every surface follows; set a token after the theme sheet to override one place. The tokens, with their defaults and what the theme sheet sets them to:

| Token                               | What it colors                                               | Default               | From the theme                              |
| ----------------------------------- | ------------------------------------------------------------ | --------------------- | ------------------------------------------- |
| `--impulse-accent`                      | navigation links and headers, the active bar, the edit links | `#003b49`             | `--mat-sys-primary`                         |
| `--impulse-surface`                     | the surface a section label and a list sit on                | `#ffffff`             | `--mat-sys-surface`                         |
| `--impulse-outline`                     | a section's border                                           | `#8e8e8e`             | `--mat-sys-outline`                         |
| `--impulse-divider`                     | a section heading's rule, a dotted outline                   | `#ccc`                | `--mat-sys-outline-variant`                 |
| `--impulse-outline-variant`             | a read-only field's focus underline and checkbox border      | `#e0e0e0`             | `--mat-sys-outline-variant`                 |
| `--impulse-hairline`                    | a row separator in a table or dialog                         | `rgba(0, 0, 0, 0.12)` | `--mat-sys-outline-variant`                 |
| `--impulse-shaded`                      | a shaded block                                               | `#e9e9e9`             | `--mat-sys-surface-container`               |
| `--impulse-dirty`                       | a field changed and not yet saved                            | `#00617f26`           | primary at 15%                              |
| `--impulse-muted`                       | a field's prefix and suffix                                  | `gray`                | `--mat-sys-on-surface-variant`              |
| `--impulse-hover`                       | a navigation item under the pointer, an open row             | `rgba(0, 0, 0, 0.04)` | on-surface at 8%                            |
| `--impulse-pressed`                     | a navigation item pressed                                    | `rgba(0, 0, 0, 0.1)`  | on-surface at 12%                           |
| `--impulse-warn`                        | the unsaved-changes word                                     | `#f44336`             | `--mat-sys-error`                           |
| `--impulse-edit-shadow`                 | a form in edit mode                                          | two soft shadows      | `--mat-sys-level2`                          |
| `--impulse-alert-bg`                    | a notice of no kind                                          | `lightgray`           | `--mat-sys-surface-container-high`          |
| `--impulse-alert-warn-border`, `-bg`    | an error notice                                              | `red`, `#fff5f4`      | `--mat-sys-error`, `-error-container`       |
| `--impulse-alert-accent-border`, `-bg`  | an information or idle notice                                | `#dfb51d`, `#fff8e0`  | `--mat-sys-tertiary`, `-tertiary-container` |
| `--impulse-alert-success-border`, `-bg` | a success notice                                             | `#4caf50`, `#f0fff0`  | `--mat-sys-primary`, `-primary-container`   |

The variables the sheet exposed before the tokens stay: `--default-blue` (a form in edit mode) and `--app-background-color` (a container's background) are set by the theme sheet too, from `--mat-sys-primary` and `--mat-sys-surface`; `--layout-label-background-color` and its presets, `--form-edit-mode-bg`, `--underline-fields` and `--sticky-headers` are switches the application flips, not colors the theme decides. The components also read Material's own tokens directly where one fits (`--mat-sys-outline-variant` for a grid line, `--mat-sys-error` for a refusal), each with a literal fallback that applies only on a page with no Material theme at all. A spec in the library holds this contract: every token a component reads is declared with a default, the theme sheet sets every one of them, and no component stylesheet carries a literal color of its own.

**A choice for the person.** An application that offers a choice of looks keeps the Material theme as above and lets the person pick light or dark, and one of the palettes it offers, from `<impulse-theme-picker>` (`@impulseframework/resource-angular/ui-theme`) on whichever page keeps their own settings. `ThemeService` applies the choice to the root element, `color-scheme` as an inline style (the theme emits both values of every token, so the one property flips the page) and `data-palette`, and remembers it in the browser's local storage (`impulse-theme`), so the login page honors it before sign-in and the next visit starts with it. The application names its palettes twice, by the same ids: to `provideTheme` in its providers, and to the theme sheet's `palettes` mixin in `theme.scss`, which emits the first palette's theme on `html` (the look where no choice was made) and every palette's color tokens under `html[data-palette='<id>']`, with its primary color on `[data-impulse-swatch='<id>']` for the picker's swatch. The `palettes` sheet carries a Workspace palette ready-made, generated from Google's blue and green with Material's theme-color schematic; Angular Material's own (`mat.$azure-palette`, `mat.$violet-palette`, ...) are the rest of the choice. An application that offers one palette, or none, hands `provideTheme` no palettes and the picker offers light and dark alone. An application's own stylesheets follow the choice as far as they read the Material tokens (`var(--mat-sys-surface)` for a page background, `var(--mat-sys-outline-variant)` for a hairline); a literal color stays what it is in dark.

```ts
provideTheme({
  palettes: [
    { id: 'blue', label: 'Blue' },
    { id: 'workspace', label: 'Workspace' },
  ],
}),
```

```scss
@use '@angular/material' as mat;
@use '@impulseframework/resource-angular/theme';
@use '@impulseframework/resource-angular/palettes';

html {
  color-scheme: light;
}
@include theme.palettes(
  (
    'blue': (
      primary: mat.$azure-palette,
      tertiary: mat.$blue-palette,
    ),
    'workspace': (
      primary: palettes.$workspace-primary,
      tertiary: palettes.$workspace-tertiary,
    ),
  )
);
```

```html
<impulse-theme-picker />
```

## Core Concepts

The central concept of the library is the `resourceConfig`. This configuration object defines the structure and behavior of a page or a part of a page. It specifies which components to render, how they are connected, and how they interact with data.

The `compound-component` component is the engine that brings the `resourceConfig` to life. It dynamically creates and configures components based on the provided configuration, allowing for highly flexible and data-driven UIs.

A row page is a compound page over one row, and the page owns that row. Its store reads the row once, by the key the page resolved, and every related config the page draws (an array view, a child list, a resolver's component, the RPC buttons) exists only once the row is present: no child asks for anything from a row that has not arrived, so an array view's `listFilter` is never written against an empty row and a child list never asks for an unfiltered first page. The page's own view provides no store of its own and draws and writes through the page's store, so a save in the form is what every other child of the page reads. A reload of the row keeps the row, the form, and the children on screen while the read is in flight. A `impulse-resource-view` therefore sits inside a compound page, where it shares the page's store, or on an element carrying `impulseRowStore`, which provides one for a view over a different row (the compound page places it on its related-row views); a view with no store in scope fails at construction naming `ResourceStore`.

A config describes the widest surface a page can have; the permission digest decides how much of it a given user gets. A create form renders only the inputs the digest grants for Create, and a list requests and renders only the configured columns the digest grants for List (key fields are structural and always pass), so one config serves a full-width role and a narrow one without a refusal in between. When the digest leaves a list no column at all, or the server refuses a request, the table says so in place of "No records found". The same digest decides whether a row can be opened: a list draws its view arrow, and the row route admits, only when Read is granted or conditional for the resource, and a row whose read is refused says so in place of its form.

An enumerated field — one that holds another resource's identifier — lists exactly the resource the generated metadata names: `enumeratedResource` for a resource-backed picker (a field-scope `@enumerate` on the Go struct, or the schema's foreign key), or `enumeration` for a fixed value set the picker renders with no request. The field's `enumeratedConfig` narrows those rows (`filter`, `sorts`) and chooses their display; it cannot name another resource. The picker asks for the id and the display columns explicitly. A `searchable` picker over a source read whole narrows the loaded options as the user types, every typed word found in the display text, the id (so an identifier can be pasted), or one of the source's search fields, folded the way the server folds them; a `searchable` picker over a paged source narrows on the server, one page at a time, by the search box when the source has search fields the caller may list, else by a prefix match on the display field when its metadata lists `startswith`, with the box inside the panel and the page's count as the matches, and a paged source with neither is refused when the field is built, naming the resource and the DDL way out. Which fields a box searches is the source resource's, never the picker's: a picker that should search fewer fields lists a view projecting fewer token columns.

How a picker reads that resource is the descriptor's statement alone: its declared maximum page size (`@page max`). No maximum, and the picker reads the source whole in one request (`limit=all`) and resolves the chosen value from that list — an id the list does not hold shows as itself — so a source with no read route (a computed catalog, a key-less view) serves it. A maximum, and the picker pages the source one server page at a time at the descriptor's default size, with Previous and Next inside the panel following the server's cursors and the first page's total, sorted by the configured `sorts`, else the source's `@order`, else the display column; the chosen value is read by key and shown ahead of the page's options whichever page is open, and a source with a maximum serves that read (the generator refuses one that does not). A bounded source the server refuses (no order and no sort) shows the refusal in the server's words under the field, never an empty list. The same switch decides how a list's referenced-resource columns resolve their display values (an unbounded source read whole once and mapped; a bounded one asked for one `filter=id:in:(…)` page per grid page over the page's keys, served by the key's index) and how an array view lists its children (whole, or one server page with Previous and Next). Nothing in the library reads every row of a bounded resource: the maximum was declared to keep the whole set out of memory, and declaring it is the author's deliberate switch.

A list page can list a view and write to the view's table. The view declares its backing table, a create goes into the table, and the new row shows up in the view on the next list because the view's SQL reads that table: the generated metadata carries the table as `rowsOf`, and the `listViewConfig` names the view alone as `primaryResource`. Rows, columns, `filter`, `sorts`, `parentRelation`, and the List-grant narrowing come from the view, since that is the resource the server enforces; create, edit, and delete go to `rowsOf`, the `elements` form is built for it, and the row page reads it. The list always requests the view's primary-key columns, read from the metadata, so an operation lifts the key the row carries — an association view keyed like its table is deleted from the list. `rowRoute` names a field of the view: a row opens that field's `enumeratedResource` on its page (the one `resourceRoutes` registered for it, else its metadata route) by the row's value in the field, and the View column is drawn when the caller may Read the target. Without `rowRoute` a row opens the write resource by its single key, and a compound-key resource with no `rowRoute` draws no View column. A `rowRoute` naming a field with no resource in the metadata throws when the page is built, naming the field. The list configuration cannot name another resource: `overrideResource` and `viewResource` are gone, and a config setting either fails to type-check.

A list pages on the server, and only there. The list component holds one page of rows — the resource's declared default page size (`@page`), or the config's `pageSize` — and the grid renders it and emits intents: a header click is the next sort, a filter menu is the next column filter, and the pager's First, Previous, and Next follow the cursors the server issued, with the total the first page answered kept while turning. Sorts and filters travel as request parameters, so the server orders and narrows every row it holds, not the ones the browser happens to have; a change to either asks for a first page again. The grid offers the server's filter grammar and no other: the ten comparisons on a column the generated metadata marks `filterable` — an indexed column always, an `allow_filter` column only once an indexed filter is in the request, since the server accepts it only beside one — and "Starts with", "Ends with" and "Contains" on a column whose field's metadata lists the match (`FieldMeta.search`), which stand alone, so a filter the grid offers is one the server answers. A column's `filterable: false` in the config removes a control the metadata would draw. The menu edits a draft: Apply, Enter in its value, or closing the menu commits it, and a new operator alone sends nothing, so an operator and a value changed together are one request, and a null test chosen alone commits as the menu closes. A `civildate` column's menu takes its value from a date control, and a `date` column's (a timestamp) from a date control and an optional time of day, sent as `2026-12-15` or `2026-12-15T09:30`, which the server reads as the whole of that day or minute in the person's zone, sent on every request as the `Time-Zone` header by the resource client (the `@impulseframework/resource` README, Time zone header), so a list filters by the day the grid shows; "One of" and "None of" on those columns take typed dates, comma-separated, in the forms the server reads (`2026-12-15`, `12/15/2026`, `2026-12`, `2026`). A list over a resource with search fields (`ResourceMeta.searchFields`) draws the search box above the table unless the config's `showSearchBox` is false: its placeholder names the fields it searches for this caller (the resource's, less those the digest denies for List; a child's field, listed under the child's name, judged by the child field's grant on the child resource through `ResourceMeta.searchBranches`), the hint under it names the fields the role leaves out, typing commits after a pause and is sent as the filter's `*:search:"…"` conjunct, the title's count becomes the matches, a companion column lights up while the box is active, and clearing the box restores the unnarrowed page. Which fields the box searches is the resource's and the caller's grants, never the page's. The request travels in the body, as the server requires, exactly when the page's filter, a column filter or the box reaches a field the metadata marks `pii` (`FieldMeta.pii`, a branch's `pii`); `disableCacheForFilterPii` forces the body for a filter the metadata cannot read. Selection is held as rows and spans pages. `ListViewConfig.limit` is gone: a list never gathers every row for the browser to slice, and the row-count title reads the server's total.

A list page over a key-less resource draws the whole list as one page. A `@computed` or `@virtual` struct with no `@primarykey` is served whole: its generated descriptor lists `keys: []` and the list operation alone, the client sends no `limit` and no `cursor` for it, and the metadata carries no key field. The page shows every row the server returned, the pager reads one page with Previous and Next disabled and the count, and each row is identified by its position, since a row has no key: no View column, no create, no delete, and no row route (`resourceRoutes` builds none and registers the page as nowhere a row opens). Keeping such a list a manageable size is the resource author's responsibility; `enableVirtualScroll` on the config renders the rows in view and scrolls the rest. A config asking the page for what it cannot have fails when the page is built, naming the resource and the reason, rather than being ignored: a `pageSize` (the list is served whole, no page size), `enableRowExpansion` (no key to open a row by), and a `rowRoute` naming a field with no target (refused on any resource). The grid itself identifies a row by its `rowKey` input, a function from a row to its identity that the list page fills with the row's key fields joined, so selection and expansion are right on a compound key and on a key not named `id`; a grid given no `rowKey` identifies rows by position.

Every field renders by its generated display type, in a grid cell, a view, and a form, and the three shapes with no editor yet are presented and never typed:

| Display type                                    | Grid cell                                                                                                        | View                                                                | Create and edit form                                                                                                                                                                                                                                                                                                      |
| ----------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `string`, `uuid`                                | the text                                                                                                         | a read-only text input                                              | a text input                                                                                                                                                                                                                                                                                                              |
| `number`                                        | the number                                                                                                       | a read-only number                                                  | a number input                                                                                                                                                                                                                                                                                                            |
| `boolean`, `nullboolean`                        | `true` or `false`                                                                                                | the checkbox or the tri-state control, read-only                    | the checkbox or the tri-state control                                                                                                                                                                                                                                                                                     |
| `date`, `civildate`                             | M/d/yyyy                                                                                                         | a read-only date                                                    | a date picker                                                                                                                                                                                                                                                                                                             |
| `enumerated`                                    | the key                                                                                                          | the picked row's display, read-only                                 | the picker                                                                                                                                                                                                                                                                                                                |
| `bytes`                                         | the size (`32 B`, `12.4 KB`), never the base64                                                                   | the size and a download of the decoded bytes, named after the field | the same, read-only; no editor: binary content is written through `@upload` and `@file`                                                                                                                                                                                                                                   |
| any array (`number[]`, `string[]`, `date[]`, …) | the elements, each by its element type, joined with `, `                                                         | the elements as chips                                               | the same, read-only; no editor yet                                                                                                                                                                                                                                                                                        |
| `object`                                        | the JSON on one line, cut with an ellipsis                                                                       | the JSON pretty-printed                                             | the same, read-only; no editor yet                                                                                                                                                                                                                                                                                        |
| any type flagged `writeOnly`                    | never a column: a `listColumns` entry naming one fails when the page is built, naming the resource and the field | absent, since the server never returns it                           | a blank input in create, and in edit over a row read without a capability envelope; the create or the patch carries the field only when a value is typed. A row's Update envelope is planned over the read projection and never names a write-only field, so under one the edit form keeps the field in view mode, absent |

A column can render its field as an icon with a tooltip instead of text. An `iconConfig` on the column declares the map: `icons` keys a Material icon by the value's text (`true` and `false` for a boolean, the key for an enumerated field), `tooltips` gives the words per value, and `empty` the icon and words for a null. A sentence may name the row's fields in braces, `'Grandfathered: {name} has {activeLearners} active learners. The partner limit is {partnerLimit}.'`, and the list requests those fields beside its columns and fills them by their display types; a value with no sentence uses the cell's formatted text as its words, and a value with no icon keeps the text, so declaring icons for some values and not others degrades to text. The list resolves the cell while it builds the display row, as it applies every other column's formatter, and the grid renders the icon as an image with the words as its accessible name and tooltip, focusable so the tooltip opens from the keyboard; nothing runs at render time. The column stays a field column: its header, sort and filter work on the field. A column is a field column, a button column or an icon column, and a config naming both a button and an icon does not compile. An icon that depends on more than one field is a computed field first; the map then renders it.

A cell with no configured `formatType` is written by its field's display type, and a null is the column's `emptyDataValue`. A create or edit form never offers a free-text control over bytes, an array, or an object, so no page can send the server a value it refuses: their controls stay in view mode whatever the form's mode, start null in a create form, and a null control leaves the add operation, so a `NOT NULL` bytes, array, or object column cannot be created from the generated form until its editor lands. An empty array shows the empty placeholder, as a null does.

A config-driven page can work in a tenant. Provide `RESOURCE_DOMAIN` with a signal of the selected tenant (your tenant picker's selection): the store binds every request for a domain-scoped resource to it, and asks again when it changes; a global resource's request never carries it, and a page over one asks nothing when the tenant changes. A permission question about a domain-scoped target whose scope names no domain is asked in that tenant's digest. A domain-scoped resource's metadata route carries the tenant parameter in braces, so such a page sets `routeData.route` to the path it lives at. That route may carry the tenant parameter as the router names it (`sectors/:sectorID/missions`: the API's domain route segment, the descriptor's `domainRoute.param` after a colon, then the page), mirroring the API's URL, and the links the page builds from it follow the page's tenant: the View column, the table button, the after-create navigation, and the page another list's rows open on put the tenant in the parameter's place, read from the route's parameter when the page is under it and from `RESOURCE_DOMAIN` otherwise, so a row link is the row's real URL and opens in a new tab. A route naming no parameter is used as written. The resolver is `pageRouteResolver()` from `@impulseframework/resource-angular/resource-nav`, for an application's own links from a registered route; it refuses a route naming the parameter when no tenant is known, naming the route.

A list page or a record page can be live: it stays current without polling and without refetching while nothing changed, and a page left and opened again inside five minutes is served by the browser with no request. A page is live by default when the generated metadata says its resource's changes are published (`ResourceMeta.live`, true on every table-backed resource); `live: false` on its `listViewConfig` or `viewConfig` opts it out, `live: true` on a resource whose metadata says its changes are not published (a view or a computed resource) is announced once and asks plainly, and metadata from a generator that predates the field makes no page live by itself (the list page's row page follows the list's config). Provide the change feed once in the application's providers: `{ provide: CHANGE_FEED, useFactory: () => firestoreChangeFeed() }` from [`@impulseframework/resource-firestore`](../resource-firestore/README.md). `AuthService` starts the client's live session with the feed when the session first authenticates, fetching the feed's identity from the API, and stops it at logout, before the session is logged out, so every subscription of the user is deleted and the feed's identity revoked first. From then on the store's list page carries the client's live option (the tab subscribes to the resource in the selected tenant, and a change the feed reports requests the page again by its own cursor, with the change's version) and the row page carries it for its row (a change reads the row again, the form and the children staying on screen while the read is in flight); a resync reads both again by a fresh seed, and a tenant change moves the list's subscription. The page's other readers (pickers, referenced-resource lookups, array views, create forms) never ask to be live. Without a provided feed, or on an outlet that serves no live subscriptions, a page configured live asks as a plain one: nothing is cached, nothing is subscribed. What the option opts into, what it costs, and what it never does are in the client's README under "Live pages".

A feature can ship dark and be turned on per environment, and the pages follow the switch. For a feature with a resource behind it there is nothing to add: while its flag is off the server leaves the resource out of the permission digest, so the menu item, the buttons, and the row page are hidden by the digest alone, and `resourceRoutes` adds a match guard from the metadata's `feature`, so a dark URL falls to the application's wildcard route as any unknown URL does. For a feature with no resource behind it the library offers three gates, each taking a member of the generated `Feature` union so a misspelled flag fails to compile: `*impulseFeature="Feature.Debriefs"` on an element, beside `*impulseHasPermission`, renders it while the flag is on; `featureMatch(Feature.Debriefs)` in a route's `canMatch` matches the route only while the flag is on; and `feature` on a `MenuItem` (and on the sidenav's `NavItem`) beside `permission` hides the item while it is off. `AuthService` loads the enabled set once when the session first authenticates, beside the digest and the domains, and forgets it at logout; `featureEnabled(name)` answers from it, signal-backed, and `ensureFeature(name)` loads it first for a guard that may run before the session's first check. On an API that serves no feature flags (a descriptor without the `features` route, from a generator that predates them) `AuthService` skips the load and every gate answers off: `*impulseFeature` renders nothing, a `featureMatch` route falls to the wildcard, and the dialog says the API serves no feature flags; the first ask of each flag is announced once through the client's `warn` option (the console by default), naming the regeneration that would serve it. Nothing polls and nothing is pushed: a flip by someone else is seen at the next sign-in, by design. The library ships the flags dialog, `FeatureFlagsDialogComponent`, opened with `openFeatureFlagsDialog(dialog)` from wherever the application puts the link: it lists every declared flag with its description and a toggle, says how long an enabled one has been on in this environment, and flips a flag through the generated `SetFeature` method, after which the client reloads the enabled set and the digest so the person flipping sees their own pages follow at once. The digest decides what the dialog may do: without Execute on the method the toggles are disabled and the list is read-only, and a refusal is shown in the dialog in the server's words.

An application installed as a progressive web app can tell the person when a new build is ready and reload only when they choose. A progressive web app is one the browser can install, with a service worker: a script the browser runs apart from the page, which keeps the application's files and downloads a new build when one is deployed. The library provides this through a secondary entry point, `@impulseframework/resource-angular/ui-app-update` (an import path of its own beside the main package), which exports `provideAppUpdate()`. The application adds `provideAppUpdate()` to its providers beside Angular's `provideServiceWorker(...)` and nothing more: no component references the service, and the notices it raises render in the alert area every application already has, the `impulse-alert` list over `NotificationService.notifications()`. `provideAppUpdate()` starts `AppUpdateService`, which listens to `SwUpdate`, Angular's service for the worker's events, from `@angular/service-worker`; that package is an optional peer dependency of this library: an application installs it when it uses this entry point. The service is inert when no service worker is provided or the worker is disabled, as it is in dev mode and in component specs: no timer starts and nothing is raised. When the worker reports that a new version is ready (`VERSION_READY`), the service raises one persistent notice, "A new version of this application is ready.", with a Reload action; it never reloads on its own, and the person chooses when. When the worker reports a state it cannot recover from (`SwUpdate.unrecoverable`), the service raises the same kind of notice, "This application needs to be reloaded to keep working.", with Reload. Checks for a new version run hourly while the browser tab is visible and once whenever the tab becomes visible again, and none run while the tab is hidden, so a hidden tab never wakes a server instance for nothing; the worker itself also checks after every page load. The hourly timer runs outside Angular's zone, the layer that tracks pending timers and requests and reports the application stable, or settled, once none is left, so it never keeps the application from settling. Every reload goes through one injectable class, `AppReloader`, whose `reload()` calls `location.reload()`, and a spec provides its own in its place to observe a reload. The service also exposes four members other parts of the library use: `ready`, a signal that is true once a new version is ready; `checkNow()`, which asks the worker to check and resolves true when a new version is ready; `reload()`; and `reloadWhenReady()`, which reloads onto the new version with no notice as soon as the worker has one ready, at once when one already is. To carry the Reload action, `CreateNotificationMessage` and `NotificationMessage` (in `@impulseframework/resource-angular/types`) gained two optional fields: `action`, a `NotificationAction` with a `label` and a `run` callback, which `AlertComponent` renders as a button beside its close; and `persistent`, which makes the alert skip its auto-dismiss timer so the notice stays until it is closed or dismissed by its id. A notice that sets neither renders and expires exactly as before. A related fix in `IdleService` (`@impulseframework/resource-angular/auth-idle-service`) moved its keep-alive, the periodic session check, so it now starts in `start()` and ends in `stop()`, as the idle ticker (the timer that watches for inactivity) already did, where before it started in the constructor and never stopped. A signed-out application therefore has no timer due and can settle, which is what lets the service worker register as soon as the application is stable. No request behavior changes: the keep-alive already sent nothing without a session.

The application reloads by itself in one case: when the server refuses this build's release, `AppUpdateService` picks up the server's build. While the application is still starting, meaning its router has not yet completed a navigation (`Router.navigated` is false, so no page is on screen yet), the service calls `reloadWhenReady()`: the worker is asked for the new build, and the document reloads onto it as soon as it is ready, with no notice. When there is no service worker, or no new build is ready within ten seconds (`RELOAD_WAIT_LIMIT`), it does an ordinary reload. Once a page is on screen, the service reloads nothing by itself and raises one persistent notice, "A new version of this application is required. Reload to continue." (`OUT_OF_DATE_MESSAGE`), with Reload. A tab reloads by itself at most once per server release: the guard is the server's release, which the service keeps in `sessionStorage` (the browser's storage for one tab, which survives a reload) under `impulse.app-update.reloaded-for` (`RELOAD_GUARD_KEY`), with every read and write wrapped in try/catch, and a refusal past the guard, one for a release the tab has already reloaded for, raises the notice instead of reloading. When the server runs an older release than this build, as after a rollback, the client has already retried the request, and a plain reload would not help, since the worker would serve this newer build again; so the service unregisters every service worker (`AppReloader.unregisterWorkers()`) and then reloads onto the server's build, whether or not a page is on screen. Past the guard it raises "The server runs an older version of this application. Reload to continue." (`SERVER_OLDER_MESSAGE`), whose Reload does the same.

The application tells the person when the server is down for maintenance and takes the notice down by itself when the server is back. The deploy pipeline starts a release that interrupts the database behind a maintenance revision of the application, which answers every request with status 503 and the marker header `X-Maintenance: 1`; the client throws such an answer as `MaintenanceError`, and the client's README says how it tells it from an ordinary 503. The client's error hook hands it to `MaintenanceService`, a service of the same entry point beside `AppUpdateService`, through the `MAINTENANCE_HANDLER` token, which `provideAppUpdate()` provides too. The first answer of an outage raises one persistent notice, "This application is down for maintenance. It comes back by itself when the work is done." (`MAINTENANCE_MESSAGE`), with no action, since there is nothing the person can do; it is the library's sentence, never the server's text or status; and the answers that follow while the notice is up change nothing, so an outage raises one notice, not one per failed request. While the notice is up the service checks back: the first check after the server's `Retry-After` (`MaintenanceError.retryAfter`), or fifteen seconds (`MAINTENANCE_CHECK_START`) when the server named none, and each check that finds the server still down doubles the wait up to two minutes (`MAINTENANCE_CHECK_CAP`); no check runs while the tab is hidden, one runs as soon as it is visible again, and the timer runs outside Angular's zone as the update service's does. The check is `MaintenanceProbe.stillDown()`: one HEAD request for the application's own entry document under its base href (`document.baseURI`), the request the maintenance page's own check-back makes. It needs no session and no database and carries no body either way: a maintenance server answers it 503 with the marker, since it is not a navigation, and the application's server answers it with the document's headers. It is sent past the service worker with the `ngsw-bypass` header (`WORKER_BYPASS_HEADER`), which Angular's worker honors, so the worker never answers it from its cache of the application's files, and past the browser's cache (`cache: 'no-store'`); a request that gets no answer at all counts as still down, since the server is between revisions. The first answer without the marker, whatever its status, is the recovery: the notice is dismissed, the checks end, and the service calls `AppUpdateService.checkNow()`, so a new build installed meanwhile raises the update notice, and the next request of the client is answered, or refused under the release rule above, which does the rest. `MaintenanceService.down` is a signal that is true from the first answer of an outage until the recovery, and a spec provides its own `MaintenanceProbe` to script an outage.

Every application provides its generated client: `provideResourceClient((options) => createApi({ baseUrl: environment.apiUrl, ...options }))` in the application's providers, with `API_URL`, `BASE_URL`, and `API_VERSION` beside it. `API_VERSION` is the release this build was made from, as the build stamps it (into an environment file, for example), and is empty or `dev` for a local build; `provideResourceClient` hands it to the client as `ClientOptions.apiVersion`, so every request of the client carries it in the `X-Api-Version` header. `provideResourceClient` also hands the client's live session the XSRF token Angular's extractor reads from the cookie `withXsrfConfiguration` names, so the page-leave unsubscribe, which rides a keepalive fetch rather than HttpClient, echoes the application's cookie and the cookie is named once. The library's store, pickers, guards, and `AuthService` all speak through that client, and an application that provides none fails at startup naming this call as the way in. The client is the one place a response becomes an error, and the adapter renders its judgment five ways. A 401 is the session gone: the client's error hook keeps the attempted URL in `AuthService.redirectUrl` and returns the browser to `FRONTEND_LOGIN_PATH`, where the login page reads the URL once the session is back (on the login page or the sign-out page already, nothing moves and nothing is kept). An `ApiError` nobody caught raises one global error notice in the server's words (the body's `message`, else `HTTP <status>`), through the `ErrorHandler` the same call provides, and a request that got no response at all raises one saying so; a refusal a page reports in place raises nothing, since handling the error is what silences the notice: the store's `pageError`, the row page's `viewError`, a component's own refusal signal, and a method's declared answer (`@answers`) all render where they happen and reach no notice. A 412 carrying the server's release is the server's refusal of this build, `ApiVersionError`: the client's error hook hands it to the update service through the `VERSION_REFUSAL_HANDLER` token, which `provideAppUpdate()` provides, and `ResourceErrorHandler` raises no notice for it, since the update service reloads or raises its own. A build that names a release without `provideAppUpdate()` fails at startup naming `provideAppUpdate()`. A 503 carrying the maintenance marker is the server down for maintenance, `MaintenanceError`: the hook hands it to the maintenance service through the `MAINTENANCE_HANDLER` token, which `provideAppUpdate()` provides too, and `ResourceErrorHandler` raises no notice for it, since the maintenance service raises its own; without `provideAppUpdate()` the answer goes to nobody and raises no notice, and a 503 without the marker is an ordinary error and raises the notice in the server's words. The library does not recognize the refusal, or the maintenance answer, of a hand-written `HttpClient` call: `apiVersionInterceptor`, below, adds the header to such a call, the application's own error path sees the 412 or the 503, and the next request of the client triggers the update service or the maintenance service. Every request the client sends begins and ends `UiCoreService` activity, so `isLoading` and the progress bar move while any request is open. The library ships one functional `HttpClient` interceptor, a function `HttpClient` runs over each request it sends: `apiVersionInterceptor` (exported from `@impulseframework/resource-angular/resource-client`), which adds the same header to the application's own same-origin `HttpClient` requests (those sent to the application's own scheme, host, and port), a login form's POST, for example. The application registers it as `provideHttpClient(withInterceptors([apiVersionInterceptor]), withXsrfConfiguration({ ... }))`. It adds nothing for a build that names no release or `dev`, leaves a request to another origin alone, and keeps a header already on the request. `HttpClient` still keeps the XSRF cookie echo (`withXsrfConfiguration`). A provider placed after `provideResourceClient` that re-provides `ErrorHandler` would silence the notice, so startup fails naming the way out when the handler in effect is not the library's: `importProvidersFrom(BrowserModule)`, which `BrowserAnimationsModule` carries, does this, and `provideAnimationsAsync()` is its replacement; an application's own handler extends `ResourceErrorHandler`. Hand-written `HttpClient` traffic outside the client is the application's to render.

An application that signs people out when they stop using it starts `IdleService` (`@impulseframework/resource-angular/auth-idle-service`) once, after sign-in, and stops it at sign-out; the skeletons do this in their root and login components. The service counts the seconds since the last activity (a mouse movement, a key, a click, a scroll, a touch) and, for the last `IDLE_WARNING_DURATION` seconds of the `IDLE_SESSION_DURATION` (sixty of three hundred by default), shows one persistent notice in the alert area, "You will be logged out in N seconds due to inactivity.", counted down in place; persistent so the alert's own thirty-second auto-dismiss never hides it before the sign-out. Any activity ends the warning on the next tick, the notice's close button among them, since a click is activity: the application knows the person is back and asks for nothing more. At the end of the session the service runs `IDLE_LOGOUT_ACTION`, stops, and sends the browser to the sign-out route (`FRONTEND_SIGN_OUT_PATH`), where the signed-out notice shows. Every `IDLE_KEEPALIVE_DURATION` seconds (thirty by default) it also asks the server for the session while one is signed in, so a session the server ended is noticed. `IDLE_TIMEOUT_REQUIRE_CONFIRMATION`, which once made the warning hold until the application's own stay-signed-in button called `stayLoggedIn()`, is retired and has no effect: a provider of it keeps compiling, `start()` logs once in the console that it does nothing, and `stayLoggedIn()` stays for an application that still offers a button.

A login page shows text it holds, never text from the URL. A refused OIDC login returns the browser to the login page as `<login page>?code=<code>`, and the code is all the session package sends; its "Login refusal codes" table is the finite list. `LOGIN_MESSAGES` holds one sentence for each of those codes (`DEFAULT_LOGIN_MESSAGES`), `UiCoreService.loginMessage(code)` answers the sentence or an empty string for a code the application does not know, and `publishLoginError(code)` raises it as a global error notification and says whether the code was known. The page reads the `code` query parameter and renders the lookup, so a crafted URL renders nothing. A refusal the server answers on a step of the sign-in itself (a password POST, a confirmation) carries its code in the body, whatever its status, and the page reads it from there: a 401 or 403 for the policy's refusals, and a 429 for the session library's attempt limiter, which refuses a name whose failed password attempts reached the limit with the code `too_many_attempts` and the wait before the next attempt, `retryAfter` in seconds (and the `Retry-After` header). `loginMessage(code, retryAfter)` and `publishLoginError(code, retryAfter)` take that wait and follow the sentence with it, "Too many attempts. Try again in 14 minutes.": in minutes from a minute up, rounded up, and in seconds below it ("Try again in 45 seconds."); without a wait the sentence stands alone ("Too many attempts."). An application whose session data resolver refuses with a code of its own, or that wants a default reworded, adds `provideLoginMessages({ not_provisioned: 'Your account has not been set up yet.' })` to its providers: the library's sentences come first and the application's after, and a reworded `too_many_attempts` is followed by the wait the same way.

## The login page

`<impulse-login-methods>` (`@impulseframework/resource-angular/auth-login-methods`) renders the sign-in methods
the server offers and owns the journey from the list to the session. On arrival it reads the
methods list through the application's client (`api.auth`, see the sign-in section of
@impulseframework/resource) and renders it: the password form when the list has a password method, asking
for the email address where the list says the username is one, and one button per redirect
method, in the server's order. Nothing it shows is named in the application, so the bundle is the
same in every environment and a method that is not registered has no button. The server's label is
the default for each method; `labels` overrides it by registration name, and nothing else about a
method is the page's to change. Everything around the methods, the branding, a manifest, a
forgotten-password link, is the page's, composed around the component:

```html
<mat-card>
  <mat-card-header><mat-card-title>Lodestar</mat-card-title></mat-card-header>
  <mat-card-content>
    <impulse-login-methods home="/dashboard" [labels]="{ google: 'the fleet directory' }" (signedIn)="go($event)" />
    <p class="hint">Ask the flight desk for an account.</p>
  </mat-card-content>
</mat-card>
```

```ts
export class LoginComponent {
  private idle = inject(IdleService);
  private router = inject(Router);

  constructor() {
    // The page ends no session: the component's first request, the methods list, primes the XSRF
    // cookie the sign-in's POST needs, and a session found standing is offered Continue or Sign out.
    this.idle.stop();
  }

  go(page: string): void {
    this.idle.start();
    void this.router.navigateByUrl(page);
  }
}
```

A password sign-in posts through the client, which reads the list again first so the POST carries a
live XSRF token even on a page left open past the cookie's life; the session established, the
component checks it through `AuthService` (loading the digest, the domains and the feature flags)
and emits `signedIn` with the router URL of the page to go to: the page the login guard turned away
from (`AuthService.redirectUrl`, read once), else `home`. The page navigates there and starts what
it starts on a sign-in. A redirect method sends the browser to the method's path, naming the same
page as the one to return to, resolved against the document's base; the server's callback brings
the browser back there signed in, or back to the login page with `?code=` for a refusal or
`?pending=` for a step that waits.

A page that offers its people as cards, as a demonstration does with its personas, fills the
password form with the component's `prefill(username, password)`; the person still presses Sign
in. The form's fields are bound to its inputs, and the component is drawn again only when
something of its own changes, so a field set from outside does not reach its input: `prefill`
sets both and has the form drawn.

A `returnUrl` in the login page's own URL names a browser path to return to, as the server sends
one: the persona sign-in's picker, a page the server serves behind the session, sends a browser
without one to the login page naming itself. The component keeps the address through the sign-in's
steps, names it as the page a redirect method returns to, and once the session stands sends the
browser there with a full navigation instead of emitting `signedIn`, since no route of the
application draws it; the address the pending status reports after a redirect method's round trip
is followed the same way. Where the address came from decides, not its path: a page the guard
recorded is the application's and is routed to, one the server handed over is loaded. Only a path
in this application in canonical form is kept (`returnPath` in `@impulseframework/resource`, the server's
own rule), and never the login page, the sign-out page or another origin: anything else is dropped
and the sign-in lands as if the URL named nothing. The page needs nothing for this.

After a refusal, every redirect button asks the provider for its account chooser
(`prompt=select_account` on the method's path) and says so ("Choose a Google account"), so a
person refused with the wrong account picks another instead of being sent round the same loop by
a provider that signs its one signed-in account in again without asking; on a stop without a
refusal, one small "Use a different account" link under the redirect buttons puts the card in the
same state (the buttons read "Choose a … account" and a note says the provider will ask), and
until it is pressed a redirect button asks nothing, so the single-account person pays no click. An arrival from a deep link (the guard recorded the page) into
an application whose only method is a redirect forwards to it at once, rendering nothing: the
provider signs the person in and returns them to the page, an expired session included. The page
stops, with its buttons, when the guard recorded nothing (a direct visit, a bookmark, the page
sign-out lands on), when the URL carries a refusal code, a step or a mailed link's token, when the
list offers more than one method or a password, and when a session is found standing.

The page ends no session on arrival, so a link to it signs nobody out: a bookmark, the back
button, a mailed link. With nothing in the URL to act on, the component reads the session after
the list, and one it finds standing takes the list's place with who is signed in, Continue (the
session is checked and `signedIn` emitted, as after a sign-in) and Sign out (the session ends and
the list shows, so the form posts on no session); one marked for a password change gets the change
form. Everywhere else, signing out is a navigation to the sign-out route, below.

A refusal arriving in the URL renders the sentence the application holds for the code
(`LOGIN_MESSAGES`, the library's sentences plus `provideLoginMessages`) and nothing for a code it
does not know; one the server answers on a step renders that sentence when its body carries a
code, whatever the status, and the server's words otherwise (a wrong password). A name whose
failed password attempts reached the server's limit is refused with 429, the code
`too_many_attempts` and the wait, on the password form and on the confirmation alike; the
component renders the sentence with the wait, "Too many attempts. Try again in 14 minutes.", and
leaves the form, or the pending step, where it is for the retry. A pending step, entered with `?pending=` or answered
to a password sign-in, is read from the pending status route and takes the list's place: for a
confirmation, the password form of the user the sign-in matched, posting to the confirmation route,
with a cancel that returns to the list and emits `cancelled`; a step the server names a page for
(a pending hook's `redirectUrl`) sends the browser there. A step that has expired returns to the
list with the sentence for `pending_expired`. An arrival whose reads fail (no answer, or a gateway's
502 or 504 with no words to show) renders the sentence held for `unreachable`, "The server did not
answer. Try again in a moment.", and a Try again button that runs the arrival again, in the list's
place; the status itself is never shown.

A sign-in the policy holds for a second factor shows the sign-in code step when the policy accepts
the mailed code: the code is mailed at once to the address the account holds, and the step says
where it went (the address masked, `n•••@fleet.example`) over the code field, a "Remember this
browser" box, Sign in, and Send a new code, which replaces the code standing. A person whose
account holds no email address is asked for one first, and it is written on the account,
confirmed, when its code is entered. A wrong code shows the sentence for `invalid_code` and keeps
the form, the code standing with one fewer attempt; a code entered after the one sent expired or
ran out of attempts shows the sentence for `code_expired`, and a new one is a click away; an
address sent three codes within fifteen minutes is refused with `too_many_attempts` and the wait,
the send button then standing in the form's place. With the box checked, the server remembers the
browser, and this person's next sign-ins from it skip the code until the server forgets it, or an
operator does from the user-management page. A step this page does not render, a reason it does
not know or a second-factor step without the mailed code, shows a note saying the page is older
than the server, asking the person to reload and try again, with the cancel.

The package declares the sign-in steps the component renders under `impulseframework.signInSteps` in its
package.json (`confirmation`, and `mfa:email-code` for the second factor by the mailed code), the
same list as the component's `SIGN_IN_STEPS`. The framework's release check holds the steps a
generated server asks the page for to that declaration, so a release never pairs a server with a
page that cannot render what the server asks; a new step is added to the component, the list and
the declaration together.

A session the sign-in establishes may carry `passwordChangeRequired`: an operator set the password
(the seed's first administrator, a user created or reset through the user-management surface), and
the server serves nothing but the password change, the session read and the logout until the person
sets a password of their own, refusing everything else with a 403 and the code
`password_change_required`. The component renders the change form in the list's place, with the
sentence the application holds for that code over it, posts the old and the new password through the
client (`api.auth.changePassword`) to the change path the methods list names, checks the session
again and only then emits `signedIn` with the page recorded at the sign-in; a refusal is rendered in
place, and a cancel ends the session and shows the list again. `AuthService.checkUserSession` loads
no digest, domains or flags while the mark is set, `LoginAuthenticationGuard` returns such a session
to the login page as it returns an unauthenticated one, and the client's error hook treats the 403
as it treats a 401, so a marked session that reaches a page is back on the login page before the
page renders.

The server's mailed links all lead to the login page, each with its token in the URL, and the
component follows them from the routes the methods list names, so a server that sends no mail shows
none of this. An invitation (`?invitation=<token>`) is opened on arrival and stands over the list
as the invitation screen: "You're invited to <tenant>" and the address, over every way that can
accept this invitation for that address and none that cannot. A directory registration that could
admit the address is a "Continue with <label> as <address>" button, which sends the address to the
provider as its login hint, so the provider takes that account and skips its chooser; the password
form shows when an account with a password holds the address ("Sign in with your password to
accept it."); and where no account holds the address and the server offers a password method, the
account form opens by itself, asking for a username (unless the username is the address) and the
password twice, which creates the account, signs the person in and accepts the invitation, but only
where no directory could admit the address, since the invited person has that account already. The
"Use a different account" link is not offered while an invitation stands: the invitation names the
account. A person the page finds already signed in may accept it as they are with one click, from a
button under the heading, when their account holds the invited address or no address at all; signed
in as an account that holds another address, they are told whose the invitation is and how to accept
it, and the server refuses that acceptance too. Under an older server that does not say whether an account holds the
address, every path is offered as before, the account form behind a "Create an account with a
password" button last. The browser keeps carrying the invitation when a redirect method refuses the
sign-in and returns to the login page with `?code=` and no token; the methods list then names it
(`invitation.standing`), and the component shows the screen again, with the refusal's sentence under
it. A directory sign-in as another account than the invited one is refused by the server
(`invitation_address`), and the screen says "This invitation is for <address>. Sign in with that
account.", the button now asking the provider for its chooser. A token in the URL is opened instead,
which also refreshes what the browser carries. A sign-up
link (`?signUp=<token>`) opens the same
account form in the list's place, and the list of a server whose password method offers sign-up
carries a "Create an account" step, which asks for an address and tells the person the link is on
its way, whatever the address's state. A confirmation link (`?verify=<token>`) is posted once a
session stands: on arrival when the page finds one, else after the sign-in it waits for (a redirect
method returns to this page with the token, since the round trip reloads the application); the
component then says the address is confirmed, or renders the server's refusal, and emits `signedIn`
on Continue. A link that has expired or was already used renders the server's words and the list.
On arrival the component holds the list back: a progress bar stands in its place and no form shows
until the reads have answered, so a person found signed in goes straight on to what the link asks
for (the address confirmed, the invitation to accept as they are, the pending step), or with
nothing in the URL is offered Continue and Sign out, and is never shown a form to fill in; the form
shows only to a person the session read finds signed out.
Canceling the account form or the sign-up shows the list again and emits `cancelled`.

## Signing out

`<impulse-sign-out>` (`@impulseframework/resource-angular/auth-sign-out`) is the page the application serves at
its sign-out route (`FRONTEND_SIGN_OUT_PATH`, `/logout` by default), the one place a navigation
ends the session. On arrival it stops the idle watch, ends the session through `AuthService.logout`
(the live session first, then the session's DELETE, then `LOGOUT_ACTION`) and forwards the browser
to `FRONTEND_LOGIN_PATH`, whatever the server answered, since a refusal there is a session it no
longer held. A sign-out control is a link to the route, `IdleService` sends the browser there when
the session times out, and the client's error hook leaves the page alone as it leaves the login
page: a 401 on either moves nothing and records nothing.

```ts
{ path: 'logout', loadComponent: () => import('@impulseframework/resource-angular/auth-sign-out').then((m) => m.SignOutComponent) }
```

```html
<a mat-stroked-button routerLink="/logout">Sign out</a>
```

## The email address

`<impulse-email-address>` (`@impulseframework/resource-angular/auth-email-address`) is the signed-in person's
email address for an account page. It reads the address from the session the `AuthService` last
checked (the address, whether the person confirmed it, and the address a directory asserted at a
sign-in, offered as a prefill while they hold none) and, where the methods list names the email
routes, offers to add, change or confirm it: the person types an address and the server mails it a
confirmation link; nothing is written until the link is followed on the login page, so an address
never attaches to an account its follower does not control, and the component says the link is on
its way whatever the address's state. A refusal (not an address; a mailbox sent too many links,
with the wait) is rendered in place.

```html
<impulse-email-address />
```

## The release

`<impulse-release>` (`@impulseframework/resource-angular/ui-release`) is the release the application runs, for
the login page and the footer of every page. Every answer of the server names the release it was
built from in `X-Api-Version`, the client keeps the last one named (`client.serverRelease`, a store
read with `get()` and watched with `subscribe()`), and the component renders it: `v0.3.1` for a
release, a pull request's build as the pipeline names it, `dev` for a server built locally. Nothing
renders until the client's first answer, which on the login page is the methods list the login
component reads. When this build of the browser application names a release (`API_VERSION`) that is
not the server's, a tab kept open across a release until the update notice is taken, the line adds
which build the tab runs. Nothing about the application is named in it; the page puts its own name
beside it where it wants one.

```html
<impulse-release />
```

## User management

`<impulse-user-management>` (`@impulseframework/resource-angular/impulse-user-management`) is the page that
administers the users of one Auth, written once for every application. It configures itself from
the `userSurfaces` block of the generated descriptor (see the user-management section of
@impulseframework/resource): `auth` names the surface, the descriptor's first when omitted, and `domain`
binds the tenant partition, the global partition when omitted. An application with route-parameter
tenancy passes the route's tenant; one bound through `RESOURCE_DOMAIN` passes that signal's value.
Nothing about the application is named in the page, so a console that manages its staff and, by the
generator program's declaration, its members composes the component twice:

```html
<impulse-user-management />
<impulse-user-management auth="Members" />
<impulse-user-management [domain]="tenant()" />
```

The page follows the pattern of a cloud console's IAM page. The table has one row per user: the
username, the email address and whether the person confirmed it, the status, how they sign in (the
password, and each linked identity by its provider and the organization the link carries, "Google
(fleet.example)"), the roles and the last sign-in. The roles column lists the user's roles one per line, each by its title
with where it reaches beside it. A role's title is its name split at the capitals, so
`FeatureAdministrator` reads "Feature Administrator" (`roleTitle` and the `roleTitle` pipe, exported
from the same entry, for a page that shows a role elsewhere); where it reaches comes from the roles
resource's `kind` and from the partition: in an application with tenants, "Everywhere" for a global
role, "Every tenant" for a seat held in every tenant, and the tenant's name under its own partition;
an application without tenants says nothing, since every role then reaches the whole of it. A row
whose seats a directory writes (`rolesSynced`) says "From the directory". Each row ends in a menu
button ("Actions for hollis") that names every action the caller may take on that user: Edit
access (View access where the caller may change nothing), Rename, Change email address, Set a
temporary password, Forget remembered browsers, Unlink for each linked sign-in method, then, after
a divider, Disable or Enable and Delete. An action the caller may not take is not listed. The menu
opens below its button with the right edges aligned, above it when below has no room, and beside it
to its left when neither has, top edges aligned, bottom edges aligned when that does not fit, or
shifted along the button to stay in the window when neither does; it is never over the button that
opened it or another row's, and the button keeps a tint while its menu is open, so the row the menu
belongs to is plain whichever side it took. The panel is the CDK's menu (the menu
and menuitem roles, the arrow keys, typeahead, Escape, focus back on the button), drawn on
Material's system tokens so it reads as Material's own menu does; `rowMenuPositions`, exported from
the same entry, is the list of places.

Edit access opens a side panel on the right edge of the page, "Edit access for hollis", listing the
user's roles under "Assign roles": one row per role, the role's title with, under it, where the role
reaches and the description the role file gives it, and a trash can; "Add another role" ("Add a
role" while there are none) appends a row whose searchable picker lists the roles the partition's
roles resource serves, each option showing the title, where it reaches and the description, narrowed
as the person types over the title, the name and the description; a role the user holds already, or
another row names, is not offered again. Each row acts at once: choosing a role seats it through the
seats resource in one call, and the trash can asks in a dialog ("Remove the Harbormaster role from
nova?") and unseats in one call; the page is read again after each, so the table and the rows say
what the server lists, and a refusal is shown in the panel in the server's words ("Could not add the
Harbormaster role to nova: Harbormaster required"). Done closes the panel. On the global page a seat
held in every tenant is a row like the others, since the global seats resource seated it and unseats
it; in the tenant partition it is shown locked. A role the caller may not change is shown with the
reason under it and cannot be removed: "Assigned by the directory." where the directory writes the
seats (and no role can be added), "You cannot change this role." where the digest withholds the
unseat, and in the tenant partition "Assigned for every tenant. You cannot change it here." under a
seat held in every tenant; the button that adds a role is there only where the digest grants the
seat, and where it does but the user holds every role there is, the panel says so in its place
("hollis holds every role this application has.").

Every other action is one step with one call, asked in a dialog. Rename, Change email address (an
operator's entry is unconfirmed and reserves the address until the person confirms it themselves,
through `<impulse-email-address>` and the login page's confirmation link) and Set a temporary password
(where the Auth has a password method; the person changes it at their next sign-in, through the
login component's change form) each open a dialog with its one field and its one button. Forget
remembered browsers (where the caller may run the reset method: it forgets the browsers the person
asked to be remembered at the sign-in code step, so their next sign-in asks for the code again; a
descriptor from a generator that predates the second factor names no such method, and the action is
not listed), Unlink (the server keeps the last way in), Disable or Enable and Delete ask once in a
dialog that names the person and says what happens, and then make their call. Nothing can
half-succeed. Where the password method declares the username as the address (`usernameIsEmail`),
the username column is the address, the new-user panel asks for it as the username, and neither is
edited by hand: a verified new address renames the person.

"New user" opens a panel of the same shape: the username (or the address), the email address, a
temporary password where the Auth has a password method, "The account starts disabled", then the roles rows and
Create. In the tenant partition the first role is required and rides the create, which seats the new
user in the tenant; every further role, and in the global partition every role, is seated by a call
per role once the server has said the new user's id. A seat the server refuses leaves the user
created and says so: "quill was created, but the Harbormaster role could not be assigned: …".

Below the users stand the partition's invitations, where the descriptor names the resource: an
address invited to hold roles, listed with the roles by title, the inviter when the page lists them,
when it was sent and when it expires, until whoever follows the mailed link signs in (by any method,
or by creating an account with a password) and is seated with the roles, or the invitation is
revoked or expires. The invite form takes the address and the roles; an invitation is the same
request whether an account holds the address or not, and the page does not learn which. The block
is shown when the caller may list or send invitations, and a partition whose descriptor names none
has no block.

Every control follows a capability. The digest decides whether users are created, seats written and
invitations sent; the row's capability envelope, asked for on every read, decides the rename, the
address, the disable, the delete, the methods and the revoke per row, so one's own row loses its
disable and its delete, and a user seated outside the tenant loses the account-level actions,
without the page knowing why. A row with no account action for the caller keeps its menu, with the
access alone, which then shows the roles read-only with the reasons. A role file scopes
all of it with the same conditions as the application's own resources: a role granted `Create` on
the seats resource under `new.role != 'Administrator'` seats people into every other role, and the
refusal of the one it may not is shown in place in the server's words. The lists are read again
after every write, so the rows say what the server says; a list that pages offers the turns the
server names.

The action dialogs are `askInDialog` of `@impulseframework/resource-angular/impulse-dialogs`, one component for a
question with at most one field, which any page may use the same way. The side panels are Material
dialogs anchored to the right edge (`panelClass: 'impulse-users-panel'`);
the library's stylesheet (`@use '@impulseframework/resource-angular/styles'`) squares their corners.

## Tokens

`<impulse-tokens>` (`@impulseframework/resource-angular/impulse-tokens`) is the signed-in person's tokens for the
application's machine outlets, for an account page. It configures itself from the `tokens` block
of the generated descriptor (see the tokens section of @impulseframework/resource): the routes, and the
machine outlets a token can be issued for. The page that composes it names the page; the component
carries no heading of its own. One sentence stands over the form: "A token lets a device or a
program of yours use this application as you. Name it for what it is for, so you know it in the
list." The form issues an application token by name for one outlet, chosen where the descriptor
lists several and fixed, and not mentioned, where it lists one; the token's value is shown once, in
a field with a Copy button (the clipboard where the browser offers one, the field's text selected
either way), with the sentence that it is not shown again. The table lists the person's live tokens
as the server serves them, newest first: an application token by its name, a device grant (the pair
a command-line client holds after the person approved its request) by the client's name and the
kind of each row, access or refresh, the outlet where there are several, when it was created, when
it expires and when it was last used, each to the minute ("Oct 28, 2026, 6:34 PM"). Each row ends
in a trash can that asks in a dialog ("Revoke nightly export?", "A device or a program using it
stops working at once.") and revokes in one call; revoking either row of a device grant revokes the
pair, and the dialog says so. A refusal is shown in place in the server's words, and the list is
read again after every write. Where the descriptor carries no block, the page says in one sentence
that the application issues no tokens for the signed-in person.

`<impulse-device-approval>` (the same entry) is the page where the person answers a command-line
client's request to act as them: the program printed a code and a link, and the person approves
or denies the request the code stands for. The component takes the code as `userCode`, else
reads it from the route's `userCode` query parameter, the link the program printed; with a code
it looks the request up and shows which program asks, for which outlet and until when, with
Approve and Deny; without one it asks for the code the program printed, in any case and spacing,
and looks it up on Continue. After a decision it says so; when no request waits on the code,
expired or already answered, it says so and asks for a code again.

```html
<impulse-tokens />
<impulse-device-approval />
<impulse-device-approval [userCode]="code" />
```

The device page is reached at the application's device verification path (`/device` by
default, what the program prints as its link) with `?userCode=XXXX-XXXX`, and belongs behind the
sign-in guard: a signed-out person who follows the link is sent to sign in and returns to the
page, code and all, once signed in.

## Modules Overview

- **`impulse-resource`**: The core module of the library. It contains the components and services related to dynamic page generation and data management.
- **`impulse-user-management`**: The user-management page over an Auth's surface, configured from the generated descriptor.
- **`impulse-tokens`**: The signed-in person's token page and the device approval page, configured from the generated descriptor's tokens block.
- **`auth`**: This module provides authentication and authorization, including the login methods component, the email address component, route guards, and permission directives.
- **`ui`**: This module contains UI components and services, such as alerts, notifications, the sidenav, the update and maintenance notices, and the theme picker with its service.
- **`utils`**: A collection of utility pipes and functions for various purposes.

## Development

### Building the Library

To build the library locally, copy the fonts it ships (from the two font packages the repository builds with, into `projects/resource-angular/fonts/`, a generated folder) and use the Angular CLI; the root build script does both:

```bash
bun run fonts && ng build resource-angular
```

### Running Tests

```bash
bun run test:angular             # once, as CI runs it
bun x ng test resource-angular   # watch mode
```

The specs run on Angular's unit-test builder (`@angular/build:unit-test`) with Vitest under jsdom
in Node; there is no browser to install. The builder initializes a library's TestBed zoneless, so a
spec drives the component itself: `TestBed.tick()` runs change detection and the effects behind it
(a resource's loader issues its request), the request is answered (`HttpTestingController`'s
`flush`, or the scripted transport below), and `await TestBed.inject(ApplicationRef).whenStable()`
settles the value. `fakeAsync`, `tick`, and `flush` from `@angular/core/testing` are not used:
Zone is not loaded, and nothing in the library advances timers.

#### The testing entry point

`@impulseframework/resource-angular/testing` exports `provideResourceTesting(options?)`: the providers a spec
of a component over the library needs. `RESOURCE_CLIENT` is provided over a scripted transport
(`scriptedTransport` from `@impulseframework/resource/testing`), so no request leaves the test and every
request is on record, and an empty router is provided, since the components read the route and
link to others. By default the client is `createClient` over an empty descriptor at `/api`; an
application passes its generated `createApi` as the `client` factory so the component under test
reads the descriptor its pages read, and its own transport to script the answers and read the
requests back:

```ts
import { ApplicationRef } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { createApi } from './zz_gen_api';
import { SquadronChannelComponent } from './squadron-channel.component';

describe('SquadronChannelComponent', () => {
  it('reads the squadron it is given and shows its callsign', async () => {
    const transport = scriptedTransport({ status: 200, body: { id: 'sq-1', callsign: 'Anvil Two' } });
    await TestBed.configureTestingModule({
      imports: [SquadronChannelComponent],
      providers: [provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: '/api', transport: t }) })],
    }).compileComponents();

    const fixture = TestBed.createComponent(SquadronChannelComponent);
    fixture.componentRef.setInput('uuid', 'sq-1');
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();

    expect(transport.requests.map((r) => r.url)).toEqual(['/api/squadrons/sq-1']);
    expect(fixture.nativeElement.textContent).toContain('Anvil Two');
  });
});
```

The client's error hook and error handler are not provided; a spec of the 401 redirect or the
uncaught-error notice uses `provideResourceClient` with its own transport. A component that reads
the page configuration from the route (`ActivatedRoute.snapshot.data['config']`) is given that
snapshot by its spec; the library's `resource-list-create.component.spec.ts` shows the shape. The
library's own creation specs (`*.component.spec.ts`) are the smallest examples.

### Known Issues

- **Contentful Dependency**: The library currently has a hard dependency on `@contentful/rich-text-types` due to the `CustomTypes.ContentfulDocument` type definition in the resource metadata. This means consumers of the library must have this package installed, even if they are not using this custom type.
