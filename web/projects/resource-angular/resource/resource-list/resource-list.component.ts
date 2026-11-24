import { NgComponentOutlet } from '@angular/common';
import {
  Component,
  computed,
  DestroyRef,
  effect,
  inject,
  Injector,
  input,
  linkedSignal,
  OnInit,
  output,
  runInInjectionContext,
  signal,
  Type,
  untracked,
} from '@angular/core';
import { ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatExpansionModule } from '@angular/material/expansion';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { MatDialog } from '@angular/material/dialog';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import {
  ActivatedRoute,
  Event as RouterEvent,
  NavigationCancel,
  NavigationCancellationCode,
  NavigationEnd,
  NavigationError,
  NavigationSkipped,
  Router,
  RouterModule,
  UrlTree,
} from '@angular/router';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import {
  AppGridComponent,
  ColumnFilter,
  filterFields,
  PageTurn,
  RowKey,
  SortRule,
  withoutOrphanedCompanions,
} from '@impulseframework/resource-angular/grid';
import { pageRouteResolver, resourcePageRoute, rowPageRoute } from '@impulseframework/resource-angular/resource-nav';
import {
  Domain,
  ListPermission,
  PermissionDigestState,
  RemoveOptions,
  TenantProperty,
} from '@impulseframework/resource';
import {
  AlertType,
  ChildResourceConfig,
  ColumnConfig,
  DataType,
  DeletePermission,
  ExecutePermission,
  FieldName,
  FieldSort,
  iconCellKey,
  keyFields,
  linkCellKey,
  ListViewConfig,
  ReadPermission,
  RecordData,
  RESOURCE_META,
  RootConfig,
  RowAction,
  rowActionsKey,
  rowAnswerKey,
  rowButtonKey,
  roleInWords,
  rowRouteTarget,
  writeResource,
} from '@impulseframework/resource-angular/types';
import {
  ActionAccessControlWrapperComponent,
  ActionButtonContext,
} from '../actions/action-button-smart/action-access-control-wrapper.component';
import { applyFormatting, formatFieldValue } from '../format-fns';
import { addRowLabel, rowMessage, rowWordsOf } from '../row-words';
import { iconTemplateFields, resolveIconCell } from './icon-cell';
import {
  DeleteConfirmation,
  DeleteResourceConfirmationModalComponent,
} from '../delete-resource-confirmation-modal/delete-resource-confirmation-modal.component';
import { methodDoneMessage, openMethodDialog } from '../method-dialog/open-method-dialog';
import {
  filledMethods,
  inNamedOrder,
  offeredAs,
  OfferedMethod,
  parentOffers,
  rowMethods,
} from '../row-methods/row-methods';
import { ResourceStore } from '../resource-store.service';
import { SearchBoxComponent } from '../search-box/search-box.component';
import {
  deniedSearchFields,
  searchDeniedHint,
  listableFieldsOf,
  RefusedSearchFields,
  refusedSearchFields,
  searchedFields,
  searchFieldLabel,
  searchPlaceholder,
  searchReachesPII,
  searchRefusedNotice,
} from '../search-box/search-fields';
import {
  configuredFilterChoices,
  filterChoiceLists,
  filterEligibility,
  filterInputKinds,
  foundRolesLimit,
  listEmptyMessage,
  listSorts,
  noRecordsFound,
  queryableMeta,
  refuseKeylessConfig,
  refuseNameColumns,
  refuseWriteOnlyColumns,
  roleQuestionField,
  rolesQuestion,
  sameSorts,
  searchEligibility,
  unorderedFields,
} from './list-request';
import { listableColumns, sameColumns } from './listable-columns';
import {
  askedNames,
  cellText,
  listableNames,
  listedColumns,
  namesListableOf,
  requestSorts,
  tableSorts,
} from './name-columns';
import {
  addressWrittenState,
  filterParam,
  readAddressFilter,
  sameFilters,
  standingFilters,
  writeAddressFilter,
} from './address-filters';
import { acrossTenantsLinkKey, refuseAcrossTenantsConfig, tenantLink, tenantWord } from './across-tenants';

@Component({
  standalone: true,
  imports: [
    AppGridComponent,
    RouterModule,
    MatIconModule,
    MatButtonModule,
    MatExpansionModule,
    MatTooltipModule,
    ReactiveFormsModule,
    ActionAccessControlWrapperComponent,
    NgComponentOutlet,
    SearchBoxComponent,
  ],
  selector: 'impulse-resource-list',
  templateUrl: './resource-list.component.html',
  styleUrls: ['./resource-list.component.scss'],
  providers: [ResourceStore],
})
export class ResourceListComponent implements OnInit {
  /* eslint-disable  @typescript-eslint/no-explicit-any */
  compoundResourceComponent = input.required<Type<any>>();

  resourceMeta = inject(RESOURCE_META);
  router = inject(Router);
  store = inject(ResourceStore);
  injector = inject(Injector);
  activatedRoute = inject(ActivatedRoute);
  auth = inject(AuthService);
  dialog = inject(MatDialog);
  private readonly destroyRef = inject(DestroyRef);

  hideCreateButton = input<boolean>(true);
  /**
   * The list's create button was pressed. The list draws no form of its own: the page
   * that holds it opens the create in a dialog.
   */
  createRequested = output<void>();
  /**
   * A method ran from this list, against one of its rows or from above it. The list has
   * already asked for its rows again; a page that holds the list under a row reads the
   * row and its other lists again. A list whose page has left by then tells nobody.
   */
  methodRan = output<void>();
  resourceConfig = input<ChildResourceConfig>();
  viewRoute = input<string>();
  filter = input<string>('');
  isRootList = input<boolean>(true);

  showCreateButton = computed(() => {
    const config = this.config();
    // A list across tenants reads alone: a row is created inside one tenant.
    if (this.across()) {
      return false;
    }
    if (config.createTitle !== '' && !config.collapsible && !this.hideCreateButton()) {
      return true;
    }
    if (config.createConfig && Object.keys(config.createConfig).length !== 0) {
      return true;
    }
    return false;
  });

  createButtonContext = computed(() => {
    const config = this.resourceConfig();
    const showCreate = this.showCreateButton();

    if (config === undefined || showCreate === undefined) {
      return undefined;
    }

    if (config.shouldRenderActions === undefined) {
      return undefined;
    }

    return {
      actionType: 'create',
      meta: this.writeMeta(),
      resource: this.writeResourceName(),
      shouldRender: (data: RecordData): boolean => showCreate && config.shouldRenderActions.create(data),
      resourceData: this.relatedData() ?? {},
    } satisfies ActionButtonContext;
  });

  relatedData = input<RecordData>();
  parentId = input<string>();

  routeConfig = computed(() => {
    return this.activatedRoute.snapshot.data['config'] as RootConfig;
  });

  config = computed(() => {
    return this.resourceConfig() as ListViewConfig;
  });

  /**
   * Whether this list keeps its column filters in the page's address: the list the page
   * is built on, handed the route's own config. A list under a row, under another list, or
   * mounted by a component with a config of its own keeps its filters to itself, since two
   * lists cannot share one parameter.
   */
  private readonly ownsAddress = computed(
    () => this.isRootList() !== false && this.routeConfig()?.parentConfig === this.config(),
  );

  /** Whether a write of the address waits for the navigation in flight to end, which it would cancel. */
  private addressWriteWaiting = false;

  /**
   * The column filters the page opens with where its address names none (opensWith),
   * each read as a filter in an address is read, so one the grid holds no such filter for
   * is dropped.
   */
  private readonly openingFilters = computed(() =>
    (this.config().opensWith ?? [])
      .map((filter) =>
        readAddressFilter(
          filter.value === ''
            ? `${filter.field}:${filter.operator}`
            : `${filter.field}:${filter.operator}:${filter.value}`,
        ),
      )
      .filter((filter): filter is ColumnFilter => filter !== undefined),
  );

  expansionConfig = computed(() => {
    const config = this.config().rowExpansionConfig;
    if (config.type !== 'Component' && config.type !== 'Array') {
      config.showBackButton = false;
    }
    return config;
  });

  viewRouteFallback = computed(() => {
    if (this.viewRoute()) {
      return this.viewRoute();
    }

    return this.resourceMeta(this.config().primaryResource).route;
  });

  /**
   * Whether the list knows what its heading says. A title that carries the count of the
   * list's rows (`showRowCount`) is not known until the list's first request has been
   * answered, and the heading is not drawn until then: a title drawn without its count
   * would grow when the rows arrive, and push the Create button along or onto a line of
   * its own. Every other heading is known from the start. A list with nothing to ask for
   * (no column the person may see) has its heading at once, as its table has its sentence.
   */
  headingKnown = computed(() => {
    const config = this.config();
    if (!config.showRowCount || config.collapsible || !config.title) {
      return true;
    }
    return this.store.firstPageAnswered() || this.noListableColumns();
  });

