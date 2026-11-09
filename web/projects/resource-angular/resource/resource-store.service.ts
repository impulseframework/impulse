import {
  computed,
  DestroyRef,
  effect,
  EffectRef,
  inject,
  Injectable,
  Injector,
  ResourceRef,
  Signal,
  signal,
  untracked,
} from '@angular/core';
import { rxResource } from '@angular/core/rxjs-interop';
import { Router } from '@angular/router';
import {
  AlertType,
  ColumnConfig,
  CreateNotificationMessage,
  FieldSort,
  METHOD_META,
  RecordData,
  requestedColumns,
  Resource,
  RESOURCE_DOMAIN,
  ResourceMeta,
  rowCapabilities,
  RPCConfig,
} from '@impulseframework/resource-angular/types';
import { ColumnFilter, composeFilter, filterFields, isComplete, PageTurn } from '@impulseframework/resource-angular/grid';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';
import {
  AnyResourceHandle,
  BatchResult,
  Domain,
  keyBatches,
  keyLookupQuery,
  ListQuery,
  liveSubscription,
  LiveSubscription,
  Operation,
  Page,
  pageIsLive,
  readMode,
  Resource as ClientResource,
  ResourceDescriptor,
  sameSubscription,
  wholeListQuery,
} from '@impulseframework/resource';
import { from, map, mergeMap, Observable, of, tap, toArray } from 'rxjs';
import { PagedList, PagedListRequest } from './paged-list';
import { PagePosition, positionAfter } from './resource-list/list-request';

/** One page of the listed resource as the table shows it, positioned in the server's list. */
export interface ListPage {
  rows: RecordData[];
  /** The rows before this page. */
  offset: number;
  hasPrev: boolean;
  hasNext: boolean;
  /** The total the first page answered, kept while turning; undefined until it answers. */
  total?: number;
}

export type ListPageStatus = 'idle' | 'loading' | 'resolved' | 'error';

const emptyListPage: ListPage = { rows: [], offset: 0, hasPrev: false, hasNext: false };

@Injectable()
export class ResourceStore {
  private static readonly BATCH_REQUEST_CONCURRENCY = 5;

  resourceMeta = signal({} as ResourceMeta);
  resourceName = signal<Resource>('' as Resource);
  filter = signal<string>('');
  disableCacheForFilterPii = signal(false);
  sorts = signal<FieldSort[]>([]);
  listColumns = signal<ColumnConfig[]>([]);
  /**
   * The server page size the list asks for; undefined sends no limit, so the resource's
   * declared default applies. A size over the declared maximum is the server's 400,
   * surfaced as the page's error.
   */
  pageSize = signal<number | undefined>(undefined);
  /** The grid's column filters, composed into the request filter beside `filter`. */
  columnFilters = signal<ColumnFilter[]>([]);
  /** The search box's text, composed into the request filter as its `*:search:"…"` conjunct. */
  searchText = signal<string>('');
  /**
   * Whether the box reaches a field the metadata marks PII, its own or a branch's, under
   * the caller's grants: the list page sets it, and a request carrying the box then
   * travels in the body, as the server requires.
   */
  searchPII = signal(false);
  /** Set while the page has nothing to ask for (the digest grants none of its columns): no request is made. */
  listSuspended = signal(false);
  /**
   * The page's own choice about being live: `live` on its ListViewConfig or ViewConfig,
   * undefined when the config says nothing. Resolved against the metadata in `live`.
   */
  liveChoice = signal<boolean | undefined>(undefined);
  /**
   * Whether the page is live: its own choice resolved against the resource's metadata
   * (`ResourceMeta.live`, whether the resource's changes are published) by pageIsLive in
   * @impulseframework/resource, so a page on a table-backed resource is live unless its config
   * says `live: false`. The list page's requests and the row's read then carry the
   * client's live option, and the page and the row are refetched when the client's live
   * session reports a change to them, or a resync. Nothing changes until the application's
   * live session runs (see LiveSession in @impulseframework/resource): until then the requests are
   * plain.
   */
  live = computed(() =>
    pageIsLive(
      this.resourceName() || this.resourceMeta().route,
      this.liveChoice(),
      this.resourceMeta(),
      this.client.warn,
    ),
  );
  uuid = signal<string>('');
  error = signal<string>('');

