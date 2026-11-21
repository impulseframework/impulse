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

| Token                                   | What it colors                                               | Default               | From the theme                              |
| --------------------------------------- | ------------------------------------------------------------ | --------------------- | ------------------------------------------- |
| `--impulse-accent`                      | navigation links and headers, the active bar, the edit links | `#003b49`             | `--mat-sys-primary`                         |
| `--impulse-surface`                     | the surface a section label and a list sit on                | `#ffffff`             | `--mat-sys-surface`                         |
| `--impulse-outline`                     | a section's border                                           | `#8e8e8e`             | `--mat-sys-outline`                         |
| `--impulse-divider`                     | a section heading's rule, a dotted outline                   | `#ccc`                | `--mat-sys-outline-variant`                 |
| `--impulse-outline-variant`             | a read-only field's focus underline and checkbox border      | `#e0e0e0`             | `--mat-sys-outline-variant`                 |
| `--impulse-hairline`                    | a row separator in a table or dialog                         | `rgba(0, 0, 0, 0.12)` | `--mat-sys-outline-variant`                 |
| `--impulse-shaded`                      | a shaded block                                               | `#e9e9e9`             | `--mat-sys-surface-container`               |
| `--impulse-dirty`                       | a field changed and not yet saved                            | `#00617f26`           | primary at 15%                              |
| `--impulse-muted`                       | a field's prefix and suffix                                  | `gray`                | `--mat-sys-on-surface-variant`              |
| `--impulse-warn`                        | the unsaved-changes word                                     | `#f44336`             | `--mat-sys-error`                           |
| `--impulse-edit-shadow`                 | a form in edit mode                                          | two soft shadows      | `--mat-sys-level2`                          |
| `--impulse-alert-bg`                    | a notice of no kind                                          | `lightgray`           | `--mat-sys-surface-container-high`          |
| `--impulse-alert-warn-border`, `-bg`    | an error notice                                              | `red`, `#fff5f4`      | `--mat-sys-error`, `-error-container`       |
| `--impulse-alert-accent-border`, `-bg`  | an information or idle notice                                | `#dfb51d`, `#fff8e0`  | `--mat-sys-tertiary`, `-tertiary-container` |
| `--impulse-alert-success-border`, `-bg` | a success notice                                             | `#4caf50`, `#f0fff0`  | `--mat-sys-primary`, `-primary-container`   |

The variables the sheet exposed before the tokens stay: `--default-blue` (a form in edit mode) and `--app-background-color` (a container's background) are set by the theme sheet too, from `--mat-sys-primary` and `--mat-sys-surface`; `--layout-label-background-color` and its presets, `--form-edit-mode-bg`, `--underline-fields` and `--sticky-headers` are switches the application flips, not colors the theme decides; the presets that name a surface (white, the default, off-white and grey) are set by the theme sheet from `--mat-sys-surface`, `--mat-sys-surface-container-low` and `--mat-sys-surface-container-highest`, so a section's label keeps its contrast in the dark theme whichever the application picks. The components also read Material's own tokens directly where one fits (`--mat-sys-outline-variant` for a grid line, `--mat-sys-error` for a refusal), each with a literal fallback that applies only on a page with no Material theme at all. A spec in the library holds this contract: every token a component reads is declared with a default, the theme sheet sets every one of them, and no component stylesheet carries a literal color of its own.

**The titles.** The title of a page and the title of a section of it (a list under a row, a create form, a panel) are drawn at two sizes the `styles` sheet declares beside the colors, in the same layer: `--impulse-page-title-size`, 32 pixels, and `--impulse-section-title-size`, 24. On a phone, the width at which the frame's bar folds its menus away (599.98 pixels and under), they are 24 and 20, so a section's title is under its page's at every width. They are lengths, not colors: the theme sheet leaves them alone, an application sets one on `:root` after the `styles` sheet to change it (for every width: to keep a phone's size of its own, it sets that one again under the same `max-width: 599.98px` query), and the spec that holds the color contract holds these four numbers too. A title longer than its line breaks, inside a word where it must, and never runs past the edge of the page. A title shares its row with what the page offers beside it, a list's buttons or a row's Edit, Actions and close buttons: beside the title, sharing the rest of its line as they always have with the last of them at the far end, while they fit that line, and on the next line, again with the last at the far end, where they do not, wrapping again among themselves. So on a narrow screen the buttons are under the title, never over it and never past the page's edge, each with its icon and its words. The rule is written once (`styles/_headings.scss`) and the list page, a row's own page and the create form include it; on a row's own page the row's view draws the page's title in that row, handed it by the page.

**A choice for the person.** An application that offers a choice of looks keeps the Material theme as above and lets the person pick light or dark, and one of the palettes it offers, from `<impulse-theme-picker>` (`@impulseframework/resource-angular/ui-theme`) on whichever page keeps their own settings. `ThemeService` applies the choice to the root element, `color-scheme` as an inline style (the theme emits both values of every token, so the one property flips the page) and `data-palette`, and remembers it in the browser's local storage (`impulse-theme`), so the login page honors it before sign-in and the next visit starts with it. Where the application places the frame (`provideFrame`), the look is kept with the person's account too, so it follows them to another device: when a person's session is first checked on a page the frame applies the look the account holds (`SessionInfo.look`) and remembers it in the browser, and it writes the person's next change to the account (`api.auth.changeLook`). The account's look is taken once for each sign-in: a later read of the session, the keepalive's among them, takes nothing back, so a change made on another device is seen at the next visit. A change writes the whole look as the page shows it, one write at a time in the order the changes were made; a write the server refuses or that does not reach it is dropped, and the page keeps the look. An account that holds no look takes none from the browser, since a browser several people share would hand one person's look to the next; what such a person then picks is kept with what the page was showing. An impersonated session neither takes the look of the person it acts as nor writes one for them. An application that draws its own shell injects `AccountLook` (`@impulseframework/resource-angular/auth-frame`) once to get the same. The application names its palettes twice, by the same ids: to `provideTheme` in its providers, and to the theme sheet's `palettes` mixin in `theme.scss`, which emits the first palette's theme on `html` (the look where no choice was made) and every palette's color tokens under `html[data-palette='<id>']`, with its primary color on `[data-impulse-swatch='<id>']` for the picker's swatch. The `palettes` sheet carries a Workspace palette ready-made, generated from Google's blue and green with Material's theme-color schematic; Angular Material's own (`mat.$azure-palette`, `mat.$violet-palette`, ...) are the rest of the choice. An application that offers one palette, or none, hands `provideTheme` no palettes and the picker offers light and dark alone. An application's own stylesheets follow the choice as far as they read the Material tokens (`var(--mat-sys-surface)` for a page background, `var(--mat-sys-outline-variant)` for a hairline); a literal color stays what it is in dark.

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

How a picker reads that resource is the descriptor's statement alone: its declared maximum page size (`@page max`). No maximum, and the picker reads the source whole in one request (`limit=all`) and resolves the chosen value from that list — an id the list does not hold shows as itself — so a source with no read route (a computed catalog, a key-less view) serves it. A maximum, and the picker pages the source one server page at a time at the descriptor's default size, with Previous and Next inside the panel following the server's cursors and the first page's total, sorted by the configured `sorts`, else the source's `@order`, else the display column; the chosen value is read by key and shown ahead of the page's options whichever page is open, and a source with a maximum serves that read (the generator refuses one that does not). A bounded source the server refuses (no order and no sort) shows the refusal in the server's words under the field, never an empty list. A picker offers only what the person may see of its source, in every application, and what it says about that is the library's own sentence, never the server's answer, which names ids, a permission and a resource. The picker asks the permission digest before it asks the server. A source is closed to the person where the digest grants nothing of what the picker asks of it: List on a source read whole, whose list is all the picker asks; on a paged source, List while the field is edited, and both List and Read while it is read. A closed source is sent no request: the field is read-only, shows the stored id, and says under it "You do not have access to this list, so only the id is shown." (with nothing stored, while it is edited, "You do not have access to this list, so nothing can be chosen here."), and saving the form leaves the stored id as it is. Where the digest is not at hand, the server's refusal of the list as not permitted leads to the same state, and nothing more is asked. Where the source is open and the row the field holds is not the person's to read (the digest grants List and no Read, or the server refuses the read of that row, as a condition on the row does), the field shows the stored id and says "You do not have access to this record, so only its id is shown.", the row is not asked for again, and while the field is edited the picker stays a picker, so a row the person may read can be chosen; left alone, the field keeps the stored id. A source the generated API does not describe has no word in the digest and is asked. A request that fails for any other reason says so under the field in the picker's words for it ("This picker could not be loaded: …"), and nothing is thrown at the page. The same switch decides how a list's referenced-resource columns resolve their display values (an unbounded source read whole once and mapped; a bounded one asked for one `filter=id:in:(…)` page per grid page over the page's keys, served by the key's index) and how an array view lists its children (whole, or one server page with Previous and Next). Nothing in the library reads every row of a bounded resource: the maximum was declared to keep the whole set out of memory, and declaring it is the author's deliberate switch.

A list page can list a view and write to the view's table. The view declares its backing table, a create goes into the table, and the new row shows up in the view on the next list because the view's SQL reads that table: the generated metadata carries the table as `rowsOf`, and the `listViewConfig` names the view alone as `primaryResource`. Rows, columns, `filter`, `sorts`, `parentRelation`, and the List-grant narrowing come from the view, since that is the resource the server enforces; create, edit, and delete go to `rowsOf`, the `elements` form is built for it, and the row page reads it. The list always requests the view's primary-key columns, read from the metadata, so an operation lifts the key the row carries — an association view keyed like its table is deleted from the list. `rowRoute` names a field of the view: a row opens that field's `enumeratedResource` on its page (the one `resourceRoutes` registered for it, else its metadata route) by the row's value in the field, and the View column is drawn when the caller may Read the target. Without `rowRoute` a row opens the write resource by its single key, and a compound-key resource with no `rowRoute` draws no View column. A `rowRoute` naming a field with no resource in the metadata throws when the page is built, naming the field. The list configuration cannot name another resource: `overrideResource` and `viewResource` are gone, and a config setting either fails to type-check.