  listTitle = computed(() => {
    if (this.config()?.collapsible) return '';

    const title = this.config().title || '';
    if (!title) return '';

    if (!this.config().showRowCount) return title;
    // The server's total, answered with the first page and kept while turning.
    const total = this.store.page().total;
    return total !== undefined && total > 0 ? `${title} (${total})` : title;
  });

  /**
   * The page's standing notice, said from the rows the page holds; empty where the
   * page gives none, or where its rows give it nothing to say.
   */
  notice = computed(() => this.config()?.notice?.(this.store.page().rows) ?? '');

  /**
   * Whether the page's address named a filter the list cannot show the person, so the
   * list is shown without it and says so above its rows. The list's own rewrite of the
   * address leaves it standing; the person's next commit, and an address whose filters
   * all stand, take it away.
   */
  filtersLeftOut = signal(false);

  /** The refusals and failures already written to the console, so each is written once. */
  private logged = new WeakSet<object>();

  indentTitle = computed(() => {
    if (this.config() === undefined) {
      return false;
    }

    return true;
  });

  /**
   * The words a row of the list goes by: the listed resource's, as its metadata carries
   * them. The add button, the delete question and the message after a delete name the
   * row by them.
   */
  rowWords = computed(() => rowWordsOf(this.meta()));

  /** The add button's label: the config's own, else "Add a hangar pass" from the row's word. */
  createButtonLabel = computed(() => addRowLabel(this.config().createButtonLabel, this.rowWords()));

  /** The listed resource's metadata: the rows, columns, and keys the table shows. */
  meta = computed(() => this.resourceMeta(this.config().primaryResource));
  /**
   * The resource the page's writes go to and whose form it builds: the metadata's
   * rowsOf when the list is a view declaring its table, else the listed resource.
   */
  writeResourceName = computed(() => writeResource(this.config().primaryResource, this.meta()));
  writeMeta = computed(() => this.resourceMeta(this.writeResourceName()));
  /**
   * Where a row opens: the rowRoute field's target, else the write resource by its
   * single key; undefined when a compound key has no rowRoute. A rowRoute naming a
   * field with no target in the metadata throws here, naming the field.
   */
  rowTarget = computed(() =>
    rowRouteTarget(this.config().primaryResource, this.config().rowRoute, this.resourceMeta, resourcePageRoute),
  );
  /** Puts the page's tenant into a registered route a row link is built from, where the route names the tenant parameter. */
  private readonly resolvePageRoute = pageRouteResolver();
  /**
   * Whether the page lists the resource across tenants (ListViewConfigOptions.acrossTenants):
   * the rows come from every tenant the person reaches, each naming its tenant, the page
   * is bound to no tenant, and it reads alone.
   */
  across = computed(() => this.config().acrossTenants === true);
  /**
   * The tenants whose digest this list has asked for, for the links of a list across
   * tenants: each is asked once while the list is on the screen, answered or not, and
   * again only when the person asks (retryLinks).
   */
  private readonly askedDigests = new Set<string>();
  /** The tenants whose digest this list asked for and whose read has ended: answered, refused or failed. */
  private readonly endedDigests = signal<ReadonlySet<string>>(new Set());
  /**
   * The page a row of a list across tenants opens on: where the application mounts a row
   * route for the record a row opens (rowPageRoute), as its config wrote the route.
   * Undefined when the application mounts none, and then the rows draw no link: a link
   * leads to the row itself in its tenant or it is not drawn.
   */
  acrossRowRoute = computed(() => {
    const target = this.across() ? this.rowTarget() : undefined;
    return target ? rowPageRoute(target.resource) : undefined;
  });
  /**
   * Whether rows are deleted from the list: the write resource's key is compound, so a
   * row has no page of its own to delete from, and the digest grants Delete on it. A
   * single-key row is deleted from its page, as before.
   */
  deletesFromList = computed(() => {
    const meta = this.writeMeta();
    // A list across tenants reads alone: a row is deleted on its page inside its tenant.
    if (this.across() || !meta || meta.deleteDisabled || keyFields(meta).length < 2) {
      return false;
    }
    return this.auth.hasPermission({ resource: this.writeResourceName(), permission: DeletePermission });
  });
  /**
   * The generated methods that run against a row of the listed resource (`@target`
   * naming it), by the method metadata: the methods the list asks the server about with
   * each row and a row's menu may offer. None when the page switches them off.
   */
  rowMethodNames = computed<string[]>(() => {
    const config = this.config();
    // A method runs inside one tenant, so a list across tenants offers none: the
    // row's page inside its tenant does.
    if (config.rowMethods === false || !config.primaryResource || this.across()) {
      return [];
    }
    return this.store.methodsTargeting(config.primaryResource);
  });
  /**
   * The methods the page fills from a row (`filledMethodNames`) that the rows of the listed
   * resource answer for (`offeredOn` naming it), by the method metadata: the list asks the
   * server about them with each row, and a row's menu offers each where the row's answer
   * lists it. None where the page fills none of them, so a list that would not read the
   * answer does not ask for it.
   */
  offeredMethodNames = computed<string[]>(() => {
    const config = this.config();
    if (config.rowMethods === false || !config.primaryResource || this.across()) {
      return [];
    }
    const filled = this.filledMethodNames();
    return this.store.methodsOfferedOn(config.primaryResource).filter((method) => filled.includes(method));
  });
  /**
   * The methods the page fills from a row and that name no target row (`fill` on a
   * presentation among `rowMethods`), each while the digest grants the caller Execute
   * on it: offered on every row, since the server answers nothing for a row the method
   * does not name, and, for a method the rows of the listed resource answer for
   * (`offeredOn` naming it), where the row's Execute answer lists it.
   */
  filledMethodNames = computed<string[]>(() =>
    (this.across() ? [] : (this.methodPresentations() ?? []))
      .filter(
        (presentation) =>
          presentation.fill && !presentation.hidden && !this.store.methodMeta(presentation.method)?.target,
      )
      .filter((presentation) =>
        this.auth.hasPermission({ resource: presentation.method, permission: ExecutePermission }),
      )
      .map((presentation) => presentation.method),
  );
  /** How the page presents the methods it names; undefined where it names none. */
  private methodPresentations = computed(() => {
    const rowMethodsConfig = this.config().rowMethods;
    return rowMethodsConfig === false ? undefined : rowMethodsConfig;
  });
  /**
   * The methods offered above the list: the ones the page names (`listMethods`). One
   * that names no row is drawn when the digest grants the caller Execute on it in the
   * selected tenant. One that names a row (`@target`) runs against the row the list sits
   * under, so it is drawn when the server's answer for that row lists it: the page
   * offers it only where the caller may run it on that row, and where the page's own
   * predicate, told the rows the list shows, does not say no.
   */
  listMethods = computed<OfferedMethod[]>(() => {
    const presentations = this.across() ? [] : (this.config().listMethods ?? []);
    return presentations
      .filter((presentation) => !presentation.hidden)
      .filter((presentation) =>
        this.store.methodMeta(presentation.method)?.target
          ? parentOffers(this.relatedData(), presentation, this.store.page().rows)
          : this.auth.hasPermission({ resource: presentation.method, permission: ExecutePermission }),
      )
      .map((presentation) => offeredAs(presentation.method, this.store.methodMeta(presentation.method), presentation));
  });

  /** The row field an expanded row is opened by: the listed resource's single key, none on a key-less resource. */
  expansionKey = computed(() => keyFields(this.meta())[0]?.fieldName as FieldName | undefined);
  primaryKeys = computed(() => keyFields(this.meta()));
  /**
   * Whether the listed resource has no primary key (a `@computed` or `@virtual` struct
   * with no `@primarykey`): its list is served whole, so the page draws every row the
   * server returned as one page, identified by position, with no page size, no row
   * expansion, and no row route; a config asking for one of those is refused in ngOnInit.
   */
  keyless = computed(() => this.primaryKeys().length === 0);
  /**
   * How the grid identifies a row: the listed resource's key fields joined into one
   * value, the same fields the handle's keyOf lifts, so selection and expansion are right
   * on a compound key and on a key not named `id`; nothing on a key-less resource, whose
   * rows the grid identifies by their position in the page.
   */
  rowKey = computed<RowKey | undefined>(() => {
    const keys = this.primaryKeys();
    if (keys.length === 0) {
      return undefined;
    }
    return (row: RecordData): string => {
      return JSON.stringify(keys.map((key) => row[key.fieldName]));
    };
  });
  /**
   * The digest's field-level List entries for the page's resource: the columns worth
   * asking for. A configured column outside them is denied, and a request naming it
   * refuses the whole list, so it leaves the request and the table alike — the way the
   * create form leaves out the inputs the digest does not grant. Undefined means nothing
   * is known (the digest has not loaded, or grants List with no field entries, a keys-only
   * resource) and nothing narrows; a loaded digest holding no List on the resource at all
   * lists nothing. Conditional entries stay; the server masks their cells.
   */
  listableFields = computed<ReadonlySet<string> | undefined>(() => {
    const resource = this.config().primaryResource;
    // Across tenants no one digest speaks for the rows: the server decides, tenant by
    // tenant, which of the page's columns a row shows, and nothing narrows here.
    if (this.across()) {
      return undefined;
    }
    return resource ? this.listableOf(resource) : undefined;
  });