  notifications = inject(NotificationService);
  client = inject(RESOURCE_CLIENT);
  router = inject(Router);
  injector = inject(Injector);
  methodMeta = inject(METHOD_META);
  /** The selected tenant, which every request for a domain-scoped resource is bound to. */
  domain = inject(RESOURCE_DOMAIN);
  private destroyRef = inject(DestroyRef);

  private resourceListRef = signal<ResourceRef<RecordData[]> | undefined>(undefined);
  listData = computed(() => {
    const ref = this.resourceListRef();
    if (ref && ref.status() === 'resolved') {
      return ref.value();
    }
    return [];
  });
  listStatus = computed(() => {
    return this.resourceListRef()?.status();
  });
  /**
   * The list request's error when it failed — an ApiError for a server refusal — else
   * undefined, so a table can show a refusal as a refusal rather than as an empty list.
   */
  listError = computed<unknown>(() => {
    const ref = this.resourceListRef();
    return ref && ref.status() === 'error' ? ref.error() : undefined;
  });

  // The server's page, as the list component shows it. One paging model, the server's:
  // the store holds the page the client answered — its rows, whether a previous and a
  // next page exist, and the total the first page asked for, kept while turning — and
  // performs the turns the grid asks for through the page's own relations. A change to
  // the sorts, the filters, the columns, or the page size drops the cursor and asks for
  // a first page with the count.
  private readonly currentPage = signal<Page<RecordData> | undefined>(undefined);
  private readonly position = signal<PagePosition>({ offset: 0 });
  private pageEffect: EffectRef | undefined;
  private pageRequestSeq = 0;
  pageStatus = signal<ListPageStatus>('idle');
  /** The page request's error when it failed — an ApiError for a server refusal — else undefined. */
  pageError = signal<unknown>(undefined);
  page = computed<ListPage>(() => {
    const current = this.currentPage();
    if (!current) {
      return emptyListPage;
    }
    return {
      rows: current.rows,
      offset: this.position().offset,
      hasPrev: current.prev !== undefined,
      hasNext: current.next !== undefined,
      total: this.position().total,
    };
  });

  /**
   * Whether the request must travel in the body: the page asked for it
   * (disableCacheForFilterPii), the page's filter or a column filter names a field the
   * metadata marks PII, or the search box reaches one. The server refuses a PII field in
   * a URL with a 400, so the store posts on its own wherever the metadata lets it tell.
   */
  private readonly sensitive = computed(() => {
    if (this.disableCacheForFilterPii()) {
      return true;
    }
    const pii = new Set((this.resourceMeta().fields ?? []).filter((field) => field.pii).map((field) => field.fieldName));
    if (pii.size > 0 && (filterFields(this.filter()).some((f) => pii.has(f)) || this.columnFilters().some((f) => isComplete(f) && pii.has(f.field)))) {
      return true;
    }
    return this.searchText().trim() !== '' && this.searchPII();
  });

  /**
   * The first-page request as the store's signals describe it: the route and the
   * request filter (the page's filter, the column filters and the search box composed),
   * the columns with every key field, the sorts, and the page size; undefined while the
   * list has nothing to ask for.
   */
  private readonly pageQuery = computed(() => {
    const route = this.route();
    if (!route || this.resourceName() === '' || this.listSuspended()) {
      return undefined;
    }
    return {
      route,
      filter: composeFilter(this.filter(), this.columnFilters(), this.searchText()),
      sensitive: this.sensitive(),
      columns: requestedColumns(
        this.listColumns().map((col) => col.id),
        this.resourceMeta(),
      ),
      sorts: this.sorts(),
      limit: this.pageSize(),
      domain: this.scopedDomain(this.descriptorFor(route)),
      live: this.live(),
    };
  });

  /**
   * The list page's live subscription, the resource in the selected tenant, while the
   * page is live and has a route; undefined otherwise. Equal answers hold the watcher
   * still, so a filter or a sort changing re-registers nothing.
   */
  private readonly pageSubscription = computed(
    () => (this.live() && this.resourceName() !== '' ? this.listSubscription(this.route()) : undefined),
    { equal: sameSubscription },
  );

