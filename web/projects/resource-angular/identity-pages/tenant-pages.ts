import { inject } from '@angular/core';
import { Route, Router } from '@angular/router';
import { ApiDescriptor, IdentityDescriptor } from '@impulseframework/resource';
import { MenuAction, Page, PlaceService } from '@impulseframework/resource-angular/auth-frame';
import { articleFor } from '@impulseframework/resource-angular/resource';
import { resourcePage, resourceRoutes } from '@impulseframework/resource-angular/resource-route-generator';
import {
  AlertType,
  ChildResourceConfig,
  ColumnConfig,
  ConfigElement,
  ExecutePermission,
  field,
  FieldName,
  keyFields,
  ListViewConfig,
  listViewConfig,
  Method,
  nameInLowerWords,
  nameInWords,
  RecordData,
  Resource,
  ResourceMeta,
  RowButton,
  rootConfig,
  RootConfig,
  RPCConfig,
  section,
} from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';
import { identityPages } from './identity-pages';

// The list of an application's tenants and a tenant's own page, built from the API's own
// description.
//
// The whole application's side of the Auth whose people make the tenants names the
// server's list of tenants (`tenants`): each tenant with the record's own fields, the
// kind of directory its people sign in through, how many people it has, since when where
// the tenant record keeps that, and who administers it. These functions place the page
// that lists them, with the invitations still waiting above it (staff bring an
// organization in by inviting one person, so nobody creates a tenant directly), and a
// tenant's page: the record's fields, and under them a summary that names its
// administrators. A description that names no such list (one from a generator that
// predates it, or an outlet that does not serve it) gets a page over the tenant record
// itself.

/** Which Auth's tenants the page lists, and the methods of the application's own its tenant's page offers. */
export interface TenantPagesOptions {
  /** The Auth whose people make the tenants, by the name the description carries (Crew). */
  auth: string;
  /** The application's own methods, offered on a tenant's page as on any page of the library's. */
  rpcConfigs?: RPCConfig[];
}

/** The configuration of one request, and the methods it was made with. */
interface MadeConfig {
  config: RootConfig;
  rpcConfigs: RPCConfig[] | undefined;
}

// The configurations made for each description and Auth, so the route and the menu
// file's page of one request are the same page (resourcePage answers one per config).
const made = new WeakMap<ApiDescriptor, Map<string, MadeConfig>>();

/**
 * The configuration of the tenants page: the list of the application's tenants and a
 * tenant's page, for the Auth whose people make the tenants. The same description and
 * Auth answer the same configuration. Refused, naming what is missing: an Auth the
 * description does not carry, an Auth with no whole application's side, and a description
 * that names no tenant record for the Auth. A second call for the same description and
 * Auth with other methods (`rpcConfigs`) is refused too: the menu file and the routes file
 * pass the same options.
 */
export function tenantsConfig(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta,
  options: TenantPagesOptions,
): RootConfig {
  const requests = made.get(descriptor) ?? new Map<string, MadeConfig>();
  made.set(descriptor, requests);
  const known = requests.get(options.auth);
  if (known) {
    if (known.rpcConfigs !== options.rpcConfigs) {
      throw new Error(
        `tenantsPage: the tenants page of ${options.auth} was already made with other options; pass tenantsPage and tenantsRoute the same options`,
      );
    }
    return known.config;
  }
  const config = buildTenants(descriptor, resourceMeta, options);
  requests.set(options.auth, { config, rpcConfigs: options.rpcConfigs });
  return config;
}

/**
 * The tenants page, for the application's menu file to place: "All sectors", at the
 * tenant record's own address, needing List on what the page lists. Refused as
 * tenantsConfig is.
 */
export function tenantsPage(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta,
  options: TenantPagesOptions,
): Page {
  return resourcePage(tenantsConfig(descriptor, resourceMeta, options), resourceMeta);
}

/**
 * The routes of the tenants page, carrying its page (tenantsPage): the list, guarded by
 * List on what it lists, and a tenant's page, guarded by Read on the tenant record.
 * Refused as tenantsConfig is.
 */