  /** The digest's List field set for any resource (listableFieldsOf): the page's own and a search branch's child. */
  private readonly listableOf = listableFieldsOf(this.auth);

  /**
   * The digest's List field set for a resource an id points to, as its name is judged
   * (namesListableOf): the empty set once a digest has answered and names no field.
   */
  private readonly namesOf = namesListableOf(this.auth);

  /**
   * The configured columns the caller may list: every column when nothing narrows,
   * otherwise those whose fields the digest grants. Key fields are structural, never
   * grant-bearing, and always pass. Equal while the same columns: every digest load
   * re-evaluates this, and one that changes the answer for none of them (another scope's
   * digest landing after sign-in, a reload) leaves the columns as they are, so the store's
   * request, which they are part of, is not asked again.
   */
  listColumns = computed<ColumnConfig[]>(
    () =>
      listableColumns(
        this.configuredColumns(),
        this.listableFields(),
        new Set<string>(this.primaryKeys().map((pk) => pk.fieldName)),
      ),
    { equal: sameColumns },
  );

  /**
   * The columns the page configures, plus the fields the rows must carry that the page
   * does not show, each as a hidden column: the rowRoute field (a row carries the key its
   * route lifts) and the fields an icon column's sentences name in braces. A hidden column
   * passes the digest like any other, so a rowRoute the caller may not list draws no View
   * column, and a sentence's field the caller may not list fills as nothing.
   */
  configuredColumns = computed<ColumnConfig[]>(() => {
    const configured = [...(this.config().listColumns || [])];
    const carried = new Set<string>(configured.map((col) => col.id));
    const carry = (field: string): void => {
      if (!carried.has(field)) {
        carried.add(field);
        configured.push({ id: field as FieldName, hidden: true });
      }
    };
    const rowRoute = this.config().rowRoute;
    if (rowRoute) {
      carry(rowRoute);
    }
    for (const col of this.config().listColumns || []) {
      if (col.iconConfig) {
        iconTemplateFields(col.iconConfig).forEach(carry);
      }
    }
    return configured;
  });

  /** One of the row's fields as a sentence writes it: by the name of a fixed value, else by the field's display type, a null as nothing. */
  private fieldText = (field: string, value: DataType | null): string => {
    return formatFieldValue(
      this.meta()?.fields?.find((f) => f.fieldName === field),
      value,
    );
  };

  /** Whether the rows carry the field their route lifts: no rowRoute, or one the list requests. */
  rowRouteRequested = computed(() => {
    const rowRoute = this.config().rowRoute;
    return !rowRoute || this.listColumns().some((col) => col.id === rowRoute);
  });

  /**
   * Whether the caller may open a row: the digest's Read answer for the resource a row
   * opens (granted, conditional or conditional on time). Without it the view column is
   * not drawn, since the row page would only be refused; the row route carries the same
   * gate.
   */
  canView = computed(() => {
    const target = this.rowTarget();
    if (!target) {
      return false;
    }
    // Across tenants a row opens inside its own tenant: the column is drawn where the
    // application mounts the record a row route, and each row carries its own link, drawn
    // where that tenant's digest lets the person read the record (processedRowData).
    if (this.across()) {
      return this.acrossRowRoute() !== undefined;
    }
    return this.auth.hasPermission({ resource: target.resource, permission: ReadPermission });
  });

  /**
   * Whether a row opens: the page lets rows open (`showViewButton`), a row has somewhere
   * to open, the rows carry the field their route lifts, and the caller may read what a row
   * opens. Then the row's link is drawn: the View column, or the column that says it opens
   * its row (`opensRow`).
   */
  private rowOpens = computed(
    () => !!this.config().showViewButton && !!this.rowTarget() && this.rowRouteRequested() && this.canView(),
  );

  /**
   * The page a row opens on, outside a list across tenants: the page's own row route for
   * the root list, else the target's page. Either is the page's route as its config wrote
   * it, with the tenant put in where the route names the parameter. Read only while a row
   * opens and the list is bound to a tenant.
   */
  private viewColumnRoute = computed(() => {
    const target = this.rowTarget();
    if (!target) {
      return '';
    }
    const ownPage = this.viewRouteFallback() && this.isRootList() === undefined && !this.config().rowRoute;
    return this.resolvePageRoute(ownPage ? this.viewRouteFallback() || '' : target.route);
  });

  /** Whether the digest left this page no column to ask for. */
  noListableColumns = computed(
    () =>
      this.listableFields() !== undefined &&
      (this.config().listColumns || []).length > 0 &&
      this.listColumns().length === 0,
  );

  /**
   * Whether the page the list holds comes after its first. Such a page with no row is not a
   * list that holds nothing: its rows went while the pages before it hold theirs, and the
   * pager is the way back to them.
   */
  afterFirstPage = computed(() => this.store.page().offset > 0 || this.store.page().hasPrev);

  /**
   * What the empty table says: the refusal when the digest leaves no column; in the page's
   * own words, that the person may not see the list, that it could not be loaded the way
   * it was asked for (a filter, a sort or a page size) or that it could not be loaded, when the
   * request was refused or failed; otherwise the page's own sentence for a list that holds
   * nothing (the config's `emptyMessage`), said only on the list's first page and while
   * neither a column filter nor the search box narrows the list, else the plain empty-list
   * text. A refusal must never read as an empty list, and what the server said of it is
   * written to the console, never to the screen.
   */
  emptyMessage = computed(() =>
    listEmptyMessage(
      this.store.pageError(),
      this.noListableColumns(),
      this.boxActive() || this.store.columnFilters().length > 0 || this.afterFirstPage()
        ? undefined
        : this.config().emptyMessage,
    ),
  );

  /**
   * What a list under a record says in place of its table when it holds nothing: the page's
   * own sentence (the config's emptyMessage), else "No hangar passes." from the word its rows
   * go by. Empty wherever the table is drawn: for the list a page is built on, before the first
   * answer, on a page after the first, for a refusal or a failed request, where the person may
   * see no column, and while a column filter or the search box narrows the list, whose table
   * then says what it always did.
   */
  emptyLine = computed(() => {
    const underRecord = this.isRootList() === false && !!this.config().parentRelation?.childKey;
    if (!underRecord || !this.store.firstPageAnswered() || this.store.page().rows.length > 0) {
      return '';
    }
    if (this.afterFirstPage() || this.store.pageError() !== undefined || this.noListableColumns()) {
      return '';
    }
    if (this.boxActive() || this.store.columnFilters().length > 0) {
      return '';
    }
    const many = this.rowWords()?.many.trim() ?? '';
    return this.config().emptyMessage || (many !== '' ? `No ${many}.` : noRecordsFound);
  });

  /**
   * The link the list draws under its rows (ListViewConfigOptions.more), asked with the row
   * the list is under: its words, the page it opens with the tenant put in as a row's link
   * has it, and its query parameters. Nothing where the page names none or it answers none.
   */
  moreLink = computed(() => {
    const link = this.config().more?.(this.relatedData());
    if (!link) {
      return undefined;
    }
    return { label: link.label, route: '/' + this.resolvePageRoute(link.route), queryParams: link.queryParams ?? {} };
  });

  /**
   * The digest's List state of each field of the page's resource, by field name: what
   * the server answers a computed list's sort, filter and search by (queryMeta,
   * searchRefused). The empty record across tenants, where no one digest speaks for the
   * rows, and with no resource.
   */
  private readonly listStates = computed((): Readonly<Record<string, PermissionDigestState>> => {
    const resource = this.config().primaryResource;
    if (this.across() || !resource) {
      return {};
    }
    return this.auth.fieldPermissionStates({ resource, permission: ListPermission });
  });

  /** The sorts the page wants: its config's until a header is clicked, then the person's choice. */
  private readonly wantedSorts = signal<FieldSort[]>([]);