  /**
   * Starts holding the server's page for the list: from here on, whenever the request the
   * signals describe changes, the store asks for a first page, and while the page is live
   * a change the feed reports to the resource in the tenant requests the page again by
   * its own cursor. Idempotent.
   */
  buildStorePage(): void {
    if (this.pageEffect) {
      return;
    }
    this.pageEffect = untracked(() =>
      effect(
        () => {
          const query = this.pageQuery();
          untracked(() => this.loadFirstPage(query));
        },
        { injector: this.injector },
      ),
    );
    this.watchLive(this.pageSubscription, () => this.reloadPage());
  }

  /** The first page of the current request, with the count. */
  firstPage(): void {
    this.loadFirstPage(untracked(() => this.pageQuery()));
  }

  /** The page the current one names as previous or next; nothing when there is none. */
  turnPage(direction: PageTurn): void {
    if (direction === 'first') {
      this.firstPage();
      return;
    }
    const current = this.currentPage();
    const step = direction === 'next' ? current?.next : current?.prev;
    if (!current || !step) {
      return;
    }
    void this.runPageRequest(step, direction, current.rows.length);
  }

  /** The page the table is on, requested again by its own cursor, so a write shows without losing the position. */
  reloadPage(): void {
    const current = this.currentPage();
    if (!current) {
      this.firstPage();
      return;
    }
    void this.runPageRequest(current.reload, 'reload', current.rows.length);
  }

  private loadFirstPage(query: ReturnType<typeof this.pageQuery>): void {
    if (!query) {
      this.pageRequestSeq++;
      this.currentPage.set(undefined);
      this.position.set({ offset: 0 });
      this.pageError.set(undefined);
      this.pageStatus.set('idle');
      return;
    }
    const handle = this.handleFor(query.route);
    // No limit unless the page names one, so the descriptor's default applies; no sort
    // unless the page names one, so a declared @order applies, and a resource declaring
    // none is refused by the server until the config names a sort or a header is
    // clicked, in the server's own words (see listEmptyMessage). The client fabricates
    // no sort of its own.
    const sorts = query.sorts.map((s) => ({ field: s.field as string, direction: s.direction }));
    const request: ListQuery<RecordData> = {
      filter: query.filter !== '' ? query.filter : undefined,
      sensitiveFilter: query.sensitive || undefined,
      columns: query.columns.length > 0 ? query.columns : undefined,
      sort: sorts.length > 0 ? (sorts as ListQuery<RecordData>['sort']) : undefined,
      limit: query.limit,
      count: true,
      live: query.live || undefined,
    };
    void this.runPageRequest(() => handle.page(request as never) as Promise<Page<RecordData>>, 'first', 0);
  }

  /** Runs one page request; a request that lands after a later one started is dropped. */
  private async runPageRequest(
    request: () => Promise<Page<RecordData>>,
    turn: PageTurn | 'reload',
    leftRows: number,
  ): Promise<void> {
    const seq = ++this.pageRequestSeq;
    this.pageStatus.set('loading');
    try {
      const landed = await request();
      if (seq !== this.pageRequestSeq) {
        return;
      }
      this.position.set(positionAfter(this.position(), turn, leftRows, { rows: landed.rows.length, total: landed.total }));
      this.currentPage.set(landed);
      this.pageError.set(undefined);
      this.pageStatus.set('resolved');
    } catch (error) {
      if (seq !== this.pageRequestSeq) {
        return;
      }
      this.currentPage.set(undefined);
      this.position.set({ offset: 0 });
      this.pageError.set(error);
      this.pageStatus.set('error');
    }
  }

  private resourceViewRef = signal<ResourceRef<RecordData> | undefined>(undefined);
  /**
   * The viewed row whenever the reader holds one: from its first answer and through a
   * reload (a reload keeps the row on screen), empty while the reader is idle, loading,
   * or in error. A page decides what depends on the row with rowPresent, not with this.
   */
  viewData = computed(() => {
    const ref = this.resourceViewRef();
    if (ref?.hasValue()) {
      return ref.value();
    }
    return {} as RecordData;
  });
  /**
   * Whether the row is on hand: true from the reader's first answer and through a
   * reload, false while it is idle (no key yet), loading for the first time, or in
   * error. The compound page draws the row's children only under it, so no child asks
   * for anything with a row that is not there yet, and a reload of the row does not take
   * the children down.
   */
  rowPresent = computed(() => this.resourceViewRef()?.hasValue() ?? false);
  viewStatus = computed(() => {
    return this.resourceViewRef()?.status();
  });
  /** The view request's error when it failed — an ApiError for a server refusal — else undefined. */
  viewError = computed<unknown>(() => {
    const ref = this.resourceViewRef();
    return ref && ref.status() === 'error' ? ref.error() : undefined;
  });

