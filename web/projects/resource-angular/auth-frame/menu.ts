import { Route } from '@angular/router';
import { PermissionScope } from '@impulseframework/resource';
import { IconName } from '@impulseframework/resource-angular/types';

/**
 * The route part that means "in the place the page is in": the open tenant's segment
 * under a tenant, nothing outside every tenant. A page every place has (Help, the person's
 * account) starts its route with it, and opening it keeps the person where they are.
 */
export const here = '@here';

/**
 * A page an application's menus can reach. The page holds its own label and icon, its
 * address, and what it needs; the menu file says only where it is placed and in what
 * order. The address says where the page lives: a route with a part naming the tenant
 * parameter (the generated DomainRoutePath, `tenants/:tenantID`) is a tenant's page, shown
 * while a tenant is open and filled with it; a route that starts with `here` is a page
 * every place has; any other route is a page of the place outside every tenant, shown
 * only there. So choosing a page in a menu never moves the person to another place.
 *
 * Declare a page once, beside its route (`pageRoute`), and place it in the menu file by
 * reference. A page the routes declare and the menu file never places is what
 * `unplacedPages` reports.
 */
export interface Page {
  readonly label: string;
  /** A Material Icon, drawn before the label. */
  readonly icon?: IconName;
  /** The page's address, as the router mounts it: see above for the tenant part and `here`. */
  readonly route: readonly string[];
  /**
   * The permission the page needs; without it the page is left out of the menus. A list
   * means any one of them: a page people reach by more than one permission.
   */
  readonly permission?: PermissionScope | readonly PermissionScope[];
  /** The feature flag the page sits behind; while it is off the page is left out. */
  readonly feature?: string;
}

/** Declares a page: the object the routes carry and the menu file places. */
export function page(declared: Page): Page {
  return declared;
}

/** The route data key a page is carried under (pageRoute), where unplacedPages finds it. */
export const pageDataKey = 'impulsePage';

/**
 * A route that carries its page, so the menu file's places are checked against the routes
 * (unplacedPages). The route's own data is kept.
 */
export function pageRoute(declared: Page, route: Route): Route {
  return { ...route, data: { ...route.data, [pageDataKey]: declared } };
}

/** A menu entry that does something rather than going somewhere: an action. */
export interface MenuAction {
  readonly label: string;
  readonly icon?: IconName;
  /** What the entry does. */
  readonly run: () => void;
  /**
   * Where the action appears: 'outside' keeps it to the place outside every tenant, as a
   * page there would be. Without it the action appears in every place.
   */
  readonly place?: 'outside';
  /** Text at the right edge: a key, a count. */
  readonly hint?: string;
  /** The permission the action needs; a list means any one of them. */
  readonly permission?: PermissionScope | readonly PermissionScope[];
  /** The feature flag the action sits behind. */
  readonly feature?: string;
  /** Drawn, grayed out, and never chosen. */
  readonly disabled?: boolean;
  /** Marks the chosen one of a set, such as Light or Dark. */
  readonly checked?: boolean;
}

/** A menu entry that opens a submenu. */
export interface Submenu {
  readonly label: string;
  readonly icon?: IconName;
  readonly groups: readonly MenuGroup[];
}

/** One entry of a menu: a page, an action or a submenu. */
export type MenuEntry = Page | MenuAction | Submenu;

/** Entries that belong together; a line separates one group from the next. */
export interface MenuGroup {
  /** A small heading over the group. */
  readonly label?: string;
  readonly entries: readonly MenuEntry[];
}

/**
 * One menu across the top: the application's menu file is a list of these. The same
 * declaration draws the menu bar on a wide screen and the side panel on a phone. Nothing
 * here decides who sees what: each page and action names the permission and the feature
 * flag it needs, and the menus leave out every entry the person cannot use, every group
 * left empty, every submenu left empty and every menu left empty.
 */
export interface Menu {
  readonly label: string;
  readonly groups: readonly MenuGroup[];
}

/** Whether a menu entry is a page: it has an address. */
export function isPage(entry: MenuEntry): entry is Page {
  return 'route' in entry;
}

/** Whether a menu entry is an action. */
export function isAction(entry: MenuEntry): entry is MenuAction {
  return 'run' in entry;
}

/** An entry as the person sees it: allowed, with its route filled. */
export interface ShownEntry {
  label: string;
  icon?: string;
  link?: readonly string[];
  run?: () => void;
  hint?: string;
  disabled: boolean;
  checked?: boolean;
  groups?: readonly ShownGroup[];
}

export interface ShownGroup {
  label?: string;
  entries: readonly ShownEntry[];
}

export interface ShownMenu {
  label: string;
  groups: readonly ShownGroup[];
}