  /**
   * The sorts the list is asked in (listSorts): the wanted sorts as the server answers them
   * for this caller. It follows the digest, so after the permissions are read again or under
   * another tenant the list asks no sort the server refuses there, and asks a wanted sort
   * again where the server answers it.
   */
  private readonly askedSorts = computed(
    (): FieldSort[] => {
      const resource = this.config().primaryResource;
      const descriptor = resource ? this.store.client.descriptor.resources[resource] : undefined;
      return listSorts(
        this.wantedSorts(),
        this.meta(),
        this.listStates(),
        descriptor,
        this.listableFields(),
        listableNames(this.meta(), this.namesOf, this.resourceMeta),
      );
    },
    { equal: sameSorts },
  );

  /**
   * The listed resource's metadata as this caller may sort and filter by it
   * (queryableMeta): on a computed resource, a column the digest lists as `conditional`
   * offers no sort and no filter. A field listed as conditionalOnTime is left as it is:
   * the server settles it from the time of the request. Across tenants nothing narrows.
   */
  queryMeta = computed(() => queryableMeta(this.meta(), this.listStates()));

  /**
   * Which columns the server filters, from the listed resource's metadata: the grid draws
   * a filter control only on these, and a companion-only column waits for an indexed filter.
   * The fields of the page's own filter are answered too, the key of the record a list is
   * under among them, so a column does not wait where the request already carries an
   * indexed filter the list shows no column for.
   */
  filterability = computed(() => filterEligibility(this.queryMeta(), this.columns(), this.configFilterFields()));

  /** The fields the page's own filter names, so the grid knows when an indexed filter is already in the request. */
  configFilterFields = computed(() => filterFields(this.filters()));

  /** Which text matches each column's field answers, from the listed resource's metadata: the grid's menu offers these. */
  searches = computed(() => searchEligibility(this.queryMeta(), this.columns()));

  /** Which control each column's menu takes a value from, from the listed resource's metadata: a date control on a date field, a date and a time on a timestamp. */
  filterInputs = computed(() => filterInputKinds(this.meta(), this.columns()));

  /**
   * The values each column filters by where they are known, from the listed resource's
   * metadata and, on a role column, the roles the list was found to hold beyond it, with
   * the values a column of the page's config names in their place (filterChoices): the
   * grid's menu offers their names.
   */
  filterChoices = computed(() => ({
    ...filterChoiceLists(this.meta(), this.columns(), this.foundRoles()),
    ...configuredFilterChoices(this.columns()),
  }));

  /**
   * The values a filter from the page's address may take on each column (takeAddress): the
   * choices the menu offers (filterChoices), less every column whose field holds a role's
   * name (`roles` in the metadata and no enumeration). A role made while the application
   * runs is in no metadata, so such a column's choices are an open set and any role an
   * address names stands there, as any value does on a column with no choices.
   */
  private readonly addressChoices = computed(() => {
    const roleFields = new Set(
      (this.meta()?.fields ?? [])
        .filter((field) => field.roles !== undefined && field.enumeration === undefined)
        .map((field) => field.fieldName),
    );
    return Object.fromEntries(Object.entries(this.filterChoices()).filter(([column]) => !roleFields.has(column)));
  });

  /**
   * The roles each role column's rows were found to hold beyond those the metadata lists,
   * by column id, in the order found, with what they were found under (the store's
   * besidePageScope): the route, the tenant and the page's own filter. They stand while
   * the list is on the page and those three stay the same, and are forgotten when one
   * changes.
   */
  private readonly rolesFound = signal<{ scope: string; byColumn: Readonly<Record<string, readonly string[]>> }>({
    scope: '',
    byColumn: {},
  });

  /** The roles found that stand under what the list reads now (rolesFound). */
  private readonly foundRoles = computed((): Readonly<Record<string, readonly string[]>> => {
    const held = this.rolesFound();
    return held.scope === this.store.besidePageScope() ? held.byColumn : {};
  });

  /** The role columns whose rows are being asked about, by what they are asked under and the column's id. */
  private readonly askingRoles = new Set<string>();

  /**
   * The search fields for which the server is sure to refuse this caller's whole search
   * of a computed list (refusedSearchFields): the fields the digest does not list and the
   * fields it lists as conditional. Across tenants no one digest speaks for the rows, so
   * nothing is refused here.
   */
  searchRefused = computed((): RefusedSearchFields => refusedSearchFields(this.meta(), this.listStates()));

  /**
   * The fields the search box searches for this caller: the listed resource's search
   * fields less those the digest denies for List, a branch judged by the child field's
   * grant on the child resource, a key field searched by whoever lists its resource. The
   * resource decides the set, never the page; a page that should search fewer fields
   * lists a view projecting fewer. None while the server would refuse the search
   * (searchRefused holds a field in either list), so the box is not offered.
   */
  searchFields = computed(() => {
    const refused = this.searchRefused();
    return refused.notSeen.length > 0 || refused.notAlwaysSeen.length > 0
      ? []
      : searchedFields(this.meta(), this.listableFields(), this.listableOf, this.resourceMeta);
  });

  /** Whether the box reaches a PII field, its own or a branch's: the request then travels in the body. */
  searchPII = computed(() => searchReachesPII(this.meta(), this.searchFields()));

  /** Whether the page draws the box: the resource has search fields for this caller and the config does not hide it. */
  showSearchBox = computed(() => this.config().showSearchBox !== false && this.searchFields().length > 0);

  /** The headers the page configured, by field, so the box names a field as its column does. */
  private columnHeaders = computed(
    () =>
      new Map(
        (this.config().listColumns ?? [])
          .filter((col) => col.header)
          .map((col): [string, string] => [col.id as string, col.header as string]),
      ),
  );

  /** What the empty box says: the fields it searches, by their labels. */
  searchPlaceholder = computed(() =>
    searchPlaceholder(this.searchFields().map((field) => searchFieldLabel(field, this.columnHeaders()))),
  );

  /** What the box says under itself: the search fields the caller's role leaves out, if any. */
  searchHint = computed(() =>
    searchDeniedHint(
      deniedSearchFields(this.meta(), this.listableFields(), this.listableOf, this.resourceMeta).map((field) =>
        searchFieldLabel(field, this.columnHeaders()),
      ),
    ),
  );

  /**
   * What the page says where it offers no box because the server would refuse the
   * search: the fields the caller's role does not see and the fields it does not always
   * see, by the labels the box gives them. Nothing when the config hides the box.
   */
  searchNotice = computed(() => {
    if (this.config().showSearchBox === false) {
      return '';
    }
    const label = (field: string): string => searchFieldLabel(field, this.columnHeaders());
    const refused = this.searchRefused();
    return searchRefusedNotice({
      notSeen: refused.notSeen.map(label),
      notAlwaysSeen: refused.notAlwaysSeen.map(label),
    });
  });

  /** Whether the box is in the request: it counts as the indexed touch a companion-only column waits for. */
  boxActive = computed(() => this.store.searchText().trim() !== '');

  /**
   * A column's filter menu opened. On a role column (roleQuestionField) the list asks its
   * own resource, beside the page, for a role its rows hold that the menu does not offer
   * yet: under the page's own filter and none of the person's (rowsBesidePage), with the
   * field holding none of the roles known (rolesQuestion), sorted by the field, one row and
   * the one column. A role answered joins the picks, which the open menu shows at once, and
   * the question is asked again with it among the known. It stops at an answer with no
   * row, at a row with no role in the field (a cell the person may not read), at a refusal
   * or a failure, which is written to the console and shown nowhere, and once
   * foundRolesLimit roles have been found. A later opening starts from what was found.
   * Any other column asks nothing.
   */
  onFilterMenuOpened(columnId: string): void {
    void this.askRoles(columnId);
  }

  private async askRoles(columnId: string): Promise<void> {
    const field = roleQuestionField(
      this.queryMeta(),
      this.columns().find((column) => column.id === columnId),
    );
    if (field?.roles === undefined) {
      return;
    }
    const scope = this.store.besidePageScope();
    const asking = JSON.stringify([scope, columnId]);
    if (this.askingRoles.has(asking)) {
      return;
    }
    this.askingRoles.add(asking);
    try {
      let found = this.foundRoles()[columnId] ?? [];
      while (found.length < foundRolesLimit) {
        const known = [...new Set([...field.roles, ...found])];
        let rows: RecordData[];
        try {
          rows = await this.store.rowsBesidePage({
            condition: rolesQuestion(field.fieldName, known),
            columns: [field.fieldName],
            sorts: [{ field: field.fieldName as FieldName, direction: 'asc' }],
            limit: 1,
          });
        } catch (error) {
          console.error(
            '%s: the roles the list holds in %s could not be read:',
            this.config().primaryResource,
            field.fieldName,
            error,
          );
          return;
        }
        const role: unknown = rows[0]?.[field.fieldName];
        if (typeof role !== 'string' || role === '' || known.includes(role) || this.store.besidePageScope() !== scope) {
          return;
        }
        found = [...found, role];
        const held = found;
        this.rolesFound.update((current) => ({
          scope,
          byColumn: { ...(current.scope === scope ? current.byColumn : {}), [columnId]: held },
        }));
      }
    } finally {
      this.askingRoles.delete(asking);
    }
  }