A list pages on the server, and only there. The list component holds one page of rows — the resource's declared default page size (`@page`), or the config's `pageSize` — and the grid renders it and emits intents: a header click is the next sort, a filter menu is the next column filter, and the pager's First, Previous, and Next follow the cursors the server issued, with the total the first page answered kept while turning. Sorts and filters travel as request parameters, so the server orders and narrows every row it holds, not the ones the browser happens to have; a change to either asks for a first page again. The grid offers the server's filter grammar and no other: the ten comparisons on a column the generated metadata marks `filterable` — an indexed column always, an `allow_filter` column only once an indexed filter is in the request, since the server accepts it only beside one — and "Starts with", "Ends with" and "Contains" on a column whose field's metadata lists the match (`FieldMeta.search`), which stand alone, so a filter the grid offers is one the server answers. A column's `filterable: false` in the config removes a control the metadata would draw. The menu edits a draft: Apply, Enter in its value, or closing the menu commits it, and a new operator alone sends nothing, so an operator and a value changed together are one request, and a null test chosen alone commits as the menu closes. A `civildate` column's menu takes its value from a date control, and a `date` column's (a timestamp) from a date control and an optional time of day, sent as `2026-12-15` or `2026-12-15T09:30`, which the server reads as the whole of that day or minute in the person's zone, sent on every request as the `Time-Zone` header by the resource client (the `@impulseframework/resource` README, Time zone header), so a list filters by the day the grid shows; "One of" and "None of" on those columns take typed dates, comma-separated, in the forms the server reads (`2026-12-15`, `12/15/2026`, `2026-12`, `2026`). A list over a resource with search fields (`ResourceMeta.searchFields`) draws the search box above the table unless the config's `showSearchBox` is false: its placeholder names the fields it searches for this caller (the resource's, less those the digest denies for List; a child's field, listed under the child's name, judged by the child field's grant on the child resource through `ResourceMeta.searchBranches`), the hint under it names the fields the role leaves out, typing commits after a pause and is sent as the filter's `*:search:"…"` conjunct, the title's count becomes the matches, a companion column lights up while the box is active, and clearing the box restores the unnarrowed page. Which fields the box searches is the resource's and the caller's grants, never the page's. The request travels in the body, as the server requires, exactly when the page's filter, a column filter or the box reaches a field the metadata marks `pii` (`FieldMeta.pii`, a branch's `pii`); `disableCacheForFilterPii` forces the body for a filter the metadata cannot read. Selection is held as rows and spans pages. `ListViewConfig.limit` is gone: a list never gathers every row for the browser to slice, and the row-count title reads the server's total.

A list page can list a resource across tenants. A tenant-scoped resource declared for it (`@listAcrossTenants`; its generated descriptor says `acrossTenants`) is listed by a page outside every tenant with `acrossTenants: true` on its `listViewConfig`: one request, without the tenant's segment, for the resource's rows in every tenant the person reaches in which they may list it, with the page's one filter, sort, page and count across them. The table gains a first column naming each row's tenant by its name, headed with the application's word for a tenant (the descriptor's `domainRoute.word`, "Tenant" where it carries none), and a row's View link opens the row itself inside its own tenant: the record's own row route there (the page `resourceRoutes` mounted with a row route for the resource the row opens, with the row's tenant in the place of the tenant parameter), which asks Read on the record and nothing else. A link is drawn only where it will open: on the rows of a tenant whose digest lets the person read the record, which the page loads once for each tenant on its page, and on no row when the application mounts the record no row route or mounts it outside the tenant's segment. The server decides, tenant by tenant, which of the page's columns a row shows, since a person's roles differ by tenant, so the page asks for no column list and consults no digest for its columns, and a cell the person may not read in a row's tenant stays empty. The page is bound to no tenant: selecting another in the place switcher asks for nothing again. It reads: it draws no create button, no delete and no method, whatever its config names, because every write stays inside one tenant, on the row's page; `enableRowExpansion` is refused when the page is built, naming the resource, as is `acrossTenants` over a resource the descriptor does not say is listed across tenants. It is plain unless the page asks, the opposite of a tenant's list: a page that leaves `live` unset takes no subscription whatever the resource's metadata says, and `live: true` on the config holds one subscription over the tenants the list reads, so a change to the resource's list in any of them requests the page again. A column that reads a referenced tenant-scoped resource (`additionalIds` naming one) shows nothing, since the page has no one tenant to read it in; a click on the tenant column's header sorts nothing, the tenant not being a field of the resource, and a page that wants its rows grouped sorts by the resource's own tenant field where it lists one. Narrowing the list to some of the tenants is the client's (`client.acrossTenants.<resources>.page({ domains })`, the `@impulseframework/resource` README); the page lists every tenant the person reaches.

A list says what it has to say in the page's own words, and a row can carry a button for what is no method. `emptyMessage` on the `listViewConfig` is what the empty table says where the list holds no row at all ("This application declares no feature flags.") in place of "No records found"; a list narrowed by a filter or the search box that leaves no row says the plain text, and a refusal, a failed request and a list with no column the person may see keep their own words. `rowButtons` names the buttons every row carries ahead of its menu of methods (`{ label, icon, run: (row, injector) => … }`), for what a person only reads and the server serves as no method, opened in a pop-up (a flag's history): an icon button named by its label, handed the row as the server answered it and the list's injector. What the caller may do is never decided there, so a button that reads asks the server and shows its answer, a refusal included. The grid draws its headers as buttons that ask for the next sort; a table whose rows come in the one order they have (a history, newest first) passes `[sortable]="false"`, and its headers are words with nothing to press. One column opts out the same way with `sortable: false` on the column, for a value the server works out for each row and orders by something else (when a flag was last flipped): its header is words, and the other headers stay buttons.

A list page over a key-less resource draws the whole list as one page. A `@computed` or `@virtual` struct with no `@primarykey` is served whole: its generated descriptor lists `keys: []` and the list operation alone, the client sends no `limit` and no `cursor` for it, and the metadata carries no key field. The page shows every row the server returned, the pager reads one page with Previous and Next disabled and the count, and each row is identified by its position, since a row has no key: no View column, no create, no delete, and no row route (`resourceRoutes` builds none and registers the page as nowhere a row opens). Keeping such a list a manageable size is the resource author's responsibility; `enableVirtualScroll` on the config renders the rows in view and scrolls the rest. A config asking the page for what it cannot have fails when the page is built, naming the resource and the reason, rather than being ignored: a `pageSize` (the list is served whole, no page size), `enableRowExpansion` (no key to open a row by), and a `rowRoute` naming a field with no target (refused on any resource). The grid itself identifies a row by its `rowKey` input, a function from a row to its identity that the list page fills with the row's key fields joined, so selection and expansion are right on a compound key and on a key not named `id`; a grid given no `rowKey` identifies rows by position.

Every field renders by its generated display type, in a grid cell, a view, and a form, and the three shapes with no editor yet are presented and never typed:

| Display type                                    | Grid cell                                                                                                        | View                                                                | Create and edit form                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| ----------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `string`, `uuid`                                | the text                                                                                                         | a read-only text input                                              | a text input                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `number`                                        | the number                                                                                                       | a read-only number                                                  | a number input                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `boolean`, `nullboolean`                        | a check for a yes and a dash for a no, each named for a screen reader                                            | the checkbox or the tri-state control, read-only                    | the checkbox or the tri-state control                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `date`, `civildate`                             | M/d/yyyy                                                                                                         | a read-only date                                                    | a date picker                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `enumerated`                                    | the key                                                                                                          | the picked row's display, read-only                                 | the picker                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| `bytes`                                         | the size (`32 B`, `12.4 KB`), never the base64                                                                   | the size and a download of the decoded bytes, named after the field | the same, read-only; no editor: binary content is written through `@upload` and `@file`                                                                                                                                                                                                                                                                                                                                                                                                         |
| any array (`number[]`, `string[]`, `date[]`, …) | the elements, each by its element type, joined with `, `                                                         | the elements as chips                                               | the same, read-only; no editor yet                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| `object`                                        | the JSON on one line, cut with an ellipsis                                                                       | the JSON pretty-printed                                             | the same, read-only; no editor yet                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| any type flagged `writeOnly`                    | never a column: a `listColumns` entry naming one fails when the page is built, naming the resource and the field | absent, since the server never returns it                           | a blank input in create and in edit, where the caller may set the field (a row's Update envelope names a write-only field the caller's grant covers); the create or the patch carries the field only when a value is typed, and after a save the page holds nothing of it                                                                                                                                                                                                                       |
| a `string` or `uuid` flagged `secret`           | never a column, as any `writeOnly` field                                                                         | absent, since the server never returns it                           | a masked input whose button shows what is being typed and hides it again, with the browser asked not to fill or correct it. The box is masked again whenever the form enters or leaves edit. The button shows the input's own text: the row never carries the value, so the page has none to reveal. A method's request field flagged `secret` is drawn the same way in the method's dialog. The generator takes `secret` on text and on bytes alone, so no other control is ever asked to mask |

A column can render its field as an icon with a tooltip instead of text. An `iconConfig` on the column declares the map: `icons` keys a Material icon by the value's text (`true` and `false` for a boolean, the key for an enumerated field), `tooltips` gives the words per value, and `empty` the icon and words for a null. A sentence may name the row's fields in braces, `'Grandfathered: {name} has {activeLearners} active learners. The partner limit is {partnerLimit}.'`, and the list requests those fields beside its columns and fills them by their display types; a value with no sentence uses the cell's formatted text as its words, and a value with no icon keeps the text, so declaring icons for some values and not others degrades to text. The list resolves the cell while it builds the display row, as it applies every other column's formatter, and the grid renders the icon as an image with the words as its accessible name and tooltip, focusable so the tooltip opens from the keyboard; nothing runs at render time. The column stays a field column: its header, sort and filter work on the field. A column is a field column, a button column or an icon column, and a config naming both a button and an icon does not compile. An icon that depends on more than one field is a computed field first; the map then renders it.

A cell with no configured `formatType` is written by its field's display type, and a null is the column's `emptyDataValue`. A yes-or-no value is not written but drawn, a check or a dash, in any column that configures no `formatType`; a column that configures one is written by it. A create or edit form never offers a free-text control over bytes, an array, or an object, so no page can send the server a value it refuses: their controls stay in view mode whatever the form's mode, start null in a create form, and a null control leaves the add operation, so a `NOT NULL` bytes, array, or object column cannot be created from the generated form until its editor lands. An empty array shows the empty placeholder, as a null does. A column that declares a `valueGetter` has its cell read off the row by it (the words for a stored code, a value put together from two fields), then written like any other; the column's `id` still names a field of the resource, which the request asks for and the grant on which decides whether the column is listed.