/** What the menus ask of the session while they decide what to show. */
export interface MenuContext {
  /** Whether the person may use a destination that needs this permission. */
  allows(permission: PermissionScope): boolean;
  /** Whether a feature flag is on. */
  enabled(feature: string): boolean;
  /** Fills a route for the place the page is in; undefined when the page does not live there. */
  link(route: readonly string[]): readonly string[] | undefined;
  /** Whether a tenant is open; false outside every tenant. */
  inTenant(): boolean;
}

/** Whether the person holds the permission, or any one of a list of them. */
function allowed(permission: PermissionScope | readonly PermissionScope[], context: MenuContext): boolean {
  const any: readonly PermissionScope[] = Array.isArray(permission) ? permission : [permission as PermissionScope];
  return any.some((scope) => context.allows(scope));
}

/** shownEntry decides one entry: undefined when the person cannot use it. */
function shownEntry(entry: MenuEntry, context: MenuContext): ShownEntry | undefined {
  if (!('groups' in entry)) {
    if (entry.permission && !allowed(entry.permission, context)) return undefined;
    if (entry.feature && !context.enabled(entry.feature)) return undefined;
  }
  if (isPage(entry)) {
    const link = context.link(entry.route);
    return link ? { label: entry.label, icon: entry.icon, link, disabled: false } : undefined;
  }
  if (isAction(entry)) {
    if (entry.place === 'outside' && context.inTenant()) return undefined;
    return {
      label: entry.label,
      icon: entry.icon,
      hint: entry.hint,
      run: entry.run,
      checked: entry.checked,
      disabled: entry.disabled ?? false,
    };
  }
  const groups = shownGroups(entry.groups, context);
  // A submenu with nothing left in it goes too.
  return groups.length ? { label: entry.label, icon: entry.icon, groups, disabled: false } : undefined;
}

/** shownGroups keeps the groups that still hold an entry. */
export function shownGroups(groups: readonly MenuGroup[], context: MenuContext): ShownGroup[] {
  return groups
    .map((group) => ({
      label: group.label,
      entries: group.entries.map((entry) => shownEntry(entry, context)).filter((entry) => entry !== undefined),
    }))
    .filter((group) => group.entries.length > 0);
}

/** shownMenus keeps the menus that still hold a group. */
export function shownMenus(menus: readonly Menu[], context: MenuContext): ShownMenu[] {
  return menus
    .map((menu) => ({ label: menu.label, groups: shownGroups(menu.groups, context) }))
    .filter((menu) => menu.groups.length > 0);
}

/**
 * Whether the menus hold anything of the place outside every tenant that the person may
 * use: a page that lives there, or an action kept to it. The context answers for that
 * place (no tenant open). What every place has (a page under `here`, an action with no
 * place) does not count, since it is no reason to be there.
 */
export function showsOutside(menus: readonly Menu[], context: MenuContext): boolean {
  const inGroups = (groups: readonly MenuGroup[]): boolean =>
    groups.some((group) =>
      group.entries.some((entry) => {
        if ('groups' in entry) return inGroups(entry.groups);
        if (entry.permission && !allowed(entry.permission, context)) return false;
        if (entry.feature && !context.enabled(entry.feature)) return false;
        if (isPage(entry)) return !entry.route.includes(here) && context.link(entry.route) !== undefined;
        return entry.place === 'outside';
      }),
    );
  return menus.some((menu) => inGroups(menu.groups));
}

/** Whether a link goes to the page at the URL, or to a page under it. */
export function linksTo(link: readonly string[] | undefined, url: string): boolean {
  if (!link) return false;
  const path = url.split(/[?#]/)[0] ?? '';
  const target = '/' + link.join('/');
  return path === target || path.startsWith(target + '/');
}

/** Whether groups hold a link to the URL, at any depth: the menu the page is in. */
export function holdsUrl(groups: readonly ShownGroup[], url: string): boolean {
  return groups.some((group) =>
    group.entries.some((entry) => linksTo(entry.link, url) || holdsUrl(entry.groups ?? [], url)),
  );
}

/**
 * fillRoute completes a declared route for the open tenant ('' outside every tenant):
 * `here` becomes the tenant's segment or nothing, and a part naming the tenant parameter
 * gets the tenant. A route that names the parameter has no link outside every tenant, and
 * a route that names neither (a page of the place outside every tenant) has none while a
 * tenant is open.
 */
export function fillRoute(
  route: readonly string[],
  tenant: string,
  segment: string,
  param: string,
): readonly string[] | undefined {
  const named = `:${param}`;
  const everyPlace = route.includes(here);
  const tenantPage = route.some((part) => part.includes(named));
  if (!everyPlace && !tenantPage && tenant) return undefined;
  const filled: string[] = [];
  for (const part of route) {
    if (part === here) {
      if (tenant) filled.push(`${segment}/${tenant}`);
    } else if (part.includes(named)) {
      if (!tenant) return undefined;
      filled.push(part.replace(named, tenant));
    } else {
      filled.push(part);
    }
  }
  return filled;
}