export function tenantsRoute(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta,
  options: TenantPagesOptions,
): Route {
  return resourceRoutes(tenantsConfig(descriptor, resourceMeta, options), resourceMeta);
}

/**
 * The menu's way to the invitation of a tenant: an action, shown outside every tenant to a
 * person who may send one (Execute on the Auth's method that invites a tenant), that opens
 * the tenants page with the invitation's dialog: it goes to the page's address with the
 * query parameter `run` naming the method, which the page's list opens. It injects the
 * router when it is called, so it is called where the menus are declared, with the
 * options the tenants page is given. Refused where the description names no method that
 * invites a tenant for the Auth, and as tenantsConfig is.
 */
export function inviteTenantAction(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta,
  options: TenantPagesOptions,
): MenuAction {
  const route = tenantsConfig(descriptor, resourceMeta, options).routeData.route;
  const identity = (descriptor.identity ?? []).find((candidate) => candidate.auth === options.auth);
  const method = identity?.sides.find((candidate) => candidate.scope === 'global')?.methods.inviteTenant;
  if (!method) {
    throw new Error(
      `inviteTenantAction: the description names no method that invites a tenant for ${options.auth}: its tenants are not made by invitation, so there is no invitation to place in a menu`,
    );
  }
  const router = inject(Router);
  const one = nameInLowerWords(identity?.tenant ?? 'Tenant');
  return {
    label: `Invite ${articleFor(one)} ${one}…`,
    icon: 'send',
    place: 'outside',
    permission: { resource: method as Method, permission: ExecutePermission },
    run: (): void => {
      void router.navigate([`/${route}`], { queryParams: { run: method } });
    },
  };
}

/** The words a tenant goes by, one and many, inside a sentence and at its start. */
interface TenantWords {
  one: string;
  One: string;
  many: string;
  Many: string;
}

/** The tenant record's fields as the page shows them. */
interface RecordFields {
  /** The field a tenant is called by, where the record has one. */
  name: string | undefined;
  /** The record's key. */
  key: string;
  /** The tenant's own directory-only switch, where the description names it. */
  directoryOnly: string | undefined;
  /** The record's field that says when the tenant was made, where the description names it. */
  since: string | undefined;
  /** Every other field of the record, in the record's order. */
  others: string[];
}