Every cell is one line, and a text wider than its column ends in an ellipsis. The whole text is then offered in a tooltip while the pointer is on the cell, the way an icon cell shows its words, and a text that fits offers nothing more. A column header cut short does the same, for the pointer and for the keyboard, whose focus reaches the header's sort button. The grid measures the text against its column when the pointer or the focus arrives, so the answer follows a column being resized and the window changing width, and one tooltip serves the whole table, so a long list pays nothing for each cell. A text cell is not a tab stop: the keyboard does not reach a cut-short value on the list, and a screen reader reads the whole value as it reads any cell, since the ellipsis is drawn over text that is all there. A button a row draws (the View arrow, Delete, a button column's own) is an icon, with its label as its tooltip and as its accessible name, so a screen reader announces the button by its label.

A column keeps a readable width and is no wider than what it holds needs, and a list whose columns do not fit scrolls sideways inside its own frame. The narrowest a column of text goes is what its heading, with the sort and filter marks beside it, and the longest value the list is showing need, with the cell's padding, and never more than 160 pixels (`MIN_TEXT_COLUMN_WIDTH`: 135 pixels of text at the list's 14 pixel type, about nineteen characters, which is a person's first and last name). A column whose values need more than that stays at 160 and cuts them short. The list reads these widths off what it has drawn, a text's own line or an icon's box, in one pass once the rows, the columns or a heading's marks have changed, so a check, an icon and a date are measured as a text is and nothing is worked out from the type. A list that is loading is read once it has loaded, since what it draws under the loading cover (a related record's key, until the record that names it is read) is not what it shows. What a column needs grows when a longer value arrives and does not come down while the list stays on the screen with the same columns, so a column drawn at what it needs does not narrow as a person pages, sorts or filters; it starts again when the columns change or the list is opened again, and when a font arrives, since every text is then drawn another width. A column that declares a `width` keeps that width, a column a person dragged keeps the width they gave it, and the select, expand and button columns keep theirs. While the frame is wide enough for every column at 160, the table fills the frame and the columns share its room as they always have. In a frame narrower than that and still wide enough for every column at its narrowest, the list does not scroll either: a column that needs more than an equal share of the frame keeps what it needs, and the others share the rest, so a column that grows there takes its room from those that share, never below what they need. Where the frame is narrower than the columns at their narrowest, the table stays as wide as they need and the frame scrolls left and right, as it scrolls up and down; the page around the list does not scroll sideways. The first column then stays at the left edge, with the select and expand columns where the list has them, so a person always sees which row they are on while the other columns scroll under it; a row's button columns scroll with the rest, and the first column is let go only where holding it would leave less than 48 pixels beside it. Rows stay one line, the header stays at the top of the frame, a control the keyboard's focus lands on is brought into view beside the held column, and the empty list's message and an expanded row's detail stay as wide as the frame and in view. A held cell is drawn on the color the list sits on, which the grid reads off the page around it, under the same tints as the rest of its row, so nothing scrolls through it in a light or a dark look and the list needs no background of its own. None of it is configured: a column's `width` is the one thing a page sets.

The pager under a list says which rows the page spans and offers First, Previous and Next. They share one line while the list's frame holds them; in a frame too narrow for both, a phone's, the buttons go under the words, at the far end, so none of them is cut off by the list's edge.

A list offers the methods that run against its rows, with nothing configured. A generated method that declares a target row (`@target` on the Go struct) runs against one row of a resource, and the generated method metadata names that resource and, for each request field that carries part of the row's key, the row's field it carries (`target: { resource, key }`). A list of the resource finds those methods in the metadata, asks the server for each row's Execute answer (`capabilities=Execute`, which costs no query where the grant carries no condition), and draws one Actions menu at the end of each row holding the methods the answer lists for that row: a row offers what the caller may run on it and nothing else, and a row that offers nothing draws no button. An entry runs the method through `MethodDialogComponent`, drawn from the metadata alone: the request fields that carry the row's key are filled from the row, every other field is an input by its display type (a picker where the field declares one, a picker of several for `enumerated[]`), required where the method requires it, and a method that takes nothing more than the row is a question with a button. A method that takes files (`@upload`) has a file input beside its fields: nothing is sent until a file is chosen, the request and the files travel together as the method's handle sends them (`upload(body, files)`), the input says the method's declared maximum, and how many files the method takes is the method's to refuse, in the dialog like any refusal. Where the caller's grant on the method carries a condition that may read the request, the dialog rehearses the call as the person fills it. The digest says the grant is `conditional` and cannot say for which values; the generated metadata names the request fields a condition may read (`attribute`, from `@attribute` on the Go field) and says which methods a dry run previews (`dryRun: true`, a method that runs in a transaction). Once those inputs and the required ones are filled, and again after each change once the inputs have rested, the dialog sends a dry run, which the server runs whole and rolls back; a dry run refused as not permitted (403) turns the inputs a condition may read red (the one changed last where it is one of several), says so in the dialog's own words (the server's name the method and the row and, by design, not the condition), and holds the button until a marked input is changed, and any other answer of a dry run leaves the form for the call itself to judge. The values a picker offers are not each tried: the picked one is. A method no condition can judge by its request (its grant's condition reads the row, which the row's own Execute answer already judged), one that runs outside a transaction, and one that takes files are not rehearsed. A method that warns before it runs (`warning: { typed }` in the generated metadata, from `@warning(typed: word)` on the Go struct) is drawn as a warning, with nothing configured: the dialog asks the server what the call will do (`handle.warning(body)`, the request as filled so far under the `X-Warning` header, which the server checks as it checks the call and saves nothing by), as it opens and again after each change once the inputs have rested, and shows the server's words in the library's warning colors (`--impulse-alert-warn-border`, `--impulse-alert-warn-bg`) under a warning icon, with the names the warning carries (`names`: the people who lose access, the rows that are deleted) drawn as a list of their own under the sentence; the confirm button carries the words the server gave it, which say what it does, and is held until the server has answered and the person has typed the word, in any case. While an input the method requires is empty the server is not asked, and a refusal of the question is shown as a refusal of the call is and holds the button too, since nobody confirms a warning they were not shown. The dialog calls the method itself in the selected tenant, shows a refusal in the server's words and stays open, and once the method has run the page says so and reads its rows again. A method is shown under its name in words (`DisableCrewSectorUser` is "Disable crew sector user"); `rowMethods` on the `listViewConfig` gives the methods it names the page's own words, an icon, a question, a row predicate (`when`, for a method that only makes sense in one state of the row, such as Enable on a disabled account) or hides one, and leaves the rest as they are; `rowMethods: false` offers none and asks the server nothing. A page may name one method more than once, each time with its own words, row predicate and `fill`, and a row offers each presentation that holds for it: one method that turns a thing on and turns it off is "Turn on" on a row that is off and "Turn off" on a row that is on. A method that names no row (a create, an invitation) is a button above the list where the page names it in `listMethods` and the digest grants the caller Execute on it, and opens the same dialog. A method that names no target row can still be run for a row: a presentation in `rowMethods` with a `fill` (`fill: (row) => ({ userId: row['id'] })`) puts the method in every row's menu while the digest grants the caller Execute on it, fills those request fields from the row, and asks for the rest; in `listMethods` the `fill` reads the row the list sits under, so a button above a child list runs for its parent. Once a method has run the page says so, in the presentation's `successMessage` or, for a method whose answer says how it went, in what the presentation's `answered` reads from that answer (`answered: (answer) => ({ message, failed })`): a method that mails a person answers whether the message was sent, and a page draws one that was not as a failure, with the server's reason. A method that does name a row can be offered above a child list too, where it belongs with what the list shows and is no row of it (a person's password, above the identities the person signs in with): named in the child list's `listMethods`, it runs against the row the list sits under, whose key fills the request, the page's question is asked of that row, and the button is drawn while the server's Execute answer for that row lists the method (and the presentation's `when` does not say no), so the page offers it only where the caller may run it on that row. For a method above a list, `when` and a `confirm` function are also told the rows the list shows (their second argument), so a method that only makes sense while the list holds something is offered only then, and its question can speak of what is listed. A field the `fill` leaves `undefined` is asked; a field it answers with `null` is sent as nothing and is not asked, which is how a role that is held in no one tenant is taken away without the dialog asking for a tenant. The server gives no answer by row for such a method, so the call itself is what judges it. A presentation's `labels` gives a request field's input the page's own words, by the field's name, where the name in words is not what the page wants (`labels: { domain: 'Sector' }`). A presentation's `throughDay` names the request fields picked as a day that mean through the end of that day, the last day of a period (`throughDay: ['endsAt']`): the dialog sends the first instant of the day after the one picked, in the viewer's time zone, and rehearses the same request, so what is given until 1 December still counts on 1 December; a day picked for any other field is sent as its first instant. A presentation's `narrows` narrows a picker by what the dialog holds, by the field's name: a function of the request as filled so far (the inputs as they stand, over what the dialog was opened with: the row's target and what the page filled) that answers the filter the picker's list is read with (`narrows: { role: (values) => values['domain'] ? 'domain:eq:"…"' : 'everyDomain:eq:false' }`), so a picker of the roles held where the dialog says lists the roles of the place chosen, and a choice made before the filter changed is cleared, since it may not exist in the new place. Every field a dialog asks for must take a value: `dialogGaps(methods)` (from `resource`) names each required field of the given methods that a person cannot fill, by method and field and with the reason (a field drawn as a value to read, such as a list of plain text, or a picker that names nothing to pick from), and an application holds its generated methods to it in a test (`dialogGaps(Object.values(Methods).map((m) => [m, methodMeta(m)]))` answering none), so a method nobody can run through its page fails there and not in a person's hands. The row page offers its row's methods the same way: where a method runs against a row of the page's resource, the read of the row asks for the Execute answer beside Update and Delete, and the page draws one Actions menu holding the methods the answer lists, presented by the same `rowMethods` (a page with a list shares it with its row page; `rowMethods: false` asks nothing and draws none). Once a method has run, from the row page or from a row of one of its child lists, the page reads the row again and every child list with it, since a method may change anything under the row (a role taken away in a child list is one fewer role the person holds); and when that read answers that the row is no longer there, the page goes back to where it was opened from, as it does after its own Delete. A hand-written component runs a method the same way: `ResourceStore.methodHandle(method)` is the method's handle bound to the selected tenant, and `openMethodDialog(dialog, method, row)` opens the same dialog.

The pages over an Auth's people are built by the library. Where the server's management of an Auth is generated (`GenerateIdentity`), the generated API description names its lists and methods by what each is (the `identity` block), and `identityRoutes` from `@impulseframework/resource-angular/identity-pages` turns that into routes of the library's own list and row pages:

```ts
import { identityRoutes } from '@impulseframework/resource-angular/identity-pages';

// The whole application's side: every person of the Auth.
...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Crew', scope: 'global' }),
// A tenant's side: the people who hold a role in the open tenant.
...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Crew', scope: 'domain' }),
```

Each side is a people page (the list, a menu on each row holding the methods the server lists for that row, a button above it for the invitation the digest grants, and a row page with the person's fields read-only, the same methods, and under it the roles held, with the methods that give one and take one away, the ways the person signs in (for an Auth with a password method: the password, where they hold one, and each linked identity that can sign them in, as the server judges it, by the words the login page's button carries) with Remove password above the list, drawn only where the list shows a password and an identity that counts, since the server refuses to remove a password that is the person's only way in; it asks first and says what is left by the words of the rows ("They will sign in through Fleet Google only."), and it goes with the password once the list is read again; where the role file lists a tenant's administrator the password only of a person no other tenant holds, as Lodestar's and the skeletons' do, the list of a shared person shows their identities alone and no removal is drawn, and the sign-in identities, with the method that unlinks one) and an invitations page; the whole application's side has the person's sessions under the row page too, and a page each for the sessions and the failed sign-ins. Nothing is drawn for what the description does not name: an Auth with no password method has no password entries, one whose roles a directory writes has no role methods, and a list the schema has no table for has no page. What reads differently from one application to the next comes from the description: the Auth's label and the tenant record's name word the titles, the columns ("Also in another sector"), the questions and the tenant input of the whole application's methods. Where the tenant record has a directory-only switch, a tenant's people page carries one more column, "Cannot enter": the server's sentence on the row of a person the switch keeps out while it is on ("Has not signed in through acme.example, so cannot enter while this sector is directory only"), empty for everyone else; the list is made from who holds a role in the tenant, so nobody leaves it. A tenant is shown by its name, never by its key, an id nobody reads: the tenant input is a picker over the tenant record that lists names (a picker with no display of its own shows the field the record's metadata says its rows are called by, `nameField`), and the roles a person holds and the mailed links name the tenant each is in, read from the tenant record by the keys on the page (the description's `tenantResource`), and a mailed link that names no tenant says where its roles are held instead, "Every sector" or "The whole application", where the caller may read the link's `everyTenant` field (a link that gives no role says nothing); the first administrator's own invitation, which nobody sent and which gives the Super Administrator role, says "The whole application and every sector", where the caller may also read the link's `invitedByUserId` field; a link whose message could not be mailed says "Not mailed" under Sent and "Ended" under Expires, with nothing under Used, where the caller may read its `mailFailedAt` field; where the caller may not list the record, the cell shows the name their own list of tenants holds, for a tenant they hold a role in, and otherwise says so in words ("A sector you cannot see"), never the key; only where the record marks no name field is the key all a cell can show. Every dialog that gives a role (Invite, Give a role, on a person's page and on a group's) picks it from the roles the caller could give with that dialog's own method: the list the description names (`rolesToGive`) answers for one method at a time, and the dialog names its method in the filter (`method:eq:InviteCrewUser`), so a dialog offers exactly the roles its own request is allowed, and a role only another method may give is not among them. The whole application's dialogs ask where the role is held first, then pick the role from the roles that exist there (the generated `<Auth>GivableRoles`, filtered by the place the dialog holds beside the method: `domain:eq:<id>` for one tenant, `everyDomain:eq:true` or `false` otherwise, as "In every sector" says; the invitation asks it as the others do, and its request names the same place, which the server holds it to (an invitation that names neither a tenant nor every tenant gives roles of the whole application alone), so a role is given in every tenant only where the person ticked that), and a role chosen before the place changed is cleared; a tenant's own dialogs pick from the tenant's roles (`<Auth><Tenant>Roles`), asked for the method the same way. An application without tenants has one place, and its pages ask for none and name none: the description names no tenant record and the generated methods that say where in an application with tenants (the ones that invite, give a role, take one away or add a group) have no field for a tenant there, so their dialogs draw no tenant input and no "In every tenant", their requests name no place, and the lists carry no "Held in", "Kept in" or tenant column. Once a method has run, the notice says what happened in a sentence ("The account is disabled.", "The role was taken away."), never the method's label and "done"; an invitation's notice reads the method's answer: "The invitation is on its way.", or, drawn as a failure, that the invitation could not be mailed, was withdrawn, and the person is to be invited again. The pages are guarded as any `resourceRoutes` page is, by the List grant on each page's resource, and each route carries its page (Accounts, Mailed links, Sessions and Failed sign-ins for the whole application; People and Invitations for a tenant, each with its icon) for the application's menu file to place: `identityMenuPages` with the same arguments answers the same page objects, in menu order, so the menus leave out a page the caller may not open and `unplacedPages` sees each one placed. By default a page's route is the Auth's name and what the page lists (`crew-accounts`, `crew-mailed-links`, `crew-sessions`, `crew-failed-sign-ins`; under the API's tenant segment `sectors/:sectorID/crew-people` and `…/crew-invitations`); `routes` renames one. `identityPages` returns the same pages as configurations, for an application that routes them itself; the same request answers the same configurations. Both refuse, naming what is missing, an Auth the description does not carry, a side it does not carry, and a tenant's side on an API with no tenant segment. The one piece of knowledge the pages hold that the server does not give is that Enable belongs on a disabled account and Disable on an enabled one.