  /**
   * The viewed row's capability envelope: which fields the session user may edit and
   * whether they may delete it. Undefined until the row resolves.
   */
  viewCapabilities = computed(() => rowCapabilities(this.viewData()));

  overrideRoute = signal<string>('');
  resourceRoute = computed(() => this.resourceMeta()?.route);
  route = computed(() => {
    if (this.overrideRoute()) {
      return this.overrideRoute();
    }
    const route = this.resourceRoute();
    if (route) {
      return route;
    }
    return '';
  });

  reloadViewData(): void {
    this.resourceViewRef()?.reload();
  }

  reloadListData(): void {
    this.resourceListRef()?.reload();
  }

  /**
   * Starts holding the whole list (or the one server page of a bounded source) for the
   * store's route, filter, sorts, and PII flag; from here on a change to any of them
   * asks again. Idempotent: one reader per store, however often a component's effect
   * calls this, and no reload after building, since a fresh reader loads on creation.
   */
  buildStoreListData(): void {
    if (untracked(() => this.resourceListRef()) !== undefined) {
      return;
    }
    const route = this.route();
    const name = this.resourceName();
    if (!route || name === '') {
      return;
    }
    // The configured columns and every key field the metadata names, so a row carries
    // the key an operation or a row route lifts.
    const uniqueColumns = signal<string[]>(
      requestedColumns(
        this.listColumns().map((col) => col.id),
        this.resourceMeta(),
      ),
    );

    this.resourceListRef.set(
      this.resourceList(this.route, this.filter, uniqueColumns, this.disableCacheForFilterPii, this.sorts),
    );
  }

  /**
   * Starts holding the row the store's route and key name: idle, with no request, until
   * both are set, one read when they are, and one more whenever either changes.
   * Idempotent: the reader is built once per store, so a component's effect may call
   * this on every run and a second component sharing the store adds no reader.
   */
  buildStoreViewData(): void {
    if (untracked(() => this.resourceViewRef()) !== undefined) {
      return;
    }
    this.resourceViewRef.set(this.resourceView(this.route, this.uuid, this.live));
  }

  /**
   * The typed client handle for the store's resource, built from the descriptor the
   * generator emitted. Mutations assemble their operations here (ops.add lifts key
   * fields into the path, ops.patch and ops.remove address one row) and apply()
   * sends them.
   */
  handle(): AnyResourceHandle {
    const name = this.resourceName();
    const descriptor = this.client.descriptor.resources[name];
    if (!descriptor) {
      throw new Error(`${name} is not in the generated API descriptor`);
    }
    return this.defineScoped(descriptor);
  }

  /**
   * The client handle for a resource the configs address by route: the generated
   * descriptor's when the route is one of its resources — as the descriptor spells it,
   * or as the resource metadata spells a domain-scoped one, with the tenant parameter
   * in braces — otherwise a read-only handle over the route alone (an override route,
   * or a resource registered by hand), keyed by `id`. Every read the store makes goes
   * through the client, so one transport, one paging contract, and one error shape
   * serve the whole library.
   */
  private handleFor(route: string): AnyResourceHandle {
    return this.defineScoped(this.descriptorFor(route));
  }

  /**
   * The descriptor behind a route (see handleFor), without binding a handle: what a
   * reader consults before it asks anything, the read mode above all.
   */
  private descriptorFor(route: string): ResourceDescriptor {
    const known = Object.values(this.client.descriptor.resources).find(
      (r) => r.route === route || this.metadataRoute(r) === route,
    );
    return (
      known ?? {
        resource: route as ClientResource,
        property: route,
        route,
        scope: 'global',
        consolidated: false,
        keys: ['id'],
        operations: ['list', 'read'],
      }
    );
  }