  /**
   * A header click: the sorts to request next, a press on a fixed set's column by the
   * names beside its field (requestSorts); the store drops the cursor and asks for a first
   * page.
   */
  onSortChange(sorts: SortRule[]): void {
    this.wantedSorts.set(requestSorts(this.listed(), sorts) as FieldSort[]);
    this.store.sorts.set(this.askedSorts());
  }

  /**
   * A filter menu commit: the column filters to request next. Once no indexed filter is
   * left in the request, the companion-only filters go with it, since the server would
   * refuse them alone.
   */
  onFilterChange(filters: ColumnFilter[]): void {
    this.store.columnFilters.set(
      withoutOrphanedCompanions(
        filters,
        this.filterability(),
        this.configFilterFields(),
        this.searches(),
        this.boxActive(),
      ),
    );
    this.filtersCommitted();
  }

  /**
   * The box's text committed: the request filter gains or loses its search conjunct, the
   * store drops the cursor and asks for a first page, and a cleared box takes the
   * companion-only filters it alone was carrying with it.
   */
  onSearchChange(text: string): void {
    this.store.searchText.set(text);
    this.store.columnFilters.set(
      withoutOrphanedCompanions(
        this.store.columnFilters(),
        this.filterability(),
        this.configFilterFields(),
        this.searches(),
        text.trim() !== '',
      ),
    );
    this.filtersCommitted();
  }

  /**
   * The person committed the column filters: a filter the address named and the list left
   * out is no longer spoken of, and the address follows the filters as they now stand.
   */
  private filtersCommitted(): void {
    this.filtersLeftOut.set(false);
    this.writeAddress();
  }

  /** The `filter` values of the page's address as it is now, one a filter. */
  private addressFilterValues(): string[] {
    return this.router.parseUrl(this.router.url).queryParamMap.getAll(filterParam);
  }

  /**
   * Takes the column filters from the page's address: on the page's opening, and after
   * each navigation while the list stands (Back and Forward, a link to the page it is on,
   * the menu entry of its own page). An address that names no filter opens the list on
   * the page's own filters (opensWith), unless it is the address the list wrote itself
   * (written), which says the person cleared every filter (takeOpening). An address that
   * already says the filters the list holds changes nothing, the notice of a filter left
   * out among it. Otherwise the list holds the filters of the address it can show the
   * person (standingFilters); where one was left out, it says so above its rows and writes
   * the address it does show.
   */
  private takeAddress(written = false): void {
    const values = this.addressFilterValues();
    const opening = values.length === 0 && !written ? this.openingFilters() : [];
    if (opening.length > 0) {
      this.takeOpening(opening);
      return;
    }
    const held = this.store.columnFilters();
    if (sameTexts(values, held.map(writeAddressFilter))) {
      return;
    }
    const read = values.map(readAddressFilter).filter((filter): filter is ColumnFilter => filter !== undefined);
    const standing = this.standing(read);
    if (!sameFilters(standing, held)) {
      this.store.columnFilters.set(standing);
    }
    this.filtersLeftOut.set(read.length !== values.length || !sameFilters(standing, read));
    if (!sameTexts(values, standing.map(writeAddressFilter))) {
      this.writeAddress();
    }
  }

  /**
   * Opens the list on the page's own filters (opensWith), those of them it can show the
   * person (standingFilters), and writes them into the address in place. One it leaves
   * out is left out silently: the notice of a filter left out is for an address a person
   * followed.
   */
  private takeOpening(opening: readonly ColumnFilter[]): void {
    const standing = this.standing(opening);
    if (!sameFilters(standing, this.store.columnFilters())) {
      this.store.columnFilters.set(standing);
    }
    this.filtersLeftOut.set(false);
    if (standing.length > 0) {
      this.writeAddress();
    }
  }

  /** Of the filters asked, those the list can show the person (standingFilters), in the order asked. */
  private standing(asked: readonly ColumnFilter[]): ColumnFilter[] {
    return standingFilters(asked, {
      columns: this.columns(),
      filterability: this.filterability(),
      externalFields: this.configFilterFields(),
      searches: this.searches(),
      inputs: this.filterInputs(),
      choices: this.addressChoices(),
      boxActive: this.boxActive(),
    });
  }

  /**
   * Writes the list's column filters into the page's address, in place, once no
   * navigation is in flight: starting one inside another would cancel it.
   */
  private writeAddress(): void {
    if (!this.ownsAddress()) {
      return;
    }
    if (this.router.currentNavigation() !== null) {
      this.addressWriteWaiting = true;
      return;
    }
    this.navigateToFilters();
  }

  /**
   * Replaces the page's address with one whose `filter` values are the list's column
   * filters as written, the parameter absent when there are none, every other query
   * parameter and the fragment kept, and marks the navigation as the list's own write
   * (addressWrittenState), so the end of it is not taken for an address that names no
   * filter. An address that already says them is left as it is.
   */
  private navigateToFilters(): void {
    const tree = this.router.parseUrl(this.router.url);
    const values = this.store.columnFilters().map(writeAddressFilter);
    if (sameTexts(tree.queryParamMap.getAll(filterParam), values)) {
      return;
    }
    const params = Object.fromEntries(Object.entries(tree.queryParams).filter(([name]) => name !== filterParam));
    if (values.length > 0) {
      params[filterParam] = values;
    }
    void this.router.navigateByUrl(new UrlTree(tree.root, params, tree.fragment), {
      replaceUrl: true,
      state: { [addressWrittenState]: true },
    });
  }

  /**
   * A navigation is over: a write of the address that waited for it is made now, from the
   * filters as they stand; where none waited, the address of one that ended is taken. A
   * navigation canceled because another superseded it ends nothing: that other is on its
   * way, and a write made now would cancel it, so the write waits for that one to be over.
   *
   * Taking an address can itself ask for a write (a filter it names is left out). The
   * router still holds the navigation that ended as its current one while it announces the
   * end, so that write is set waiting like any other; the navigation is over, and the write
   * is made here, not left for the end of the next one.
   *
   * A navigation the router skips, since it brings the address the page is at, is taken
   * too where the page opens with filters of its own: the page's own menu entry after the
   * person cleared every filter opens the list on them again.
   */
  private navigationOver(event: RouterEvent): void {
    if (event instanceof NavigationCancel && event.code === NavigationCancellationCode.SupersededByNewNavigation) {
      return;
    }
    if (!this.addressWriteWaiting && event instanceof NavigationEnd) {
      this.takeAddress(this.listWroteAddress());
    }
    if (!this.addressWriteWaiting && event instanceof NavigationSkipped && this.openingFilters().length > 0) {
      this.takeAddress();
    }
    if (this.addressWriteWaiting) {
      this.addressWriteWaiting = false;
      this.navigateToFilters();
    }
  }

  /**
   * Whether the navigation that just ended is the list's own write of the address
   * (navigateToFilters): one the code asked for with the list's mark, not Back or Forward,
   * which bring back the mark of the entry they return to.
   */
  private listWroteAddress(): boolean {
    const navigation = this.router.lastSuccessfulNavigation();
    return navigation?.trigger === 'imperative' && navigation.extras.state?.[addressWrittenState] === true;
  }

  /** A pager click: the store follows the server's relation. */
  onPageTurn(turn: PageTurn): void {
    this.store.turnPage(turn);
  }

  rootColumns = computed(() => {
    const idCols = [];
    const listColumns = this.listColumns();
    // Every key field rides along hidden unless the page shows it, so a row carries
    // the key an operation or a row route lifts.
    const shown = new Set<string>(listColumns.map((col) => col.id));
    for (const pk of this.primaryKeys()) {
      if (shown.has(pk.fieldName)) {
        continue;
      }
      idCols.push({
        id: pk.fieldName as FieldName,
        hidden: true,
      });
    }
    return [...new Set([...idCols, ...listColumns])];
  });

  /**
   * How the list draws each of its columns (listedColumns): on its field's stored value,
   * or, on a field that holds another row's id, by the name the server answers beside the
   * id, where the pointed-to resource declares one and the person may list it. Across
   * tenants the names a list carries point outside every tenant, and the whole
   * application's digest speaks for them.
   */
  private readonly listed = computed(() =>
    listedColumns(this.rootColumns(), this.meta(), this.namesOf, this.resourceMeta),
  );