The whole application's side has a Sign-in page too, where the description names the list of the Auth's sign-in methods (`signInMethods`, which an Auth whose roles a directory writes does not have, nor an Auth of a tenanted application whose people create no tenant): one row for each method the Auth registers, with who can sign in with it, whether it is Open or Invite only, the role a new person gets (an application without tenants), and what a person with no invitation meets, all in the server's words. A row that is invite only offers Open sign-up and a row that is open offers Make invite only, each only while the digest grants the caller Execute on its method; a method whose directory gives the roles offers neither, and neither does one whose registration carries an account rule of its own in code (`ownRule`): each row says who decides, and the choice column reads Set by the directory or Set by the application. A method opened for another audience than its registration has now (a release widened or narrowed it, or put another organization in the place of one) is invite only: its row says that the people it admits have changed since it was opened and whom it admits now, and offers Open sign-up again. Open sign-up runs the generated method through the method dialog, which draws the warning the method declares: the server's sentence for that method (how wide it is, and what each new person gets), the word to type, a confirm button in the server's words ("Open to anyone with a Google account"), and, in an application without tenants, a picker of the role each new person gets that lists, with its description, every role the caller may give a person directly (the server's list; a role the caller may not give is refused by name). Make invite only asks a plain question. While a method is open the page keeps a notice above the list, in the warning colors, naming the method and what a new person gets. The page's route is the Auth's name and `sign-in` (`crew-sign-in`), and its page object (Sign-in) is placed by the menu file like the others. Two options carry the page and are any page's to use: `notice` on a `listViewConfig`, a function of the rows on the page that answers the sentence to keep above the list, drawn with a warning icon and nothing where it answers none; and `shows` on a method's presentation, which names the fields a picker of the dialog lists each option by (`shows: { defaultRole: ['name', 'description'] }`), where the picker's own display is not enough to choose by.

The whole application's side has one more page where the description names the list of the invitations that make a tenant (`invitedTenants`, which the Auth whose people make the application's tenants has): staff bring an organization in by inviting one person, and the tenant is made when that person first signs in. The page is named by the tenant record ("Invited sectors") and lists the invitations that still stand: the tenant as staff entered it, one column for each field of the record the list carries (the record's field under the record's name, `sectorName`, headed "Sector name"; an application's own fields follow with nothing written), "Directory only" and "Starting role" where the record has a directory-only switch, the invited address and name, when the invitation was sent and when it expires. Above the list, "Invite a sector" runs the generated method (`inviteTenant`) through the method dialog, which asks for the person's email and name and for the record's fields by their metadata; on each row, Withdraw runs `withdrawInvitedTenant` with the row's id (`invitationId`) after a question that names the address and the tenant that will not be set up. Each is offered only while the digest grants the caller Execute on its method. An invitation's details are not edited: it is withdrawn and another is sent, and the question says so. The page's route is the Auth's name, `invited` and the tenants (`crew-invited-sectors`; `routes: { invitedTenants: 'new-sectors' }` renames it), and its page object is placed by the menu file like the others. A list of a tenant record that refuses a direct create (`createDisabled` in its generated metadata) draws no Create button, so this page is where a tenant begins.

Where the Auth keeps its own groups of people, each side has a Groups page (`crew-groups`, and `sectors/:sectorID/crew-groups` in a tenant). The list says of each group how many people are in it and the roles it holds, a line for each role, so nobody opens every group to learn what it is for; outside every tenant, in an application that has tenants, it says where each group is kept too. "Add a group" is above the list, and a row offers Change the description, Add a person, Give a role and Delete. Give a role offers the roles that group may hold: outside every tenant the server lists them for the group the dialog names beside its method (`method:eq:GiveCrewGroupRole,group:eq:<id>`), by the rule the method itself keeps, so no group is offered the Super Administrator role, and a group kept in one tenant is offered that tenant's roles, in that tenant, with no question of where. Delete is offered on a group that holds nothing: a group that still has people or roles says what is left where Delete would be ("3 people and 2 roles: empty it first"), and the server refuses a request that never saw the page. A group's page is headed by the group's name and holds the people in the group and the roles it holds. Taking a role away from a group asks first and says how many people lose it ("2 people lose it. Anyone else in the group holds it some other way and keeps it."), by the server's count of the people in the group who hold the role in no other way (`losing` on the row). A person's page has one list of roles: the column "Comes from" says "Their own" for a role of the person's own and names the group for a role that comes through one, such a row opens the group's page, where the role is changed, and offers no removal, and a role of the person's own shows why it was given ("Why") beside when it ends. Where a directory decides the Auth's roles (the description's `rolesFrom` names it, by the words its sign-in button carries), the Auth keeps no groups and the Groups entry stays in the menus: its page says that the directory's groups do this job here, names the directory, and lists nothing; it is shown to whoever may list the Auth's people.

The whole application's side has a Break glass page (`crew-break-glass`) where the description names the list of the roles held for a set time with a reason (`takenRoles`, which an Auth whose roles a directory writes does not have). "Take a role" above the list runs the generated method (`takeRole`) through the method dialog: the tenant, in an application that has tenants; the role, picked from the roles the caller may take, each with its description; for how long, picked from the lengths the server offers (1 hour, 4 hours, 8 hours, 1 day); and why, one line, which is required. The request names no person, so the role goes to the person who asks. "Held right now" lists who holds which role, where, why, when it ends and how long is left, and a row offers End now, the ordinary removal of the role, while the digest grants the caller Execute on it. "Ended", under it on the same page, lists the ones whose time is over, with from when to when; the server keeps such a row until its person next signs in. The page object (Break glass) is placed by the menu file like the others, and the page is guarded by the List grant on the list, as every list page is.