  /**
   * The route as the generated resource metadata carries it: a domain-scoped resource's
   * is prefixed with the domain pair, the tenant parameter in braces
   * (`sectors/{sectorID}/missions`), which the client fills from the bound tenant.
   */
  private metadataRoute(descriptor: ResourceDescriptor): string | undefined {
    const domainRoute = this.client.descriptor.domainRoute;
    if (descriptor.scope !== 'domain' || !domainRoute) {
      return undefined;
    }
    return `${domainRoute.segment}/{${domainRoute.param}}/${descriptor.route}`;
  }

  /**
   * A handle bound to the selected tenant when the resource is domain-scoped. The client
   * refuses to build a domain-scoped handle with no tenant, so a page over such a
   * resource asks for nothing until one is selected.
   */
  private defineScoped(descriptor: ResourceDescriptor): AnyResourceHandle {
    return this.client.define(descriptor, this.scopedDomain(descriptor));
  }

  /**
   * The tenant a request for the resource is bound to: the selected tenant when the
   * resource is domain-scoped, undefined otherwise. Every reader's request key carries
   * this rather than the tenant itself, so a global resource's key does not change when
   * the tenant does (the reader asks once), while a domain-scoped resource's key does
   * (the reader asks again, naming the new tenant).
   */
  private scopedDomain(descriptor: ResourceDescriptor): Domain | undefined {
    return descriptor.scope === 'domain' ? this.domain() : undefined;
  }

  /**
   * The live subscription of a list over the route: the resource in the selected tenant
   * when it is domain-scoped, the resource alone when global. Undefined with no route, or
   * for a domain-scoped resource with no tenant selected, which asks for nothing anyway.
   */
  private listSubscription(route: string): LiveSubscription | undefined {
    if (!route) {
      return undefined;
    }
    const descriptor = this.descriptorFor(route);
    const domain = this.scopedDomain(descriptor);
    if (descriptor.scope === 'domain' && domain === undefined) {
      return undefined;
    }
    return liveSubscription(descriptor, domain);
  }

  /** The live subscription of one row over the route, by its key; undefined on the same terms as listSubscription, and with no key. */
  private rowSubscription(route: string, key: string): LiveSubscription | undefined {
    if (!route || !key || key === 'undefined') {
      return undefined;
    }
    const descriptor = this.descriptorFor(route);
    const domain = this.scopedDomain(descriptor);
    if (descriptor.scope === 'domain' && domain === undefined) {
      return undefined;
    }
    return liveSubscription(descriptor, domain, [key]);
  }

  /**
   * Holds the row or list `subscription` names on the client's live session for as long
   * as the signal names it: a change the feed reports to it, or a resync, runs `refetch`.
   * A new answer from the signal releases the old hold and takes the new; undefined holds
   * nothing. Released with the store.
   */
  private watchLive(subscription: Signal<LiveSubscription | undefined>, refetch: () => void): void {
    let release: (() => void) | undefined;
    const ref = untracked(() =>
      effect(
        () => {
          const current = subscription();
          untracked(() => {
            release?.();
            release = current ? this.client.live.watch(current, () => refetch()) : undefined;
          });
        },
        { injector: this.injector },
      ),
    );
    this.destroyRef.onDestroy(() => {
      release?.();
      ref.destroy();
    });
  }

  /**
   * Sends operations through the client's consolidated endpoint as one transaction
   * and reports success. The client raises ApiError on refusal, so the notification
   * fires only on commit.
   */
  async apply(operations: Operation[], message: string): Promise<BatchResult> {
    const result = await this.client.batch(operations);
    this.notifications.addGlobalNotification({
      message,
      type: AlertType.SUCCESS,
      duration: 5000,
      link: '',
    } satisfies CreateNotificationMessage);
    return result;
  }