  /**
   * The columns the list's request asks for (the store's): the columns the caller may list
   * and, for each column drawn by a name, a hidden column on the name field (askedNames),
   * so every row carries the name beside its id. Equal while the same fields, so the
   * store's request is not asked again for the same columns.
   */
  private readonly requestColumns = computed<ColumnConfig[]>(
    () => {
      const names = askedNames(this.listed());
      const columns = this.listColumns();
      return names.length === 0 ? columns : [...columns, ...names.map((id) => ({ id, hidden: true }))];
    },
    { equal: (a, b) => a.length === b.length && a.every((column, i) => column.id === b[i].id) },
  );

  /**
   * The sorts the table draws on its headings: a sort by the names beside a fixed set's
   * field stands on that field's column (tableSorts).
   */
  drawnSorts = computed(() => tableSorts(this.listed(), this.store.sorts()));

  columns = computed(() => {
    const columns: ColumnConfig[] = [];
    const usedIds = new Set<string>();
    // A column over a field the server does not order is drawn with no sort button, as one
    // that opts out is: the metadata says which, so no sort is offered that the server refuses.
    const unordered = unorderedFields(this.queryMeta());
    const rowOpens = this.rowOpens();

    if (this.across()) {
      // The tenant column of a list across tenants takes its id before any column of the page's.
      usedIds.add(TenantProperty);
    }

    for (const listed of this.listed()) {
      const col = listed.column;
      // A column drawn by the name beside its id is the table's column under the name
      // field's id, so a header click sorts by the name and its filter names it.
      const indexId = this.getUniqueId(listed.tableId, usedIds) as FieldName;
      usedIds.add(indexId);

      if (col.hidden) {
        continue;
      }

      // A cell without a configured format is written by the field: the name of the
      // value where the field takes one of a fixed set, a role's name in words where it
      // holds one, and otherwise by the display type: a size for bytes, the elements for
      // an array, a place in words or one-line JSON for an object, a calendar date for a
      // date, the value's text for the rest.
      // A yes-or-no value is not written at all: the table draws it as a mark
      // (processedRowData).
      const fieldMeta = this.meta()?.fields?.find((field) => field.fieldName === col.id);
      const valueFormatter = (params: DataType | null): string => {
        if (col.formatType) {
          const retValue = applyFormatting(col.formatType, params as string);
          return retValue || (col.emptyDataValue as string);
        }
        return formatFieldValue(fieldMeta, params) || (col.emptyDataValue as string);
      };
      // A column that says how its cell is read off the row (valueGetter) is read
      // that way, then formatted like any other. A name column, and a column which
      // shows no name (the list carries none beside its id, or the person may not list
      // it) but has the page's words for a tenant or a row, are read off the row by
      // cellText: the name beside the id, else the page's words, else the stored value.
      // The second says no stored value, so it offers neither sort nor filter.
      const named = listed.kind === 'name' || listed.kind === 'worded';
      const worded = listed.kind === 'worded';
      const shared = {
        id: indexId,
        header: listed.header ?? col.header,
        width: col.width,
        resizable: col.resizable,
        ...(named
          ? { valueGetter: (data: RecordData): string => cellText(listed, data, (key) => this.ownTenantName(key)) }
          : { valueFormatter }),
        // A configured format stays with the column, so the display row knows the
        // author chose how the cell is written.
        ...(col.formatType ? { formatType: col.formatType } : {}),
        ...(col.valueGetter ? { valueGetter: col.valueGetter } : {}),
        // A column that opts out of its filter control says so to the table, and so
        // does one that opts out of sorting.
        ...(col.filterable === false || worded ? { filterable: false } : {}),
        // A column whose list is drawn a line each says so to the table.
        ...(col.lines ? { lines: true } : {}),
        // A column that names the values it filters by keeps them, for its menu
        // (filterChoices).
        ...(col.filterChoices ? { filterChoices: col.filterChoices } : {}),
        ...(col.sortable === false || worded || unordered.has(col.id) ? { sortable: false } : {}),
        // A column of words that opens its row says so while a row opens, and its link
        // rides the display row (processedRowData); otherwise its cell is words alone.
        ...(col.opensRow && !col.iconConfig && rowOpens ? { opensRow: true } : {}),
      };
      // An icon column keeps its map and tooltip position for the grid; the cell itself is
      // resolved with the display row (processedRowData), never at render time.
      columns.push(
        col.iconConfig ? { ...shared, iconConfig: col.iconConfig, tooltipPosition: col.tooltipPosition } : shared,
      );
    }

    if (this.across()) {
      // Each row of a list across tenants says which tenant it is in, under the
      // application's word for a tenant: in the second column, after the column that names
      // the row. The first column is the one a list holds in place while the others scroll,
      // and it holds one, so the row's name stays and its tenant scrolls with the rest. The
      // tenant is not a field of the resource, so the server sorts by none: the heading is
      // words, with nothing to press and no room kept for a sort arrow.
      columns.splice(Math.min(1, columns.length), 0, {
        id: TenantProperty as FieldName,
        header: tenantWord(this.store.client.descriptor),
        sortable: false,
        filterable: false,
        valueFormatter: (tenant: DataType | null): string =>
          tenant ? this.store.client.permissions.domainName(tenant as Domain) : '',
      });
    }

    const target = this.rowTarget();
    // A row has one link: where a column drawn here opens its row, the list draws no View
    // column.
    if (target && rowOpens && !columns.some((col) => col.opensRow)) {
      // The root list's rows open on the page's own row route; any other list's, and a
      // rowRoute's, on the target's page (viewColumnRoute).
      columns.push({
        id: 'view' as FieldName,
        header: 'View',
        hideHeader: true,
        buttonConfig: this.across()
          ? // Each row opens inside its own tenant: the link rides the display row
            // (processedRowData), built from the target's route and the row's tenant.
            { label: 'View', icon: 'arrow_forward', linkField: acrossTenantsLinkKey, actionType: 'link' }
          : {
              label: 'View',
              icon: 'arrow_forward',
              viewRoute: this.viewColumnRoute(),
              keyField: target.keyField,
              actionType: 'link',
            },
      });
    }

    this.rowButtons().forEach((button, index) => {
      // A button of the page's own on every row, ahead of the row's menu: it is handed
      // the row as the server answered it, which rides the display row (processedRowData).
      columns.push({
        id: rowButtonKey(index) as FieldName,
        header: button.label,
        hideHeader: true,
        buttonConfig: {
          label: button.label,
          icon: button.icon,
          actionType: 'function',
          action: (displayed): void => {
            const answered = (displayed as Record<string, unknown>)[rowAnswerKey] as RecordData | undefined;
            if (answered) {
              button.run(answered, this.injector);
            }
          },
        },
      });
    });

    if (this.rowMethodNames().length > 0 || this.filledMethodNames().length > 0) {
      // The methods a row offers, in one menu at its end: the entries ride the display
      // row (processedRowData), so a row that offers none draws no button.
      columns.push({
        id: rowActionsKey as FieldName,
        header: 'Actions',
        hideHeader: true,
        buttonConfig: { label: 'Actions', icon: 'more_vert', actionType: 'menu' },
      });
    }

    if (this.deletesFromList()) {
      columns.push({
        id: 'delete' as FieldName,
        header: 'Delete',
        hideHeader: true,
        buttonConfig: {
          label: 'Delete',
          icon: 'delete',
          actionType: 'function',
          action: (row): void => {
            this.confirmDeleteRow(row as RecordData);
          },
        },
      });
    }

    return columns;
  });

  /**
   * Asks before a row leaves the list, then deletes it through the write resource. The
   * question names the row by its own name where the config reads one off the row
   * (`rowTitle`) and the list by its title, else by the word the resource's rows go by.
   * The dialog is handed the delete and runs it when the person says so, so a delete the
   * server refuses is answered in the dialog, in the server's words, and a row whose
   * delete takes records with it is asked about a second time, in the server's question.
   */
  confirmDeleteRow(row: RecordData): void {
    const config = this.config();
    const data: DeleteConfirmation = {
      name: config.rowTitle?.(row),
      from: config.title,
      word: this.rowWords()?.one,
      inList: true,
      delete: (options) => this.deleteRow(row, options),
    };
    this.dialog.open(DeleteResourceConfirmationModalComponent, { data, delayFocusTrap: false });
  }

  /**
   * Deletes one row through the write resource, lifting the key fields the row carries
   * under the table's names — the compound key an association view carries whole. The
   * list's page is read again once the row is gone. A delete the server refuses rejects
   * with the client's `ApiError`, for whoever asked to show, and so does the delete of a
   * row with records under it that its delete takes, until the options say it takes them
   * (`everythingUnder`).
   */
  deleteRow(row: RecordData, options?: RemoveOptions): Promise<void> {
    const handle = this.store.handle();
    const operation = handle.ops.remove(handle.keyOf(row), options);

    return this.store.apply([operation], rowMessage(this.rowWords(), 'deleted')).then(() => {
      this.store.reloadPage();
    });
  }