Four options carry these pages and are any page's to use. `rowTitle` on a `listViewConfig` heads the row page by a function of its row (`rowTitle: (row) => String(row['name'])`), and by nothing until the row is read. `lines: true` on a column draws a value that is a list a line for each element. `otherwise` on a method's presentation gives the words a row shows in the method's place while its `when` says no (`otherwise: (row) => '2 roles: empty it first'`), drawn in the row's Actions menu as an entry that cannot be chosen; a row it answers nothing for shows nothing. `below` on the `listViewConfig` of a list page names further lists drawn under the first on the same page, each read on its own with its own filter, and read again once a method of the page has run. A column whose rows open another list's page (`rowRoute` over a field that names a row of that list) draws no link on a row whose field is empty.

A config-driven page can work in a tenant. Provide `RESOURCE_DOMAIN` with a signal of the selected tenant (the frame provides it as the tenant the URL is open in; see The frame): the store binds every request for a domain-scoped resource to it, and asks again when it changes; a global resource's request never carries it, and a page over one asks nothing when the tenant changes. A permission question about a domain-scoped target whose scope names no domain is asked in that tenant's digest. A domain-scoped resource's metadata route carries the tenant parameter in braces, so such a page sets `routeData.route` to the path it lives at. That route may carry the tenant parameter as the router names it (`sectors/:sectorID/missions`: the API's domain route segment, the descriptor's `domainRoute.param` after a colon, then the page), mirroring the API's URL, and the links the page builds from it follow the page's tenant: the View column, the table button, the after-create navigation, and the page another list's rows open on put the tenant in the parameter's place, read from the route's parameter when the page is under it and from `RESOURCE_DOMAIN` otherwise, so a row link is the row's real URL and opens in a new tab. A route naming no parameter is used as written. The resolver is `pageRouteResolver()` from `@impulseframework/resource-angular/resource-nav`, for an application's own links from a registered route; it refuses a route naming the parameter when no tenant is known, naming the route.

A list page or a record page can be live: it stays current without polling and without refetching while nothing changed, and a page left and opened again inside five minutes is served by the browser with no request. A page is live by default when the generated metadata says its resource's changes are published (`ResourceMeta.live`, true on every table-backed resource); `live: false` on its `listViewConfig` or `viewConfig` opts it out, `live: true` on a resource whose metadata says its changes are not published (a view or a computed resource) is announced once and asks plainly, and metadata from a generator that predates the field makes no page live by itself (the list page's row page follows the list's config). Provide the change feed once in the application's providers: `{ provide: CHANGE_FEED, useFactory: () => firestoreChangeFeed() }` from [`@impulseframework/resource-firestore`](../resource-firestore/README.md). `AuthService` starts the client's live session with the feed when the session first authenticates, fetching the feed's identity from the API, and stops it at logout, before the session is logged out, so every subscription of the user is deleted and the feed's identity revoked first. From then on the store's list page carries the client's live option (the tab subscribes to the resource in the selected tenant, and a change requests the page again by its own cursor, with the change's version) and the row page carries it for its row (a change reads the row again, the form and the children staying on screen while the read is in flight); a resync reads both again by a fresh seed, and a tenant change moves the list's subscription. The client's live session says when: a write the person made in this tab shows at once, when the write is answered, and the feed's report of the same write requests nothing; other people's changes wait two seconds from the first and become one request, at most one every five seconds while they keep coming; and a tab that is not shown requests nothing until it is shown again, then once. A component's own reload after its own write (`reloadPage`, `reloadViewData`) is the request the session already made, not a second one. An application sets the two times where it creates its client: `provideResourceClient((options) => createApi({ baseUrl: environment.apiUrl, ...options, live: { ...options.live, changeWait: 1_000, refetchInterval: 10_000 } }))`. The page's other readers (pickers, referenced-resource lookups, array views, create forms) never ask to be live. Without a provided feed, or on an outlet that serves no live subscriptions, a page configured live asks as a plain one: nothing is cached, nothing is subscribed. What the option opts into, what it costs, and what it never does are in the client's README under "Live pages".

A feature can ship dark and be turned on per environment, and the pages follow the switch. For a feature with a resource behind it there is nothing to add: while its flag is off the server leaves the resource out of the permission digest, so the menu item, the buttons, and the row page are hidden by the digest alone, and `resourceRoutes` adds a match guard from the metadata's `feature`, so a dark URL falls to the application's wildcard route as any unknown URL does. For a feature with no resource behind it the library offers three gates, each taking a member of the generated `Feature` union so a misspelled flag fails to compile: `*impulseFeature="Feature.Debriefs"` on an element, beside `*impulseHasPermission`, renders it while the flag is on; `featureMatch(Feature.Debriefs)` in a route's `canMatch` matches the route only while the flag is on; and `feature` on a page or an action of the menu file (below, under the frame) beside `permission` leaves the entry out of the menus while it is off. A group, a submenu or a menu whose every entry is left out, by a flag or by the caller's permissions, is left out too, so nobody is shown a heading that opens on nothing. `AuthService` loads the enabled set once when the session first authenticates, beside the digest and the domains, and forgets it at logout; `featureEnabled(name)` answers from it, signal-backed, and `ensureFeature(name)` loads it first for a guard that may run before the session's first check. On an API that serves no feature flags (a descriptor without the `features` route, from a generator that predates them) `AuthService` skips the load and every gate answers off: `*impulseFeature` renders nothing, a `featureMatch` route falls to the wildcard, the first ask of each flag is announced once through the client's `warn` option (the console by default), naming the regeneration that would serve it. Nothing polls and nothing is pushed: a flip by someone else is seen at the next sign-in, by design.

**The Feature flags page** is the library's (`@impulseframework/resource-angular/feature-flags`), and every application places it: `featureFlagsPage(resourceMeta)` in the menu file and `featureFlagsRoute(resourceMeta)` among the routes of the place outside every tenant (an untenanted application's own routes). It is the library's list over the feature flags resource and draws nothing of its own: each declared flag by its name in words (`cargo_manifest` is "Cargo manifest"), its description, whether it is on, and when and by whom it was last turned on or off ("Never" for a flag nobody has flipped; the two are read from the flag's history, and their headers offer no sort). It is shown under List on the flags. A row's Actions menu holds Turn on for a flag that is off and Turn off for one that is on, for a person the digest grants the generated `SetFeature` method; the person is asked first ("Turn Cargo manifest on for everyone in this environment?"), a refusal is shown in the server's words, and once the flip is written the client reloads the enabled set and the digest, so the person flipping sees their own pages follow at once. A person who may list the flags and not flip them sees the list without the menu. Each row carries a History button, which opens the flag's flips in a pop-up, newest first: when, what it was turned to, and by whom (`client.features.changes(name)`, the flag's `changes` route, which whoever may list the flags may read; a column the person's role shows nothing of, as the role shows nothing of the same fact in the list, is not drawn). The pop-up is its own entry point (`@impulseframework/resource-angular/feature-flag-history`, `openFeatureFlagHistory(dialog, { name, words })`), loaded when a person first asks for a history, so the page costs the first paint nothing. An application that declares no flag reads that it declares none.

An application installed as a progressive web app can tell the person when a new build is ready and reload only when they choose. A progressive web app is one the browser can install, with a service worker: a script the browser runs apart from the page, which keeps the application's files and downloads a new build when one is deployed. The library provides this through a secondary entry point, `@impulseframework/resource-angular/ui-app-update` (an import path of its own beside the main package), which exports `provideAppUpdate()`. The application adds `provideAppUpdate()` to its providers beside Angular's `provideServiceWorker(...)` and nothing more: no component references the service, and the notices it raises render in the alert area every application already has, the `impulse-alert` list over `NotificationService.notifications()`. `provideAppUpdate()` starts `AppUpdateService`, which listens to `SwUpdate`, Angular's service for the worker's events, from `@angular/service-worker`; that package is an optional peer dependency of this library: an application installs it when it uses this entry point. The service is inert when no service worker is provided or the worker is disabled, as it is in dev mode and in component specs: no timer starts and nothing is raised. When the worker reports that a new version is ready (`VERSION_READY`), the service raises one persistent notice, "A new version of this application is ready.", with a Reload action; it never reloads on its own, and the person chooses when. When the worker reports a state it cannot recover from (`SwUpdate.unrecoverable`), the service raises the same kind of notice, "This application needs to be reloaded to keep working.", with Reload. Checks for a new version run hourly while the browser tab is visible and once whenever the tab becomes visible again, and none run while the tab is hidden, so a hidden tab never wakes a server instance for nothing; the worker itself also checks after every page load. The hourly timer runs outside Angular's zone, the layer that tracks pending timers and requests and reports the application stable, or settled, once none is left, so it never keeps the application from settling. Every reload goes through one injectable class, `AppReloader`, whose `reload()` calls `location.reload()`, and a spec provides its own in its place to observe a reload. The service also exposes four members other parts of the library use: `ready`, a signal that is true once a new version is ready; `checkNow()`, which asks the worker to check and resolves true when a new version is ready; `reload()`; and `reloadWhenReady()`, which reloads onto the new version with no notice as soon as the worker has one ready, at once when one already is. To carry the Reload action, `CreateNotificationMessage` and `NotificationMessage` (in `@impulseframework/resource-angular/types`) gained two optional fields: `action`, a `NotificationAction` with a `label` and a `run` callback, which `AlertComponent` renders as a button beside its close; and `persistent`, which makes the alert skip its auto-dismiss timer so the notice stays until it is closed or dismissed by its id. A notice that sets neither renders and expires exactly as before. A related fix in `IdleService` (`@impulseframework/resource-angular/auth-idle-service`) moved its keep-alive, the periodic session check, so it now starts in `start()` and ends in `stop()`, as the idle ticker (the timer that watches for inactivity) already did, where before it started in the constructor and never stopped. A signed-out application therefore has no timer due and can settle, which is what lets the service worker register as soon as the application is stable. No request behavior changes: the keep-alive already sent nothing without a session.