  /**
   * One row, read by route and key. The params track both signals and, for a
   * domain-scoped resource, the tenant, so a change to any of them reads again; while the
   * route or the key is empty (or the string 'undefined', which a template renders for a
   * missing value) the params are undefined, and the reader is idle with no value and no
   * request. Nothing answers an empty key with an empty row, since an empty row would
   * count as present. While `live` is set the read carries the client's live option (so
   * the first live read subscribes the tab to the row) and the row is held on the live
   * session: a change the feed reports to it reads it again by the change's version.
   */
  resourceView(
    route: Signal<string>,
    uuid: Signal<string>,
    live: Signal<boolean> = signal(false),
  ): ResourceRef<RecordData> {
    return untracked(() => {
      const ref = rxResource({
        injector: this.injector,
        params: () => {
          const currentRoute = route();
          const key = uuid();
          if (!currentRoute || !key || key === 'undefined') {
            return undefined;
          }
          return {
            route: currentRoute,
            uuid: key,
            domain: this.scopedDomain(this.descriptorFor(currentRoute)),
            live: live(),
          };
        },
        stream: ({ params }) => {
          // The view is the edit surface: opt into the capability envelope so each
          // field and the delete button render from the row's own affordances.
          return from(
            this.handleFor(params.route).read([params.uuid], {
              capabilities: ['Update', 'Delete'],
              live: params.live || undefined,
            }) as Promise<RecordData>,
          );
        },
      }) as ResourceRef<RecordData>;
      this.watchLive(
        computed(() => (live() ? this.rowSubscription(route(), uuid()) : undefined), { equal: sameSubscription }),
        () => ref.reload(),
      );
      return ref;
    });
  }

  resourceList(
    route: Signal<string>,
    filter: Signal<string> = signal(''),
    columns: Signal<string[]> = signal([]),
    disableCacheForFilterPii: Signal<boolean> = signal(false),
    sorts: Signal<FieldSort[]> = signal([]),
    limit: Signal<number | undefined> = signal(undefined),
  ): ResourceRef<RecordData[]> {
    return untracked(() => {
      return rxResource({
        defaultValue: [] as RecordData[],
        injector: this.injector,
        params: () => {
          const currentRoute = route();
          return {
            route: currentRoute,
            filter: filter(),
            columns: columns(),
            sensitive: disableCacheForFilterPii(),
            sorts: sorts(),
            limit: limit(),
            domain: this.scopedDomain(this.descriptorFor(currentRoute)),
          };
        },
        stream: ({ params }) => {
          if (!params.route) return of([] as RecordData[]);
          return this.list<RecordData>(
            String(params.route),
            params.filter,
            params.sensitive,
            params.columns,
            params.sorts,
            params.limit,
          );
        },
      }) as ResourceRef<RecordData[]>;
    });
  }

  /**
   * The display values a list's referenced-resource columns show, resolved on the read
   * mode of the referenced resource (readMode). A source with no maximum is read whole
   * once, `limit=all`, and mapped, whatever keys the page holds, so turning the page asks
   * nothing more of it. A source with a maximum is asked for one `filter=<key>:in:(…)`
   * page per batch of the page's keys, each batch no larger than its maximum (and than
   * `batchSize`), each answered by the key's index in one request; nothing is walked and
   * nothing is read whole.
   */
  resourceListByKeys(
    route: Signal<string>,
    keyField: Signal<string>,
    keys: Signal<string[]>,
    columns: Signal<string[]> = signal([]),
    batchSize = 200,
  ): ResourceRef<RecordData[]> {
    if (!Number.isInteger(batchSize) || batchSize <= 0) {
      throw new Error(`resourceListByKeys: batchSize must be a positive integer, got ${batchSize}`);
    }

    return untracked(() => {
      return rxResource({
        defaultValue: [] as RecordData[],
        injector: this.injector,
        params: () => {
          const resourceRoute = route();
          const descriptor = this.descriptorFor(resourceRoute);
          const paged = resourceRoute !== '' && readMode(descriptor) === 'paged';
          return {
            route: resourceRoute,
            keyField: keyField(),
            // A whole source is read once for every page: the keys are not a parameter.
            keys: paged ? keys() : [],
            paged,
            columns: columns(),
            domain: this.scopedDomain(descriptor),
          };
        },
        stream: ({ params }) => {
          if (!params.route || !params.keyField) {
            return of([] as RecordData[]);
          }
          const handle = this.handleFor(params.route);
          const columnsAsked = params.columns.length > 0 ? params.columns : undefined;
          if (!params.paged) {
            return from(
              handle.list(wholeListQuery<RecordData>(handle.descriptor, { columns: columnsAsked }) as never) as Promise<RecordData[]>,
            );
          }
          if (params.keys.length === 0) {
            return of([] as RecordData[]);
          }
          return from(keyBatches(handle.descriptor, params.keys, batchSize)).pipe(
            mergeMap(
              (batch) =>
                from(
                  handle.list(
                    keyLookupQuery<RecordData>(handle.descriptor, params.keyField, batch, columnsAsked) as never,
                  ) as Promise<RecordData[]>,
                ),
              ResourceStore.BATCH_REQUEST_CONCURRENCY,
            ),
            toArray(),
            map((results) => results.flat()),
          );
        },
      }) as ResourceRef<RecordData[]>;
    });
  }