  /** Requests the page the table is on again, by its own cursor, so a write shows without losing the position. */
  reloadListData(): void {
    this.store.reloadPage();
  }

  /** The buttons every row carries, as the page names them (`rowButtons`); none on a list across tenants, which only reads rows into their tenants. */
  rowButtons = computed(() => (this.across() ? [] : (this.config().rowButtons ?? [])));

  /** The page's rows with each column's getter and formatter applied. */
  processedRowData = computed(() => {
    const data = this.store.page().rows;
    const columns = this.columns();
    const methods = this.rowMethodNames();
    const filled = this.filledMethodNames();
    const buttons = this.rowButtons().length > 0;

    const target = this.across() ? this.rowTarget() : undefined;
    const rowRoute = this.acrossRowRoute();
    const tenantParam = this.store.client.descriptor.domainRoute?.param;
    // The columns that open their row, each with the page a row opens on and the field a
    // row's key is read from, outside a list across tenants, whose rows carry their own link.
    const opening = columns.filter((col) => col.opensRow);
    const opens =
      opening.length > 0 && !this.across()
        ? { route: this.viewColumnRoute(), keyField: this.rowTarget()?.keyField ?? 'id' }
        : undefined;
    const roleFields = new Set(
      (this.meta()?.fields ?? []).filter((field) => field.roles !== undefined).map((field) => field.fieldName),
    );

    return data.map((row) => {
      const updatedRow = { ...row };
      if (target && rowRoute && tenantParam) {
        // The row's own link: the record's row route inside the tenant the row is in,
        // drawn where the person may read the record there, as the row route asks. The
        // tenant's digest answers; until it has loaded the row draws no link.
        const tenant = row[TenantProperty];
        const reads =
          typeof tenant === 'string' &&
          this.auth.hasPermission({ resource: target.resource, permission: ReadPermission, domain: tenant as Domain });
        updatedRow[acrossTenantsLinkKey] = reads
          ? tenantLink(rowRoute, tenantParam, tenant, row[target.keyField ?? 'id'])
          : undefined;
      }
      for (const col of columns) {
        if (col.valueGetter) {
          updatedRow[col.id] = col.valueGetter(row);
        } else {
          updatedRow[col.id] = row[col.id];
        }

        // A yes-or-no value stays what it is, so the table draws it as a check or a dash
        // and not as the word a formatter would make of it. A column that configures a
        // format is written by that format, as its author asked.
        const mark = typeof updatedRow[col.id] === 'boolean' && !col.formatType;
        // A list drawn a line each stays a list, each item written as text, so the
        // table draws the items one under another: a role's name in words where the
        // column's field holds one.
        const listed = col.lines && Array.isArray(updatedRow[col.id]);
        if (listed) {
          const roles = roleFields.has(col.id);
          updatedRow[col.id] = (updatedRow[col.id] as unknown[]).map((item) =>
            roles ? roleInWords(String(item ?? '')) : String(item ?? ''),
          );
        } else if (typeof col.valueFormatter === 'function' && !mark) {
          updatedRow[col.id] = col.valueFormatter(updatedRow[col.id]);
        }

        // An icon column's cell: the words under the column's key, the icon under its
        // sibling key, both resolved here from the raw row and the formatted text.
        if (col.iconConfig) {
          const cell = resolveIconCell(col.iconConfig, row, col.id, String(updatedRow[col.id] ?? ''), this.fieldText);
          updatedRow[col.id] = cell.words;
          updatedRow[iconCellKey(col.id)] = cell.icon;
        }
      }
      // A column that opens its row draws its words as the row's link: across tenants the
      // link into the row's own tenant, where the person may read the record there;
      // otherwise the page a row opens on and the row's key, and no link for a row with no
      // key.
      for (const col of opening) {
        const cell = updatedRow as Record<string, unknown>;
        if (opens === undefined) {
          cell[linkCellKey(col.id)] = cell[acrossTenantsLinkKey];
          continue;
        }
        const key = row[opens.keyField];
        cell[linkCellKey(col.id)] =
          key === undefined || key === null || key === '' ? undefined : `/${opens.route}/${String(key)}`;
      }
      if (methods.length > 0 || filled.length > 0) {
        // The methods this row offers, read off its capability answer, then the ones
        // the page fills from the row: the menu column's entries, each bound to the
        // raw row it runs against.
        (updatedRow as Record<string, unknown>)[rowActionsKey] = this.rowActions(row, methods, filled);
      }
      if (buttons) {
        // The row as the server answered it, for the page's own buttons: a column's
        // getter and formatter have written over the cells of the display row.
        (updatedRow as Record<string, unknown>)[rowAnswerKey] = row;
      }
      return updatedRow;
    });
  });

  /** The menu entries of one row: the methods it offers, each opening the method's dialog over the row. */
  private rowActions(row: RecordData, methods: readonly string[], filled: readonly string[]): RowAction[] {
    const presentations = this.methodPresentations();
    const offeredHere = inNamedOrder(
      [
        ...rowMethods(row, methods, this.store.methodMeta, presentations),
        ...filledMethods(row, this.config().primaryResource, presentations, this.store.methodMeta, (method) =>
          filled.includes(method),
        ),
      ],
      presentations,
    );
    // A method the page presents more than once may be offered twice on one row: each
    // entry after the first carries its position, so the menu tells them apart.
    return offeredHere.map((method, index) => {
      const id =
        offeredHere.findIndex((other) => other.method === method.method) === index
          ? method.method
          : `${method.method}:${index}`;
      return method.says
        ? // What the row says where the method would be: words, and nothing to run.
          {
            id,
            label: method.says,
            icon: method.icon,
            group: method.presentation?.group,
            run: (): void => undefined,
            disabled: true,
          }
        : {
            id,
            label: method.label,
            icon: method.icon,
            group: method.presentation?.group,
            run: (): void => this.runMethod(method, row),
          };
    });
  }

  /**
   * Runs a method through its dialog: against the row when one is given, its key filling
   * the request, or with no row for a method offered above the list. Once the method has
   * run the page says so and reads its rows again, by the page's own cursor. Where the
   * method took away the row the list sits under, the page may have left before the
   * dialog answers here (a live page reads its row again as soon as the write lands):
   * what ran is still said, and the list, which nobody sees any more, reads no rows and
   * tells no page.
   */
  runMethod(method: OfferedMethod, row?: RecordData): void {
    openMethodDialog(this.dialog, method, row, this.relatedData(), this.store.page().rows).subscribe((result) => {
      if (!result) {
        return;
      }
      const notice = methodDoneMessage(method, result.answer);
      this.store.notifications.addGlobalNotification({
        message: notice.message,
        type: notice.failed ? AlertType.ERROR : AlertType.SUCCESS,
        duration: 5000,
        link: '',
      });
      if (this.destroyRef.destroyed) {
        return;
      }
      this.store.reloadPage();
      this.methodRan.emit();
    });
  }

  /** The tenants the rows of a list across tenants are in, where the rows link to their own pages; none otherwise. */
  private linkTenants = computed<readonly string[]>(() => {
    if (!this.acrossRowRoute()) {
      return [];
    }
    const tenants = new Set<string>();
    for (const row of this.store.page().rows) {
      const tenant = row[TenantProperty];
      if (typeof tenant === 'string' && tenant !== '') {
        tenants.add(tenant);
      }
    }
    return [...tenants];
  });

  /**
   * Whether a link some row shows is still on its way: the digest of a tenant on the page
   * is not held and its read has not ended. A row's link is drawn where the tenant's digest
   * lets the person read the record, so until the digest answers nothing says whether the
   * row has one.
   */
  private linksAwaited = computed(() => {
    const ended = this.endedDigests();
    return this.linkTenants().some((tenant) => !this.auth.hasDigest(tenant as Domain) && !ended.has(tenant));
  });

  /**
   * Whether the links of some rows could not be worked out: the digest of a tenant on the
   * page could not be read, so nothing is known of what the person may open there. Those
   * rows draw no link, which is not a person who may read none of them, and the list says
   * so above its rows, with a way to try again. A tenant the server refuses is an answer:
   * its rows have no link, and nothing is said.
   */
  linksUnread = computed(() => this.linkTenants().some((tenant) => this.auth.digestUnread(tenant as Domain)));

  /** Whether the digests that could not be read are being read again: Try again waits for the answer. */
  linksRetrying = computed(() => this.linksUnread() && this.linksAwaited());

  /** retryLinks asks again for the digest of every tenant on the page whose read failed. */
  retryLinks(): void {
    for (const tenant of this.linkTenants()) {
      if (this.auth.digestUnread(tenant as Domain)) {
        void this.askDigest(tenant);
      }
    }
  }