The application reloads by itself in one case: when the server refuses this build's release, `AppUpdateService` picks up the server's build. While the application is still starting, meaning its router has not yet completed a navigation (`Router.navigated` is false, so no page is on screen yet), the service calls `reloadWhenReady()`: the worker is asked for the new build, and the document reloads onto it as soon as it is ready, with no notice. When there is no service worker, or no new build is ready within ten seconds (`RELOAD_WAIT_LIMIT`), it does an ordinary reload. Once a page is on screen, the service reloads nothing by itself and raises one persistent notice, "A new version of this application is required. Reload to continue." (`OUT_OF_DATE_MESSAGE`), with Reload. A tab reloads by itself at most once per server release: the guard is the server's release, which the service keeps in `sessionStorage` (the browser's storage for one tab, which survives a reload) under `impulse.app-update.reloaded-for` (`RELOAD_GUARD_KEY`), with every read and write wrapped in try/catch, and a refusal past the guard, one for a release the tab has already reloaded for, raises the notice instead of reloading. When the server runs an older release than this build, as after a rollback, the client has already retried the request, and a plain reload would not help, since the worker would serve this newer build again; so the service unregisters every service worker (`AppReloader.unregisterWorkers()`) and then reloads onto the server's build, whether or not a page is on screen. Past the guard it raises "The server runs an older version of this application. Reload to continue." (`SERVER_OLDER_MESSAGE`), whose Reload does the same.

The application tells the person when the server is down for maintenance and takes the notice down by itself when the server is back. The deploy pipeline starts a release that interrupts the database behind a maintenance revision of the application, which answers every request with status 503 and the marker header `X-Maintenance: 1`; the client throws such an answer as `MaintenanceError`, and the client's README says how it tells it from an ordinary 503. The client's error hook hands it to `MaintenanceService`, a service of the same entry point beside `AppUpdateService`, through the `MAINTENANCE_HANDLER` token, which `provideAppUpdate()` provides too. The first answer of an outage raises one persistent notice, "This application is down for maintenance. It comes back by itself when the work is done." (`MAINTENANCE_MESSAGE`), with no action, since there is nothing the person can do; it is the library's sentence, never the server's text or status; and the answers that follow while the notice is up change nothing, so an outage raises one notice, not one per failed request. While the notice is up the service checks back: the first check after the server's `Retry-After` (`MaintenanceError.retryAfter`), or fifteen seconds (`MAINTENANCE_CHECK_START`) when the server named none, and each check that finds the server still down doubles the wait up to two minutes (`MAINTENANCE_CHECK_CAP`); no check runs while the tab is hidden, one runs as soon as it is visible again, and the timer runs outside Angular's zone as the update service's does. The check is `MaintenanceProbe.stillDown()`: one HEAD request for the application's own entry document under its base href (`document.baseURI`), the request the maintenance page's own check-back makes. It needs no session and no database and carries no body either way: a maintenance server answers it 503 with the marker, since it is not a navigation, and the application's server answers it with the document's headers. It is sent past the service worker with the `ngsw-bypass` header (`WORKER_BYPASS_HEADER`), which Angular's worker honors, so the worker never answers it from its cache of the application's files, and past the browser's cache (`cache: 'no-store'`); a request that gets no answer at all counts as still down, since the server is between revisions. The first answer without the marker, whatever its status, is the recovery: the notice is dismissed, the checks end, and the service calls `AppUpdateService.checkNow()`, so a new build installed meanwhile raises the update notice, and the next request of the client is answered, or refused under the release rule above, which does the rest. `MaintenanceService.down` is a signal that is true from the first answer of an outage until the recovery, and a spec provides its own `MaintenanceProbe` to script an outage.

Every application provides its generated client: `provideResourceClient((options) => createApi({ baseUrl: environment.apiUrl, ...options }))` in the application's providers, with `API_URL`, `BASE_URL`, and `API_VERSION` beside it. `API_VERSION` is the release this build was made from, as the build stamps it (into an environment file, for example), and is empty or `dev` for a local build; `provideResourceClient` hands it to the client as `ClientOptions.apiVersion`, so every request of the client carries it in the `X-Api-Version` header. `provideResourceClient` also hands the client's live session the XSRF token Angular's extractor reads from the cookie `withXsrfConfiguration` names, so the page-leave unsubscribe, which rides a keepalive fetch rather than HttpClient, echoes the application's cookie and the cookie is named once. The library's store, pickers, guards, and `AuthService` all speak through that client, and an application that provides none fails at startup naming this call as the way in. The client is the one place a response becomes an error, and the adapter renders its judgment five ways. A 401 is the session gone: the client's error hook keeps the attempted URL in `AuthService.redirectUrl` and returns the browser to `FRONTEND_LOGIN_PATH`, where the login page reads the URL once the session is back (on the login page or the sign-out page already, nothing moves and nothing is kept). An `ApiError` nobody caught raises one global error notice in the server's words (the body's `message`, else `HTTP <status>`), through the `ErrorHandler` the same call provides, and a request that got no response at all raises one saying so; a refusal a page reports in place raises nothing, since handling the error is what silences the notice: the store's `pageError`, the row page's `viewError`, a component's own refusal signal, and a method's declared answer (`@answers`) all render where they happen and reach no notice. A 412 carrying the server's release is the server's refusal of this build, `ApiVersionError`: the client's error hook hands it to the update service through the `VERSION_REFUSAL_HANDLER` token, which `provideAppUpdate()` provides, and `ResourceErrorHandler` raises no notice for it, since the update service reloads or raises its own. A build that names a release without `provideAppUpdate()` fails at startup naming `provideAppUpdate()`. A 503 carrying the maintenance marker is the server down for maintenance, `MaintenanceError`: the hook hands it to the maintenance service through the `MAINTENANCE_HANDLER` token, which `provideAppUpdate()` provides too, and `ResourceErrorHandler` raises no notice for it, since the maintenance service raises its own; without `provideAppUpdate()` the answer goes to nobody and raises no notice, and a 503 without the marker is an ordinary error and raises the notice in the server's words. The library does not recognize the refusal, or the maintenance answer, of a hand-written `HttpClient` call: `apiVersionInterceptor`, below, adds the header to such a call, the application's own error path sees the 412 or the 503, and the next request of the client triggers the update service or the maintenance service. Every request the client sends begins and ends `UiCoreService` activity, so `isLoading` and the progress bar move while any request is open. The library ships one functional `HttpClient` interceptor, a function `HttpClient` runs over each request it sends: `apiVersionInterceptor` (exported from `@impulseframework/resource-angular/resource-client`), which adds the same header to the application's own same-origin `HttpClient` requests (those sent to the application's own scheme, host, and port), a login form's POST, for example. The application registers it as `provideHttpClient(withInterceptors([apiVersionInterceptor]), withXsrfConfiguration({ ... }))`. It adds nothing for a build that names no release or `dev`, leaves a request to another origin alone, and keeps a header already on the request. `HttpClient` still keeps the XSRF cookie echo (`withXsrfConfiguration`). A provider placed after `provideResourceClient` that re-provides `ErrorHandler` would silence the notice, so startup fails naming the way out when the handler in effect is not the library's: `importProvidersFrom(BrowserModule)`, which `BrowserAnimationsModule` carries, does this, and `provideAnimationsAsync()` is its replacement; an application's own handler extends `ResourceErrorHandler`. Hand-written `HttpClient` traffic outside the client is the application's to render.

An application that signs people out when they stop using it starts `IdleService` (`@impulseframework/resource-angular/auth-idle-service`) once, after sign-in, and stops it at sign-out; the skeletons do this in their root and login components. The service counts the seconds since the last activity (a mouse movement, a key, a click, a scroll, a touch) and, for the last `IDLE_WARNING_DURATION` seconds of the `IDLE_SESSION_DURATION` (sixty of three hundred by default), shows one persistent notice in the alert area, "You will be logged out in N seconds due to inactivity.", counted down in place; persistent so the alert's own thirty-second auto-dismiss never hides it before the sign-out. Any activity ends the warning on the next tick, the notice's close button among them, since a click is activity: the application knows the person is back and asks for nothing more. At the end of the session the service runs `IDLE_LOGOUT_ACTION`, stops, and sends the browser to the sign-out route (`FRONTEND_SIGN_OUT_PATH`), where the signed-out notice shows. Every `IDLE_KEEPALIVE_DURATION` seconds (thirty by default) it also asks the server for the session while one is signed in, so a session the server ended is noticed. `IDLE_TIMEOUT_REQUIRE_CONFIRMATION`, which once made the warning hold until the application's own stay-signed-in button called `stayLoggedIn()`, is retired and has no effect: a provider of it keeps compiling, `start()` logs once in the console that it does nothing, and `stayLoggedIn()` stays for an application that still offers a button.

A login page shows text it holds, never text from the URL. A refused OIDC login returns the browser to the login page as `<login page>?code=<code>`, and the code is all the session package sends; its "Login refusal codes" table is the finite list. `LOGIN_MESSAGES` holds one sentence for each of those codes (`DEFAULT_LOGIN_MESSAGES`), `UiCoreService.loginMessage(code)` answers the sentence or an empty string for a code the application does not know, and `publishLoginError(code)` raises it as a global error notification and says whether the code was known. The page reads the `code` query parameter and renders the lookup, so a crafted URL renders nothing. A refusal the server answers on a step of the sign-in itself (a password POST, a confirmation) carries its code in the body, whatever its status, and the page reads it from there: a 401 or 403 for the policy's refusals, and a 429 for the session library's attempt limiter, which refuses a name whose failed password attempts reached the limit with the code `too_many_attempts` and the wait before the next attempt, `retryAfter` in seconds (and the `Retry-After` header), and for its mail limit, which refuses a code or link to an address sent as many as it allows, none used, with `too_many_emails` and the same wait. A component that asks for mail keeps its button disabled for that wait with `MailHold` (from `@impulseframework/resource-angular/ui-core-service`). `loginMessage(code, retryAfter)` and `publishLoginError(code, retryAfter)` take that wait and follow the sentence with it, "Too many attempts. Try again in 14 minutes.": in minutes from a minute up, rounded up, and in seconds below it ("Try again in 45 seconds."); without a wait the sentence stands alone ("Too many attempts."). An application whose session data resolver refuses with a code of its own, or that wants a default reworded, adds `provideLoginMessages({ not_provisioned: 'Your account has not been set up yet.' })` to its providers: the library's sentences come first and the application's after, and a reworded `too_many_attempts` is followed by the wait the same way.

## Permission questions in reactive code

The client's permission answers (`api.can()`, a handle's `can()` and `grantedFields()`,
a method's `can()`), its feature flags and its session read plain stores, and
`provideResourceClient` makes every such read an Angular dependency (`trackStoreReads`).
A question asked inside `computed()`, `effect()` or a template is therefore asked again
when its store changes: a digest loading, a tenant's digest arriving after a page opened
by its address, the permissions cleared at sign-out. Nothing mirrors a store first;
`storeSignal(store)` remains for a signal to pass along.

A `resource()` loader is not reactive code: Angular runs it untracked, so a permission
answer read inside a loader is read once and never reloads the resource. Put the answer
in the resource's params, where it is tracked, and branch on it in the loader:

```ts
private canList = computed(() => this.api.domain(this.tenantID()).tenantDirectories.can(Permissions.List));

directories = resource({
  params: () => ({ tenant: this.tenantID(), listed: this.canList() }),
  loader: ({ params }) => (params.listed ? this.api.domain(params.tenant).tenantDirectories.list() : Promise.resolve([])),
  defaultValue: [],
});
```

`provideResourceTesting` tracks store reads the same way, so a spec that loads a digest
sees a computed answer follow it.

## The frame

The frame is what every signed-in page sits in: a bar across the top with the application's name, the place the page is in, the menus and the person's own menu, and on a phone a menu button that opens the same menus in a side panel. The application gives its name, its menu file and the name of the place outside every tenant; the library draws the rest (`@impulseframework/resource-angular/auth-frame`). There is no footer: the release is in the About pop-up.

Provide it after the client, and route every signed-in page as a child of `FrameComponent`:

```ts
// app.config.ts
provideFrame({ name: 'Lodestar', home: 'dashboard', outside: { label: 'Configuration', icon: 'settings' }, menus });

// app.routes.ts
{
  path: '',
  component: FrameComponent,
  canActivate: [LoginAuthenticationGuard],
  children: [
    { path: '', pathMatch: 'full', canActivate: [homeGuard], children: [] },
    refusedRoute(DomainRoutePath),
    { path: '', canActivate: [outsideGuard], children: [...outsidePages, ...frameRoutes()] },
    { path: DomainRoutePath, canActivate: [tenantGuard], children: [...tenantPages, ...frameRoutes()] },
    { path: '**', redirectTo: 'dashboard' },
  ],
}
```

The place outside every tenant comes before the tenant segment, and the one wildcard comes last, outside both. A list page over the tenant record lives at the record's own address, as the API has it (`tenants`, and a row at `tenants/acme`), which the segment would otherwise take for the tenant `acme`; every longer address under a tenant matches no page outside and passes on to the segment.

- `name` is drawn at the upper left as a link to the application's root; without it no name is drawn.
- `home` is the first page of every place, a path under the place. Signing in arrives at the root, where `homeGuard` sends the person to the first page of the place they land in.
- `outside` names the place outside every tenant, for the switcher and the menus. Without it the place is Configuration, with a gear.
- `menus` is the menu file: the menus across the top, in order. The function form, `() => Menu[]`, is called once in an injection context, so an action of the application's own may use a service it injects (`const dialog = inject(MatDialog)`, then `run: () => openNewTenantDialog(dialog)`).
- `help` and `person` are the application's own entries in the Help menu, above About, and in the person's menu, above Appearance.

`provideFrame` also provides `RESOURCE_DOMAIN` as the tenant the URL is open in, so every library page asks in it.

**Pages and the menu file.** A page holds its own label, icon, address, and the permission and feature flag it needs: `page({ label: 'Customers', icon: 'apartment', route: ['customers'], permission: { resource: Resources.Customers, permission: ListPermission } })`. The route carries it, `pageRoute(customers, { path: 'customers', component: CustomersComponent })`, and the menu file places it by reference: `{ label: 'Records', groups: [{ entries: [customers] }] }`. The address says where the page lives. A route with a part naming the tenant parameter (`tenants/:tenantID/users`) is a tenant's page, shown while a tenant is open and filled with it. A route that starts with `here` is a page every place has, opened where the person is (the library's account page is such a page). Any other route is a page of the place outside every tenant, shown only there. So choosing a page in a menu never moves the person to another place. Beside pages, an entry is an action (`{ label, icon, run }`, with `place: 'outside'` to keep it to the place outside every tenant) or a submenu (`{ label, icon, groups }`). The menus leave out every entry the person may not use, by its permission (a list means any one of them) and its feature flag, and then every group, submenu and menu left empty. The answer follows the session: a tenant opened, a digest loaded, a flag flipped, and the menus redraw. The library appends Help, last on the bar, ending with About and the application's name: an action that opens the About pop-up over the page the person is on.