function buildTenants(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta | undefined,
  options: TenantPagesOptions,
): RootConfig {
  const identity = (descriptor.identity ?? []).find((candidate) => candidate.auth === options.auth);
  if (!identity) {
    const known = (descriptor.identity ?? []).map((candidate) => candidate.auth).join(', ') || 'none';
    throw new Error(
      `tenantsPage: the API description carries no generated management of the auth ${options.auth} (it carries: ${known}); the auth is named as GenerateIdentity names it, and its management is served on the outlets that declare or manage it`,
    );
  }
  const side = identity.sides.find((candidate) => candidate.scope === 'global');
  if (!side) {
    throw new Error(
      `tenantsPage: the generated management of ${options.auth} has no whole application's side on this API`,
    );
  }
  if (!identity.tenantResource) {
    throw new Error(
      `tenantsPage: the description names no tenant record for ${options.auth} on this API: the application has no tenants, or this outlet does not serve the record, so there is no list of tenants to place`,
    );
  }
  const record = identity.tenantResource as Resource;
  const words = tenantWords(identity, record);
  const fields = recordFields(identity, resourceMeta(record));
  const listed = side.lists.tenants ? (side.lists.tenants as Resource) : undefined;
  const listedFields = new Set(
    (resourceMeta(listed ?? record)?.fields ?? []).map((candidate) => String(candidate.fieldName)),
  );
  // Since: the list's own column where the list carries it, and the record's own field
  // where the page lists the record; none where the description names no such field.
  const sinceField = listed ? 'since' : fields.since;
  const since = fields.since && sinceField && listedFields.has(sinceField) ? sinceField : undefined;
  const opens = openButton(fields, words);
  const waiting = listed ? waitingList(descriptor, resourceMeta, options.auth, side.lists.invitedTenants) : undefined;
  const invite = waiting?.listMethods;
  const columns: ColumnConfig[] = [
    ...(fields.name ? [{ id: fields.name as FieldName, header: 'Name', opensRow: true }] : []),
    ...fields.others
      .filter((name) => listedFields.has(name))
      .map((name) => ({ id: name as FieldName, header: nameInWords(name) })),
    ...(listed && listedFields.has('directory') ? [{ id: 'directory' as FieldName, header: 'Directory' }] : []),
    ...(fields.directoryOnly ? [{ id: fields.directoryOnly as FieldName, header: 'Directory only' }] : []),
    ...(listed ? [{ id: 'people' as FieldName, header: 'People' }] : []),
    ...(since ? [{ id: since as FieldName, header: 'Since' }] : []),
    // Read with each row and not drawn: what the row button and the row's link open by.
    { id: fields.key as FieldName, hidden: true },
  ];
  const nameField = fields.name;
  return rootConfig({
    page: { label: `All ${words.many}`, icon: 'domain' },
    routeData: { route: resourceMeta(record)?.route ?? '' },
    parentConfig: listViewConfig({
      title: words.Many,
      primaryResource: listed ?? record,
      showRowCount: true,
      emptyMessage: `There are no ${words.many} yet.`,
      listColumns: columns,
      ...(nameField ? { rowTitle: (row: RecordData) => String(row[nameField] ?? '') } : {}),
      rowButtons: [opens],
      ...(invite ? { listMethods: invite } : {}),
      ...(waiting ? { above: [waitingAbove(waiting, words)] } : {}),
      elements: [section({ label: words.One, children: formFields(fields, !listed) })],
    }),
    relatedConfigs: listed ? [summary(listed, listedFields, fields.key, since !== undefined, opens)] : [],
    ...(options.rpcConfigs ? { rpcConfigs: options.rpcConfigs } : {}),
  });
}

/** What the tenants are called: `Sector` and `Sectors` are "sector" and "sectors". */
function tenantWords(identity: IdentityDescriptor, record: Resource): TenantWords {
  const tenant = identity.tenant ?? 'Tenant';
  return {
    one: nameInLowerWords(tenant),
    One: nameInWords(tenant),
    many: nameInLowerWords(record),
    Many: nameInWords(record),
  };
}

/** The tenant record's fields, read from its metadata and the description. */
function recordFields(identity: IdentityDescriptor, meta: ResourceMeta | undefined): RecordFields {
  const key = String(keyFields(meta)[0]?.fieldName ?? 'id');
  const name = meta?.nameField ? String(meta.nameField) : undefined;
  const directoryOnly = identity.tenantDirectoryOnlyField;
  const since = identity.tenantSinceField;
  return {
    name,
    key,
    directoryOnly,
    since,
    others: (meta?.fields ?? [])
      .map((candidate) => String(candidate.fieldName))
      .filter(
        (candidate) => candidate !== key && candidate !== name && candidate !== directoryOnly && candidate !== since,
      ),
  };
}

/**
 * A tenant's form: the name, the record's other fields, and the directory-only switch and
 * the key, both shown and never changed here (the switch is the tenant's own, on its
 * Sign-in settings page). The record's field for when the tenant was made is no field of
 * the form where the page has a summary, which says Since; where the page lists the record
 * itself the form holds it, labeled Since.
 */
function formFields(fields: RecordFields, listsRecord: boolean): ConfigElement[] {
  return [
    ...(fields.name ? [field({ name: fields.name as FieldName, label: 'Name', cols: 4 })] : []),
    ...fields.others.map((name) => field({ name: name as FieldName, label: nameInWords(name), cols: 4 })),
    ...(fields.directoryOnly
      ? [field({ name: fields.directoryOnly as FieldName, label: 'Directory only', cols: 4, readOnly: true })]
      : []),
    ...(listsRecord && fields.since ? [field({ name: fields.since as FieldName, label: 'Since', cols: 4 })] : []),
    field({ name: fields.key as FieldName, label: 'Id', cols: 4, readOnly: true }),
  ];
}