  /**
   * One server page of a resource, for a reader that pages a bounded source (a picker
   * over a resource with a maximum page size): the first page, with its count, whenever
   * the request changes, and Previous and Next by the server's relations. The request is
   * bound to the selected tenant when the resource is domain-scoped and goes through the
   * client like every other read.
   */
  resourcePage(request: Signal<PagedListRequest | undefined>): PagedList<RecordData> {
    const params = computed(() => {
      const current = request();
      return current ? { ...current, domain: this.scopedDomain(this.descriptorFor(current.route)) } : undefined;
    });
    return untracked(
      () =>
        PagedList.over<RecordData, PagedListRequest & { domain: Domain | undefined }>(this.injector, params, (p) => {
          const handle = this.handleFor(p.route);
          const query: ListQuery<RecordData> = {
            filter: p.filter && p.filter.trim() !== '' ? p.filter : undefined,
            sensitiveFilter: p.sensitive || undefined,
            columns: p.columns && p.columns.length > 0 ? p.columns : undefined,
            sort: p.sorts && p.sorts.length > 0 ? (p.sorts as ListQuery<RecordData>['sort']) : undefined,
            limit: p.limit,
            count: true,
          };
          return handle.page(query as never) as Promise<Page<RecordData>>;
        }),
    );
  }

  /**
   * Lists a resource through the client for the readers that want a whole set — the
   * array view, the pickers, the referenced-resource lookups — on the resource's read
   * mode (readMode). A source with no maximum is read whole, `limit=all`, in one
   * request. A source with a maximum is never read whole, so the answer is one server
   * page, the descriptor's default, and a bounded source with no `@order` and no sort is
   * refused by the server in its own words, which the reader shows. A caller that names
   * a limit gets that page on either. A PII filter travels in the request body. The
   * list component does not read here: it holds one server page (see buildStorePage).
   */
  private list<T>(
    resourceRoute: string,
    filter?: string,
    disableCacheForFilterPii?: boolean,
    columns?: string[],
    sort?: FieldSort[],
    limit?: number,
  ): Observable<T[]> {
    const handle = this.handleFor(resourceRoute);
    const query = {
      filter: filter && filter.trim() !== '' ? filter : undefined,
      sensitiveFilter: disableCacheForFilterPii || undefined,
      columns: columns && columns.length > 0 ? columns : undefined,
      sort: sort && sort.length > 0 ? sort.map((s) => ({ field: s.field as string, direction: s.direction })) : undefined,
      limit: limit && limit > 0 ? limit : undefined,
    };
    const request =
      query.limit !== undefined || readMode(handle.descriptor) === 'paged'
        ? query
        : wholeListQuery<RecordData>(handle.descriptor, query as ListQuery<RecordData>);
    return from(handle.list(request as never) as Promise<T[]>);
  }

  rpcCall<T>(rpcConfig: RPCConfig, body: T): Observable<T> {
    const methodData = this.methodMeta(rpcConfig.method);
    if (!methodData) {
      console.error('Method not found in methodMap:', rpcConfig.method);
      return of({} as T);
    }

    return from(this.client.request<T>('POST', methodData.route, { body })).pipe(
      tap(() => {
        this.notifications.addGlobalNotification({
          message: rpcConfig.successMessage ? rpcConfig.successMessage : `${rpcConfig.method} called successfully`,
          type: AlertType.SUCCESS,
          duration: 5000,
          link: '',
        } satisfies CreateNotificationMessage);
        if (rpcConfig.afterMethodRedirect) {
          if (typeof rpcConfig.afterMethodRedirect === 'string') {
            this.router.navigate([rpcConfig.afterMethodRedirect]);
          } else {
            this.router.navigate(rpcConfig.afterMethodRedirect);
          }
        }
      }),
    );
  }
}