A config-driven page names its label and icon on its config, `page: { label: 'Missions', icon: 'rocket_launch' }`. `resourceRoutes` makes the page at the page's route, needing List on the resource and behind the resource's feature flag unless the config says otherwise, and carries it on the route; `resourcePage(config, resourceMeta)` answers the same page object for the menu file. A config without a page makes a page no menu reaches, such as one only a row link opens. The pages over an Auth's people do the same through `identityMenuPages`. A page that lists its resource across tenants (`acrossTenants`) is mounted at `routeData.route`, which it must name, with no row route. No one tenant's digest speaks for it, and the application names no permission for it: the page needs, and its route is guarded by, the global digest's answer that the person may list the resource in at least one tenant they reach (`ListAcrossTenantsPermission`, which the server works out and no role grants). The menus show the page to those people and nobody else, a person who may list the resource in no tenant is turned away from its route, and holding the page is a reason to have the place outside every tenant. A `page.permission` on the config is what the menus ask instead; the route's guard stays.

A page's list route asks List and its row route asks Read, each of its own route: the list's guard does not stand over the row. A person who may read a row opens it from a link drawn anywhere, whatever they may list, which matters where the list is a view and the row its table's (`rowsOf`), or where the link was drawn by a list across tenants.

`unplacedPages(routes, menus)` answers the pages the routes carry that no menu places. Each application's test asks it for an empty list, so a page added to the routes and forgotten in the menu file fails the test, naming the page. A page placed in two menus passes.

**Icons.** Every icon the menu file and the pages name is typed `IconName` (from the types), the names of the Material Icons font the library ships, so a misspelled icon fails the build instead of drawing its letters. `bun run icons` writes the list again after a font update, and a test fails until it is run.

**Places.** A place is a tenant, or the place outside every tenant. The tenant the page is open in is the URL's: every tenant's page lives under the tenant segment the generated client names (`DomainRoutePath`, `tenants/:tenantID`). `tenantGuard` on that segment checks the tenant against the session's tenant list (asking the server once more when it is not there, since a deep link may arrive before the list) and opens it before any page loads; a tenant the session cannot reach is sent to the refusal page under the tenant's own URL (`refusedRoute`), which says 404 Not Found and nothing more, as the server's domain guard answers, and shows no tenant's id. The navigation that lands settles the rest, so no guard is needed for leaving. A person holds something outside every tenant when the application's menu file shows them something there: a page that lives there, or an action kept to it, that they may use. A grant there that no menu entry answers to (a record the application reads behind a tenant's page) is no place to work in, and an application whose menus hold nothing there has no such place. `outsideGuard` sends a person who holds nothing outside every tenant to the place they land in, instead of an empty page. The place switcher, beside the name, lists the place outside every tenant for a person who holds something there, then the tenants by name, under the application's word for a tenant, with a box to find one by name from eight places on. A tenant the person holds a seat in and cannot enter with this sign-in (the server's tenant list marks it `closed`, with the reason) is no place: the tenant guard refuses it like any tenant the session does not reach, and the switcher lists it under the places, apart, under "Closed to this sign-in", with a lock, its name and the server's sentence ("Acme admits only people who sign in through its own directory. You signed in with a password."), and nothing to open (`AuthService.closedDomains`, `PlaceService.closed`). Moving between tenants keeps the page; moving to or from the place outside every tenant opens that place's first page. A person lands where they last worked, remembered per application and per login; the first time, a person who holds something outside every tenant lands there and anyone else in their first tenant by name.

The word for a tenant, one and many, is the generated description's (`domainRoute.word`), the name of the application's tenant record, so the browser and the server use the same word. A description without one takes it from the segment (`tenants` is Tenant and Tenants).