/**
 * The invitations still waiting, as the Auth's identity pages list them, with the method
 * that sends one. None where the side names no such list.
 */
function waitingList(
  descriptor: ApiDescriptor,
  resourceMeta: (resource: Resource) => ResourceMeta | undefined,
  auth: string,
  invitedTenants: string | undefined,
): ListViewConfig | undefined {
  if (!invitedTenants) {
    return undefined;
  }
  const config = identityPages(descriptor, resourceMeta, { auth, scope: 'global' }).find(
    (candidate) => candidate.parentConfig.primaryResource === invitedTenants,
  )?.parentConfig;
  return config?.type === 'ListView' ? config : undefined;
}

/**
 * The waiting invitations as the tenants page draws them above its list: their columns
 * and Withdraw as they are, under a title of their own and with no method above them,
 * drawn only while an invitation waits.
 */
function waitingAbove(waiting: ListViewConfig, words: TenantWords): ListViewConfig {
  const above: ListViewConfig = {
    ...waiting,
    title: `Invited ${words.many}, waiting for a first sign-in`,
    emptyMessage: 'No invitation is waiting.',
    hiddenWhenEmpty: true,
  };
  delete above.listMethods;
  return above;
}

/**
 * The button on a tenant's row that opens the tenant, where it is one of the person's
 * places. Where it is not, a notice says why nothing opened: for a tenant the person holds
 * a seat in and this sign-in cannot enter, the server's reason, as the place switcher
 * shows it; for any other, that the person holds no role in it.
 */
function openButton(fields: RecordFields, words: TenantWords): RowButton {
  return {
    label: `Open this ${words.one}`,
    icon: 'login',
    run: (row, injector) => {
      const id = String(row[fields.key] ?? '');
      const places = injector.get(PlaceService);
      const place = id === '' ? undefined : places.places().find((candidate) => candidate.id === id);
      if (place) {
        void places.open(place);
        return;
      }
      const closed = id === '' ? undefined : places.closed().find((candidate) => candidate.id === id);
      if (closed) {
        injector.get(NotificationService).addGlobalNotification({
          message: closed.reason,
          type: AlertType.INFO,
          duration: 8000,
          link: '',
        });
        return;
      }
      const named = fields.name ? row[fields.name] : undefined;
      const name = typeof named === 'string' && named !== '' ? named : `this ${words.one}`;
      injector.get(NotificationService).addGlobalNotification({
        message: `You hold no role in ${name}, so you cannot open it.`,
        type: AlertType.INFO,
        duration: 5000,
        link: '',
      });
    },
  };
}

/**
 * The summary under a tenant's row: the server's list of tenants, filtered to the row, for
 * what the tenant's form does not show (who administers the tenant, its directory, how many
 * people it has, and since when where the record keeps that), with the button that opens
 * the tenant. It reads and changes nothing.
 */
function summary(
  listed: Resource,
  carried: Set<string>,
  key: string,
  saysSince: boolean,
  opens: RowButton,
): ChildResourceConfig {
  const fixed = (id: string, header: string, lines = false): ColumnConfig => ({
    id: id as FieldName,
    header,
    filterable: false,
    sortable: false,
    ...(lines ? { lines } : {}),
  });
  return listViewConfig({
    title: 'Summary',
    primaryResource: listed,
    parentRelation: { parentKey: key as FieldName, childKey: key as FieldName },
    listColumns: [
      fixed('administrators', 'Administrators', true),
      ...(carried.has('directory') ? [fixed('directory', 'Directory')] : []),
      fixed('people', 'People'),
      ...(saysSince ? [fixed('since', 'Since')] : []),
      { id: key as FieldName, hidden: true },
    ],
    showViewButton: false,
    showRowCount: false,
    showSearchBox: false,
    rowMethods: false,
    shouldRenderActions: { create: () => false, edit: () => false, delete: () => false },
    rowButtons: [opens],
    elements: [],
  });
}