  /**
   * Asks for one tenant's digest, for the links of its rows, and notes when the read has
   * ended. What it returns settles then, however the read ended, and never rejects.
   */
  private askDigest(tenant: string): Promise<void> {
    this.askedDigests.add(tenant);
    this.endedDigests.update((ended) => new Set([...ended].filter((asked) => asked !== tenant)));
    return new Promise<void>((resolve) => {
      const end = (): void => {
        this.endedDigests.update((ended) => new Set(ended).add(tenant));
        resolve();
      };
      this.auth.loadDigest(tenant as Domain).subscribe({ complete: end, error: end });
    });
  }

  /**
   * The rows the table draws: the page's rows, and on a list across tenants once the
   * digest of every tenant on the page has answered or failed. While a digest is being
   * read the table keeps the rows it had (none, on a list just opened), so a row is never
   * drawn without a link that is drawn a moment later.
   */
  tableRowData = linkedSignal<{ rows: RecordData[]; awaited: boolean }, RecordData[]>({
    source: () => ({ rows: this.processedRowData(), awaited: this.linksAwaited() }),
    computation: (now, previous) => (now.awaited ? (previous?.value ?? []) : now.rows),
  });

  loadingRowData = computed(() => this.store.pageStatus() === 'loading' || this.linksAwaited());

  filters = computed(() => {
    const configFilter = this.config().filter?.(this.relatedData()) || '';
    const inputFilter = this.filter();
    if (inputFilter && configFilter) {
      return `(${inputFilter}),(${configFilter})`;
    }
    if (inputFilter) {
      return `(${inputFilter})`;
    }
    return configFilter || '';
  });

  createResource(event: MouseEvent): void {
    event.stopPropagation();
    this.createRequested.emit();
  }

  parentKey = computed(() => {
    if (this.relatedData() === undefined || this.childKey() === undefined) {
      return '';
    }
    const parent = this.config().parentRelation?.parentKey;
    return this.relatedData()?.[parent] || '';
  });

  childKey = computed(() => {
    if (!this.config().parentRelation) {
      return '';
    }
    return this.config().parentRelation?.childKey as string;
  });

  /**
   * A tenant's name as the person's own list of tenants holds it, for a column over a
   * tenant's key whose row shows no name; undefined for a key the list does not name.
   */
  private ownTenantName(key: unknown): string | undefined {
    if (typeof key !== 'string' || key === '') {
      return undefined;
    }
    const name = this.store.client.permissions.domainName(key as Domain);
    return name !== '' && name !== key ? name : undefined;
  }

  /** Whether a refusal or failure is seen for the first time; from then on it is known. */
  private firstSeen(error: unknown): boolean {
    if (typeof error !== 'object' || error === null) {
      return true;
    }
    if (this.logged.has(error)) {
      return false;
    }
    this.logged.add(error);
    return true;
  }

  private getUniqueId(baseId: string, usedIds: Set<string>): string {
    if (!usedIds.has(baseId)) {
      return baseId;
    }

    let counter = 1;
    let newId = `${baseId}_${counter}`;
    while (usedIds.has(newId)) {
      counter++;
      newId = `${baseId}_${counter}`;
    }
    return newId;
  }

  /**
   * Reads the digest of every tenant the rows are in that has not been asked for, each
   * once, where a row of a list across tenants links to its own page: the tenant's digest
   * says whether the person may read the record there, and so whether the row draws its
   * link. The page waits for them, so a row and its link are drawn together. A digest that
   * could not be read does not fail the page: its rows draw no link, the list says so
   * above them (linksUnread), and it is asked again only when the person asks (retryLinks).
   */
  private async readTenantDigests(rows: readonly RecordData[]): Promise<void> {
    if (!this.acrossRowRoute()) {
      return;
    }
    const tenants = new Set<string>();
    for (const row of rows) {
      const tenant = row[TenantProperty];
      if (
        typeof tenant === 'string' &&
        tenant !== '' &&
        !this.askedDigests.has(tenant) &&
        !this.auth.hasDigest(tenant as Domain)
      ) {
        tenants.add(tenant);
      }
    }
    await Promise.all(Array.from(tenants, (tenant) => this.askDigest(tenant)));
  }

  ngOnInit(): void {
    // A key-less resource's page is the whole list, identified by position: a page size
    // or a row expansion asked of it is a configuration error, raised here naming the
    // resource, before anything is requested.
    refuseKeylessConfig(this.config().primaryResource, this.config(), this.meta());
    // A column over a write-only field would be a 400 from the server and an empty cell
    // otherwise: a configuration error, raised here naming the resource and the field.
    refuseWriteOnlyColumns(this.config().primaryResource, this.config().listColumns ?? [], this.meta());
    // A column on a name field names nothing a grant lists: the column belongs on the id
    // beside it, which the refusal names.
    refuseNameColumns(this.config().primaryResource, this.config().listColumns ?? [], this.meta());
    // A list across tenants needs the resource declared for it, and reads alone: a
    // page asking for what it cannot do is refused here, naming the resource.
    refuseAcrossTenantsConfig(this.config().primaryResource, this.config(), this.store.client);
    this.store.acrossTenants.set(this.across());
    if (this.meta()) {
      // The store reads the listed resource and writes the write resource.
      this.store.resourceName.set(this.writeResourceName());
      this.store.resourceMeta.set(this.meta());
      this.store.listColumns.set(this.requestColumns());
      this.store.pageCapabilities.set(
        this.rowMethodNames().length > 0 || this.offeredMethodNames().length > 0 ? ['Execute'] : [],
      );
      this.wantedSorts.set(this.config().sorts || []);
      this.store.sorts.set(this.askedSorts());
      this.store.pageSize.set(this.config().pageSize);
    }

    this.store.filter.set(this.filters());
    this.store.disableCacheForFilterPii.set(this.config().disableCacheForFilterPii);
    // The list a page is built on opens with the filters its address names, so its first
    // request carries them, and follows the address while it stands.
    if (this.ownsAddress()) {
      this.router.events.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((event) => {
        if (
          event instanceof NavigationEnd ||
          event instanceof NavigationCancel ||
          event instanceof NavigationError ||
          event instanceof NavigationSkipped
        ) {
          this.navigationOver(event);
        }
      });
      this.takeAddress();
    }

    runInInjectionContext(this.injector, () => {
      effect(() => {
        // A row of a list across tenants links to its own page where its tenant's digest
        // lets the person read it: the digests of the tenants on a page are read before
        // the page is the list's (readTenantDigests), each once, and here for a tenant
        // whose rows come to link after their page landed. A digest that could not be
        // read is not asked again by a later page: nothing is known of the tenant, its
        // rows stay without a link, and the list says the links could not be loaded, with
        // a way to try again.
        for (const tenant of this.linkTenants()) {
          if (this.askedDigests.has(tenant) || this.auth.hasDigest(tenant as Domain)) {
            continue;
          }
          untracked(() => void this.askDigest(tenant));
        }
      });

      effect(() => {
        // What the server said of a list it refused, or how the read failed, is for
        // whoever looks into it: it is written to the console, once, with the resource it
        // belongs to. The screen says it in the page's words.
        // Each line's words are a constant, and the console puts the names where they keep
        // a place (%s): a name is never itself read as words to print.
        const resource = this.config().primaryResource;
        const pageError: unknown = this.store.pageError();
        if (pageError && this.firstSeen(pageError)) {
          console.error('%s: the list could not be loaded:', resource, pageError);
        }
      });

      // The store asks what the list is asked in now: the sorts follow the digest (askedSorts).
      effect(() => {
        this.store.sorts.set(this.askedSorts());
      });

      effect(() => {
        this.filter();
        this.relatedData();
        const columns = this.requestColumns();
        this.store.filter.set(this.filters());
        this.store.disableCacheForFilterPii.set(this.config().disableCacheForFilterPii);
        this.store.searchPII.set(this.searchPII());
        this.store.liveChoice.set(this.config().live);
        // A list whose rows offer methods asks for each row's Execute answer.
        this.store.pageCapabilities.set(
          this.rowMethodNames().length > 0 || this.offeredMethodNames().length > 0 ? ['Execute'] : [],
        );
        this.store.listColumns.set(columns);
        // Nothing the digest grants is on this page: there is nothing to ask for, and
        // the table says so instead of provoking the refusal it already predicts.
        this.store.listSuspended.set(this.noListableColumns());
      });

      // A page across tenants is the list's once the digests its rows' links are drawn
      // from have been read.
      this.store.pageNeeds = (page): Promise<void> => this.readTenantDigests(page.rows);
      // From here the store holds the server's page: a change to the filter, the columns,
      // the sorts, or the column filters asks for a first page; the pager turns it.
      this.store.buildStorePage();
    });
  }
}

/** Whether two lists of texts are the same texts in the same order. */
function sameTexts(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((text, i) => text === b[i]);
}