**The person's menu** is headed by the person's name where they hold one, with the username under it, and by the username alone where they do not; the bar shows the same name beside their initials, in a circle that also heads the menu: the first character of the first word and of the last word of that name ("RG" for Rey Gale, "MS" for Mary Jo Smith), and one character where the name is one word, as a username is; on a phone the circle stands alone. It holds what is the person's rather than the application's: Your account, where the account page holds something for them; Your API keys, where the application issues tokens; the application's own entries; Appearance (Light or Dark, and the colors where there is more than one palette); and Sign out. **The library's pages** are every place's, so `frameRoutes()` goes in the children of both places: the account page (Your name where the server serves its change and the session is the person's own, the email address where the server serves its routes, Change your password where the person's auth has the password method, with Remove your password under it where the server serves the removal and the session is the person's own (the person gives the password as proof, is asked once with what that leaves them to sign in through, and reads the server's words where it refuses: a person no directory sign-in is linked to keeps the password; where the server says what the person signs in with, the methods list's `signInMeans`, the page offers neither the change nor the removal to a person who holds no password, offers the removal only to a person a directory sign-in is linked to, and names that directory by the words its sign-in button carries: "You will sign in through Fleet Google only."), Choose a password where the server says the person holds none, the server serves a first password (the methods list's `firstPassword`) and the session is the person's own (the person asks for a code, which the server emails to the address their account holds confirmed, and sets the password with the code; the server's words are shown where it refuses, among them that the account has no confirmed address yet), and Forget this browser where the browser is remembered for them; a page that holds none of them says there is nothing to change) and the API keys page (the tokens component under the heading the menu names). **About** is no page but a pop-up (`openAboutDialog(dialog)`, which the Help menu's last entry runs), so the address does not change: the application's name, the release the server runs (with the build the tab runs where the two differ), and, wherever the server names one, the environment (`client.serverEnvironment`, from the `X-Environment` header the server sets everywhere but production, so production shows no such line). Its Copy button puts the three on the clipboard as one line for a problem report ("Lodestar, release v0.3.1, environment tst"); where the browser offers no clipboard, or refuses, the line is shown selected, to copy by hand.

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
    <impulse-login-methods home="/" [labels]="{ google: 'the fleet directory' }" (signedIn)="go($event)" />
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
from (`AuthService.redirectUrl`, read once), else `home`, which is the root unless the page names
another. An application with tenants leaves it the root, where `homeGuard` sends the person to
the place they last worked in; a page of the place outside every tenant named there would land
everyone who holds something in that place on it, wherever they last worked. The page navigates
there and starts what it starts on a sign-in. A redirect method sends the browser to the method's path, naming the same
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
list with the sentence for `pending_expired`. The component drops the password once the server has
taken it and asks for a step, and a step that is canceled or has expired returns to an empty
sign-in form: neither the username nor the password typed comes back with the list. A refused
password is different, and leaves the form as typed for the person to correct. An arrival whose reads fail (no answer, or a gateway's
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
address sent five codes within fifteen minutes and using none is refused with `too_many_emails`
and the wait, the send button then standing in the form's place, disabled until the wait has
passed; a code that is used resets the count, so a person who signs in with each code sent never
meets the limit. Send me a link on the sign-up form is held the same way. With the box checked, the server remembers the
browser, and this person's next sign-ins from it skip the code until the server forgets it, or an
operator does from the person's account page. A step this page does not render, a reason it does
not know or a second-factor step without the mailed code, shows a note saying the page is older
than the server, asking the person to reload and try again, with the cancel.

The package declares the sign-in steps the component renders under `impulseframework.signInSteps` in its
package.json (`confirmation`, and `mfa:email-code` for the second factor by the mailed code), the
same list as the component's `SIGN_IN_STEPS`. The framework's release check holds the steps a
generated server asks the page for to that declaration, so a release never pairs a server with a
page that cannot render what the server asks; a new step is added to the component, the list and
the declaration together.

A session the sign-in establishes may carry `passwordChangeRequired`: an administrator required a
new password of the person, and
the server serves nothing but the password change, the session read and the logout until the person
chooses one, refusing everything else with a 403 and the code
`password_change_required`. The component renders the change form in the list's place, with the
sentence the application holds for that code over it, posts the old and the new password through the
client (`api.auth.changePassword`) to the change path the methods list names, checks the session
again and only then emits `signedIn` with the page recorded at the sign-in; a refusal is rendered in
place, and a cancel ends the session and shows the list again. `AuthService.checkUserSession` loads
no digest, domains or flags while the mark is set, `LoginAuthenticationGuard` returns such a session
to the login page as it returns an unauthenticated one, and the client's error hook treats the 403
as it treats a 401, so a marked session that reaches a page is back on the login page before the
page renders.

The server's mailed links all lead to the login page, each with its token in the URL's fragment
(`/login#invitation=<token>`), and the component follows them from the routes the methods list
names. A browser sends a fragment to no server,
so a link a person opens writes its token to no request log. On arrival the component reads the
link (`mailedLink`, from `@impulseframework/resource`), takes it out of the address before its
first request, by a router navigation that replaces the tab's history entry, and posts the token in
the body of the request that uses it. A link opened in a tab that already shows the login page
changes the fragment alone, which loads nothing, so the page loads itself again and the link opens
as it does in a new tab. From then on the address the tab shows, the entry Back
returns to and an address or bookmark a person takes from the page carry no token. The browser's
own history of visited pages is not the page's to clear and keeps the address the mail opened,
token and all, so a link opened and not used can be opened again from that browser until it is used
or expires. The fragment is the only place a link is read from: a parameter of a link's name in the
address's query is not a link, so the component sends its value nowhere and leaves the address as
it came. An invitation
(`#invitation=<token>`) is opened on arrival and stands over the list
as the invitation screen: "You're invited to <the tenant's name>" and the address, over every way that can
accept this invitation for that address and none that cannot. A directory registration that could
admit the address is a "Continue with <label> as <address>" button, which sends the address to the
provider as its login hint, so the provider takes that account and skips its chooser; the password
form shows when an account with a password holds the address ("Sign in with your password to
accept it."); and where no account holds the address and the server offers a password method, the
person may create an account with a password: the account form asks for a username (unless the
username is the address) and the password twice, and the code step follows it: the server mails a
code to the invited address, and entering it creates the account, signs the person in and accepts
the invitation. It opens by itself where it is the one way to accept, and stands behind a
"Create an account with a password" button under the directory buttons where a directory could
admit the address too, so a new person chooses how they sign in. The server names in `methods` only
the registrations whose sign-in it accepts for the invited address: for an account with no password
that already signs in through a directory, those it signs in through and no other, so no button
leads to a refusal. The link only opens the invitation, and nothing is accepted by it: a person
whose sign-in does not itself prove the invited address is asked for a sign-in code, which
the server mails to the invited address; nobody types an address for it. That is a person who
accepts through a directory that does not vouch for an address (Microsoft Entra ID, whether or not
its registration is set up to send one), one who signs in with the password of an account that has
not confirmed the invited address, and one who creates an account with a password. Where people
choose their username, an account may hold no address yet; while no account holds the invited
address the screen also offers "Sign in with an account you already have", which shows the password
form, and that sign-in is held for the same code. The
"Use a different account" link is not offered while an invitation stands: the invitation names the
account. A person the page finds already signed in may accept it as they are with one click, from a
button under the heading, when their account holds the invited address; the server takes the click
only where that address is confirmed, and otherwise answers with a sentence that says to sign in
again from the invitation's link, which the screen shows. Signed in as an account that holds no
address yet, they are told to sign in again, over the password form: the code mailed to the invited
address makes it theirs. Signed in as an account that holds another address, they are told whose
the invitation is and how to accept it, and the server refuses that acceptance too. The
browser keeps carrying the invitation when a redirect method refuses the
sign-in and returns to the login page with `?code=` and no token; the methods list then names it
(`invitation.standing`), and the component shows the screen again, with the refusal's sentence under
it. A directory sign-in as another account than the invited one is refused by the server
(`invitation_address`), and the screen says "This invitation is for <address>. Sign in with that
account.", the button now asking the provider for its chooser. A link's token is opened instead,
which also refreshes what the browser carries. An invitation that makes a tenant (`newTenant` in
the answer: staff bring an organization in by inviting one person, and the tenant is made at that
person's first sign-in) is headed "You're invited to set up <the tenant's name>", with the invited
address under it, before anyone signs in. Where that tenant is entered through its directory only
(`directoryOnly`), the screen offers the directory buttons alone: no password form, no account form
and no "Create an account with a password" button, whatever the server says of the address, and
it says "To accept it, sign in with your organization's account, not with a password or a personal
account." A sign-in the server refuses for such an invitation comes back with one of two codes,
each with its sentence in `DEFAULT_LOGIN_MESSAGES`, shown under the screen like any refusal:
`organization_account_required` (the sign-in came through no organization's directory) and
`organization_has_tenant` (the organization's directory is already another tenant's), whose
sentence says "Your organization is already set up in this application. Ask its administrator to
invite you." and nothing of that tenant. An acceptance by a person already signed in that the
server refuses shows the server's sentence, the invitation still standing. A sign-up
link (`#signUp=<token>`) opens the same
account form in the list's place, and the list of a server whose password method offers sign-up
carries a "Create an account" step, which asks for an address and tells the person the link is on
its way, whatever the address's state. Under the password form the component draws "Forgot your
password?" only while the list names where a reset link is asked for (`passwordReset.request`),
which an administrator turns on and off in the running application: it asks for the address and
says a link is on its way if the address is an account's with a password, whatever its state. An
address sent too many reset links is refused with the wait and Send me a link stays disabled until
it has passed; the server counts reset links apart from sign-in codes, and so does the page, which
holds the reset form alone. Where an administrator turned resetting off after the page was drawn,
the form says so and to ask an administrator for a link. A
reset link (`#reset=<token>`), the person's own or one an administrator sent, opens a form for the
new password in the list's place; once it is set the component says the person is signed out
everywhere and shows the list again with the username filled in, and nobody is signed in by it; a
password that changed while the person was choosing shows the server's sentence, the form staying. A
link to an address an administrator set (`#address=<token>`) shows a question first and posts
nothing on arrival; Confirm this email address posts it, with or without a session, and the
component names the address the account now holds. A confirmation link (`#verify=<token>`) is posted once a
session stands: on arrival when the page finds one, else after the sign-in it waits for. A redirect
method's round trip reloads the application, so the token waits for it in the tab's session
storage and the method returns to this page with no token in the address, which is named in a
request line; a browser that refuses the storage keeps nothing, and the person opens the link
again once signed in. The
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
its way whatever the address's state. A refusal (not an address; a mailbox sent too many links it
did not use, `too_many_emails` with the wait) is rendered in place, and the button stays disabled
until the wait has passed.

```html
<impulse-email-address />
```

## The release

`<impulse-release>` (`@impulseframework/resource-angular/ui-release`) is the release the application runs, for
the login page and the frame's About pop-up. Every answer of the server names the release it was
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

The pages that administer the people of an Auth are not a component an application composes:
they are routes the library builds from the generated description, the library's own list and
row pages over the lists and methods the server generates for the Auth (`identityRoutes` of
`@impulseframework/resource-angular/identity-pages`, under Core Concepts above). A console that
manages its staff and, by the generator program's declaration, its members asks for each Auth's
routes, the whole application's side at the root and a tenant's side where its tenant pages go:

```ts
...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Crew', scope: 'global' }),
...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Members', scope: 'global' }),
```

A page that asks one question of its own, with at most one field, can use the dialog the tokens
page uses: `askInDialog` of `@impulseframework/resource-angular/dialogs`.

A tenant's Sign-in page, which the tenanted skeletons carry, reads who the person is signed in
through from the session the `AuthService` last checked: `sessionInfo().directory` is the
organization's directory the session came through, its sign-in method and its connection
(`SessionDirectory`), and is absent for a session that came by password or with a personal
account. A tenant's directory is never typed in: the page offers a person who has one, and who may
set the tenant's directory, to make it the tenant's, with a create that names no directory, since
the server sets the one the session came through.

## Tokens

`<impulse-tokens>` (`@impulseframework/resource-angular/impulse-tokens`) is the signed-in person's tokens for the
application's machine outlets, for an account page. It configures itself from the `tokens` block
of the generated descriptor (see the tokens section of @impulseframework/resource): the routes, and the
machine outlets a token can be issued for. The page that composes it names the page; the component
carries no heading of its own. The frame composes it on its API keys page, under the heading "Your
API keys", which the person's menu opens in an application that issues tokens (see The frame). One sentence stands over the form: "A token lets a device or a
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
page, code and all, once signed in. In the frame it is a child of `FrameComponent` beside the
places, not inside one, so the link lands on it whichever place the person works in.

## Modules Overview

- **`impulse-resource`**: The core module of the library. It contains the components and services related to dynamic page generation and data management.
- **`identity-pages`**: The pages over an Auth's people (the accounts, the roles held, the invitations, the sessions), built from the generated descriptor's identity block.
- **`impulse-tokens`**: The signed-in person's token page and the device approval page, configured from the generated descriptor's tokens block.
- **`auth-frame`**: The frame every signed-in page sits in: the bar, the menus from the application's menu file, the place switcher, the person's menu, the account and API keys pages, and the About pop-up.
- **`auth`**: This module provides authentication and authorization, including the login methods component, the email address component, route guards, and permission directives.
- **`ui`**: This module contains UI components and services, such as alerts, notifications, the update and maintenance notices, and the theme picker with its service.
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
