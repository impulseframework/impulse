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
  ResourceRef,
  runInInjectionContext,
  signal,
  Type,
} from '@angular/core';
import { ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatExpansionModule } from '@angular/material/expansion';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { MatDialog } from '@angular/material/dialog';
import { ActivatedRoute, Router, RouterModule } from '@angular/router';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { CamelCaseToTitlePipe } from '@impulseframework/resource-angular/camel-case-to-title';
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
import { Domain, TenantProperty } from '@impulseframework/resource';
import { firstValueFrom } from 'rxjs';
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
  ListViewConfig,
  ReadPermission,
  RecordData,
  Resource,
  RESOURCE_META,
  RootConfig,
  RowAction,
  rowActionsKey,
  rowAnswerKey,
  rowButtonKey,
  rowRouteTarget,
  writeResource,
} from '@impulseframework/resource-angular/types';
import {
  ActionAccessControlWrapperComponent,
  ActionButtonContext,
} from '../actions/action-button-smart/action-access-control-wrapper.component';
import {
  hyphenConcatWithoutResource,
  hyphenSpaceConcatWithoutResource,
  noSpaceConcatWithoutResource,
  spaceConcatWithoutResource,
  spaceHyphenConcatWithoutResource,
} from '../concat-fns';
import { applyFormatting, formatByDisplayType, formatDateString } from '../format-fns';
import { iconTemplateFields, resolveIconCell } from './icon-cell';
import {
  DeleteConfirmation,
  DeleteResourceConfirmationModalComponent,
} from '../delete-resource-confirmation-modal/delete-resource-confirmation-modal.component';
import { methodDoneMessage, openMethodDialog } from '../method-dialog/open-method-dialog';
import { filledMethods, offeredAs, OfferedMethod, parentOffers, rowMethods } from '../row-methods/row-methods';
import { ResourceStore } from '../resource-store.service';
import { SearchBoxComponent } from '../search-box/search-box.component';
import {
  deniedSearchFields,
  searchDeniedHint,
  listableFieldsOf,
  searchedFields,
  searchFieldLabel,
  searchPlaceholder,
  searchReachesPII,
} from '../search-box/search-fields';
import {
  filterEligibility,
  filterInputKinds,
  listEmptyMessage,
  refuseKeylessConfig,
  refuseWriteOnlyColumns,
  relatedUnreadMessage,
  searchEligibility,
  unorderedFields,
} from './list-request';
import { listableColumns, sameColumns } from './listable-columns';
import { acrossTenantsLinkKey, refuseAcrossTenantsConfig, tenantLink, tenantWord } from './across-tenants';

/** A column's heading where the page gives none: its field's name in words, as the table draws it. */
const headingTitle = new CamelCaseToTitlePipe();

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
   * The related reads the server refused or that failed, each by its key with what the
   * read ended in. A read asked again stays here while it is on its way: a page turn, a
   * sort or a filter brings other rows, so other keys to read, and the read that could not
   * be made a moment ago is the same read, most often with the same answer. It leaves when
   * it answers. A read the server refused is not asked again for the same keys while the
   * list stands (the store's reader keeps the refusal): it comes back here as the same
   * error, so nothing more is written of it.
   */
  private unanswered = linkedSignal<{ refKey: string; status: string; error: unknown }[], Map<string, unknown>>({
    source: () =>
      [...this.resourceRefMap()].map(([refKey, ref]) => ({
        refKey,
        status: ref.status(),
        error: ref.status() === 'error' ? ref.error() : undefined,
      })),
    computation: (reads, previous) => {
      const unanswered = new Map<string, unknown>();
      for (const read of reads) {
        if (read.status === 'error') {
          unanswered.set(read.refKey, read.error);
        } else if ((read.status === 'loading' || read.status === 'reloading') && previous?.value.has(read.refKey)) {
          unanswered.set(read.refKey, previous.value.get(read.refKey));
        }
      }
      return unanswered;
    },
  });

  /**
   * What the list could not read of the records its columns name: a sentence for each
   * related read the server refused or that failed, naming the shown columns it fills by
   * their headings. Such a read is over, so the list is drawn with the rows it has, and
   * its sentence stands above them, standing still while the read is asked again, until
   * the read answers. Two reads that come to the same sentence (two columns under one
   * heading, each naming another record) say it once.
   */
  unread = computed<string[]>(() => {
    const sentences = new Set<string>();
    for (const [refKey, error] of this.unanswered()) {
      const headings = this.headingsOver(refKey);
      if (headings.length > 0) {
        sentences.add(relatedUnreadMessage(error, headings));
      }
    }
    return [...sentences];
  });

  /** The refusals and failures already written to the console, so each is written once. */
  private logged = new WeakSet<object>();

  indentTitle = computed(() => {
    if (this.config() === undefined) {
      return false;
    }

    return true;
  });

  createButtonLabel = computed(() => {
    return this.config().createButtonLabel || 'Create';
  });

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
   * The methods the page fills from a row and that name no target row (`fill` on a
   * presentation among `rowMethods`), each while the digest grants the caller Execute
   * on it: offered on every row, since the server answers nothing for a row the method
   * does not name.
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
  resourceRefMap = signal(new Map<string, ResourceRef<RecordData[]>>());
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
   * The configured columns the caller may list: every column when nothing narrows,
   * otherwise those whose fields the digest grants. Key fields are structural, never
   * grant-bearing, and always pass; a concatenated column passes when its own field and
   * every field it reads off this resource pass (fields of a referenced resource are
   * read through that resource's own request). Equal while the same columns: every digest
   * load re-evaluates this, and one that changes the answer for none of them (another
   * scope's digest landing after sign-in, a reload) leaves the columns as they are, so the
   * store's request, which they are part of, is not asked again.
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

  /** One of the row's fields as a sentence writes it: by the field's display type, a null as nothing. */
  private fieldText = (field: string, value: DataType | null): string => {
    const displayType = this.meta()?.fields?.find((f) => f.fieldName === field)?.displayType;
    return formatByDisplayType(displayType, value);
  };

  /** Whether the rows carry the field their route lifts: no rowRoute, or one the list requests. */
  rowRouteRequested = computed(() => {
    const rowRoute = this.config().rowRoute;
    return !rowRoute || this.listColumns().some((col) => col.id === rowRoute);
  });

  /**
   * Whether the caller may open a row: the digest's Read answer for the resource a row
   * opens (granted or conditional). Without it the view column is not drawn, since the
   * row page would only be refused; the row route carries the same gate.
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

  /** Whether the digest left this page no column to ask for. */
  noListableColumns = computed(
    () =>
      this.listableFields() !== undefined &&
      (this.config().listColumns || []).length > 0 &&
      this.listColumns().length === 0,
  );

  /**
   * What the empty table says: the refusal when the digest leaves no column; in the page's
   * own words, that the person may not see the list, that it could not be loaded the way
   * it was asked for (a filter, a sort or a page size) or that it could not be loaded, when the
   * request was refused or failed; otherwise the page's own sentence for a list that holds
   * nothing (the config's `emptyMessage`), said only while neither a column filter nor the
   * search box narrows the list, else the plain empty-list text. A refusal must never read
   * as an empty list, and what the server said of it is written to the console, never to
   * the screen.
   */
  emptyMessage = computed(() =>
    listEmptyMessage(
      this.store.pageError(),
      this.noListableColumns(),
      this.boxActive() || this.store.columnFilters().length > 0 ? undefined : this.config().emptyMessage,
    ),
  );

  /**
   * Which columns the server filters, from the listed resource's metadata: the grid draws
   * a filter control only on these, and a companion-only column waits for an indexed filter.
   */
  filterability = computed(() => filterEligibility(this.meta(), this.columns()));

  /** The fields the page's own filter names, so the grid knows when an indexed filter is already in the request. */
  configFilterFields = computed(() => filterFields(this.filters()));

  /** Which text matches each column's field answers, from the listed resource's metadata: the grid's menu offers these. */
  searches = computed(() => searchEligibility(this.meta(), this.columns()));

  /** Which control each column's menu takes a value from, from the listed resource's metadata: a date control on a date field, a date and a time on a timestamp. */
  filterInputs = computed(() => filterInputKinds(this.meta(), this.columns()));

  /**
   * The fields the search box searches for this caller: the listed resource's search
   * fields less those the digest denies for List, a branch judged by the child field's
   * grant on the child resource. The resource decides the set, never the page; a page
   * that should search fewer fields lists a view projecting fewer.
   */
  searchFields = computed(() => searchedFields(this.meta(), this.listableFields(), this.listableOf));

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
      deniedSearchFields(this.meta(), this.listableFields(), this.listableOf).map((field) =>
        searchFieldLabel(field, this.columnHeaders()),
      ),
    ),
  );

  /** Whether the box is in the request: it counts as the indexed touch a companion-only column waits for. */
  boxActive = computed(() => this.store.searchText().trim() !== '');

  /** A header click: the sorts to request next; the store drops the cursor and asks for a first page. */
  onSortChange(sorts: SortRule[]): void {
    this.store.sorts.set(sorts as FieldSort[]);
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
    for (const col of listColumns) {
      if (!('additionalIds' in col)) continue;
      for (const additionalCol of col.additionalIds) {
        if (additionalCol.resource !== undefined) continue;
        idCols.push({
          id: additionalCol.id as FieldName,
          hidden: true,
        });
      }
    }

    return [...new Set([...idCols, ...listColumns])];
  });
  columns = computed(() => {
    const refmap = this.resourceRefMap();
    const cols = this.rootColumns();
    const columns: ColumnConfig[] = [];
    const usedIds = new Set<string>();
    // A column over a field the server does not order is drawn with no sort button, as one
    // that opts out is: the metadata says which, so no sort is offered that the server refuses.
    const unordered = unorderedFields(this.meta());

    if (this.across()) {
      // The tenant column of a list across tenants takes its id before any column of the page's.
      usedIds.add(TenantProperty);
    }

    for (const col of cols) {
      const indexId = this.getUniqueId(col.id, usedIds) as FieldName;
      usedIds.add(indexId);

      if (col.hidden) {
        continue;
      }

      if ('additionalIds' in col) {
        columns.push({
          id: indexId,
          header: col.header,
          width: col.width,
          resizable: col.resizable,
          // A column that opts out of its filter control says so to the table, and so
          // does one that opts out of sorting.
          ...(col.filterable === false ? { filterable: false } : {}),
          ...(col.sortable === false || unordered.has(col.id) ? { sortable: false } : {}),
          valueGetter: (data) => {
            const concatArray: string[] = [];

            col.additionalIds.forEach((additionalCol) => {
              if (!additionalCol.resource) {
                concatArray.push(data[additionalCol.id]);
              } else {
                const refKey = this.resourceRefKey(additionalCol.resource, additionalCol.id as FieldName);
                const resourceRef = refmap.get(refKey);
                // A related read the server refused, or one that failed, holds no rows (reading
                // its value would throw): the cell says what the page gives for a row whose
                // record is not read, and the list says above its rows what it could not read.
                const resData = resourceRef?.hasValue() ? resourceRef.value() : undefined;
                if (!resData) {
                  return;
                }
                for (const res of resData) {
                  if (res[additionalCol.id] === data[col.id] && additionalCol.field) {
                    const value = res[additionalCol.field];
                    if (col.formatType && typeof value === 'string') {
                      concatArray.push(formatDateString(col.formatType, value));
                    } else if (value) {
                      concatArray.push(value as string);
                    }
                  }
                }
              }
            });

            if (concatArray.length === 0) {
              return col.fallback ? col.fallback(data) : (col.emptyDataValue as string);
            }

            switch (col.concatFn) {
              case 'space-concat':
                return spaceConcatWithoutResource(concatArray);
              case 'space-hyphen-concat':
                return spaceHyphenConcatWithoutResource(concatArray);
              case 'hyphen-space-concat':
                return hyphenSpaceConcatWithoutResource(concatArray);
              case 'no-space-concat':
                return noSpaceConcatWithoutResource(concatArray);
              default: {
                // default case is hyphen-concat
                return hyphenConcatWithoutResource(concatArray);
              }
            }
          },
        });
      } else {
        // A cell without a configured format is written by the field's display type: a
        // size for bytes, the elements for an array, one-line JSON for an object, a
        // calendar date for a date, the value's text for the rest. A yes-or-no value is
        // not written at all: the table draws it as a mark (processedRowData).
        const displayType = this.meta()?.fields?.find((field) => field.fieldName === col.id)?.displayType;
        const valueFormatter = (params: DataType | null): string => {
          if (col.formatType) {
            const retValue = applyFormatting(col.formatType, params as string);
            return retValue || (col.emptyDataValue as string);
          }
          return formatByDisplayType(displayType, params) || (col.emptyDataValue as string);
        };
        // A column that says how its cell is read off the row (valueGetter) is read
        // that way, then formatted like any other.
        const shared = {
          id: indexId,
          header: col.header,
          width: col.width,
          resizable: col.resizable,
          valueFormatter,
          // A configured format stays with the column, so the display row knows the
          // author chose how the cell is written.
          ...(col.formatType ? { formatType: col.formatType } : {}),
          ...(col.valueGetter ? { valueGetter: col.valueGetter } : {}),
          // A column that opts out of its filter control says so to the table, and so
          // does one that opts out of sorting.
          ...(col.filterable === false ? { filterable: false } : {}),
          // A column whose list is drawn a line each says so to the table.
          ...(col.lines ? { lines: true } : {}),
          ...(col.sortable === false || unordered.has(col.id) ? { sortable: false } : {}),
        };
        // An icon column keeps its map and tooltip position for the grid; the cell itself is
        // resolved with the display row (processedRowData), never at render time.
        columns.push(
          col.iconConfig ? { ...shared, iconConfig: col.iconConfig, tooltipPosition: col.tooltipPosition } : shared,
        );
      }
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
    if (this.config().showViewButton && target && this.rowRouteRequested() && this.canView()) {
      // The root list's rows open on the page's own row route; any other list's, and a
      // rowRoute's, on the target's page. Either route is the page's as its config wrote
      // it, so the tenant goes in where the route names the parameter.
      const ownPage = this.viewRouteFallback() && this.isRootList() === undefined && !this.config().rowRoute;
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
              viewRoute: this.resolvePageRoute(ownPage ? this.viewRouteFallback() || '' : target.route),
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
   * (`rowTitle`), and the list by its title. The dialog is handed the delete and runs it
   * when the person says so, so a delete the server refuses is answered in the dialog, in
   * the server's words.
   */
  confirmDeleteRow(row: RecordData): void {
    const config = this.config();
    const data: DeleteConfirmation = {
      name: config.rowTitle?.(row),
      from: config.title,
      inList: true,
      delete: () => this.deleteRow(row),
    };
    this.dialog.open(DeleteResourceConfirmationModalComponent, { data, delayFocusTrap: false });
  }

  /**
   * Deletes one row through the write resource, lifting the key fields the row carries
   * under the table's names — the compound key an association view carries whole. The
   * list's page is read again once the row is gone. A delete the server refuses rejects
   * with the client's `ApiError`, for whoever asked to show.
   */
  deleteRow(row: RecordData): Promise<void> {
    const handle = this.store.handle();
    const operation = handle.ops.remove(handle.keyOf(row));

    return this.store.apply([operation], `${this.store.resourceName()} deleted successfully`).then(() => {
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
        // table draws the items one under another.
        const listed = col.lines && Array.isArray(updatedRow[col.id]);
        if (listed) {
          updatedRow[col.id] = (updatedRow[col.id] as unknown[]).map((item) => String(item ?? ''));
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
    const offeredHere = [
      ...rowMethods(row, methods, this.store.methodMeta, presentations),
      ...filledMethods(row, presentations, this.store.methodMeta, (method) => filled.includes(method)),
    ];
    // A method the page presents more than once may be offered twice on one row: each
    // entry after the first carries its position, so the menu tells them apart.
    return offeredHere.map((method, index) => {
      const id =
        offeredHere.findIndex((other) => other.method === method.method) === index
          ? method.method
          : `${method.method}:${index}`;
      return method.says
        ? // What the row says where the method would be: words, and nothing to run.
          { id, label: method.says, icon: method.icon, run: (): void => undefined, disabled: true }
        : { id, label: method.label, icon: method.icon, run: (): void => this.runMethod(method, row) };
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

  loadingRowData = computed(() => {
    if (this.store.pageStatus() === 'loading') {
      return true;
    }
    for (const ref of this.resourceRefMap().values()) {
      const refStatus = ref.status();
      if (refStatus === 'loading' || refStatus === 'reloading') {
        return true;
      }
    }
    return false;
  });

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

  private resourceRefKey(resource: Resource, keyField: FieldName): string {
    return `${resource}::${keyField}`;
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

  /** The headings, as the table draws them, of the shown columns that name a record of the related read under the key. */
  private headingsOver(refKey: string): string[] {
    const headings: string[] = [];
    for (const col of this.listColumns()) {
      if (col.hidden || !('additionalIds' in col)) {
        continue;
      }
      const reads = col.additionalIds.some(
        (additional) =>
          additional.resource !== undefined &&
          this.resourceRefKey(additional.resource, additional.id as FieldName) === refKey,
      );
      if (reads) {
        headings.push(headingTitle.transform(col.header || col.id));
      }
    }
    return headings;
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
   * could not be read is kept as one that grants nothing, and its rows draw no link.
   */
  private async readTenantDigests(rows: readonly RecordData[]): Promise<void> {
    if (!this.acrossRowRoute()) {
      return;
    }
    const tenants = new Set<Domain>();
    for (const row of rows) {
      const tenant = row[TenantProperty];
      if (typeof tenant === 'string' && tenant !== '' && !this.auth.hasDigest(tenant as Domain)) {
        tenants.add(tenant as Domain);
      }
    }
    await Promise.all(Array.from(tenants, (tenant) => firstValueFrom(this.auth.loadDigest(tenant))));
  }

  ngOnInit(): void {
    // A key-less resource's page is the whole list, identified by position: a page size
    // or a row expansion asked of it is a configuration error, raised here naming the
    // resource, before anything is requested.
    refuseKeylessConfig(this.config().primaryResource, this.config(), this.meta());
    // A column over a write-only field would be a 400 from the server and an empty cell
    // otherwise: a configuration error, raised here naming the resource and the field.
    refuseWriteOnlyColumns(this.config().primaryResource, this.config().listColumns ?? [], this.meta());
    // A list across tenants needs the resource declared for it, and reads alone: a
    // page asking for what it cannot do is refused here, naming the resource.
    refuseAcrossTenantsConfig(this.config().primaryResource, this.config(), this.store.client);
    this.store.acrossTenants.set(this.across());
    if (this.meta()) {
      // The store reads the listed resource and writes the write resource.
      this.store.resourceName.set(this.writeResourceName());
      this.store.resourceMeta.set(this.meta());
      this.store.listColumns.set(this.listColumns());
      this.store.pageCapabilities.set(this.rowMethodNames().length > 0 ? ['Execute'] : []);
      this.store.sorts.set(this.config().sorts || []);
      this.store.pageSize.set(this.config().pageSize);
    }

    this.store.filter.set(this.filters());
    this.store.disableCacheForFilterPii.set(this.config().disableCacheForFilterPii);

    runInInjectionContext(this.injector, () => {
      const referenced = new Map<
        string,
        { route: string; keyField: FieldName; fkColumns: Set<FieldName>; columns: Set<FieldName> }
      >();
      this.config().listColumns.forEach((element) => {
        if (!('additionalIds' in element)) {
          return;
        }
        element.additionalIds.forEach((additionalCol) => {
          if (additionalCol.resource === undefined) {
            return;
          }
          const meta = this.resourceMeta(additionalCol.resource);
          if (meta === undefined) {
            return;
          }
          const keyField = additionalCol.id as FieldName;
          const refKey = this.resourceRefKey(additionalCol.resource, keyField);
          const entry = referenced.get(refKey) ?? {
            route: meta.route,
            keyField,
            fkColumns: new Set<FieldName>(),
            columns: new Set<FieldName>(),
          };
          entry.fkColumns.add(element.id as FieldName);
          entry.columns.add(keyField);
          if (additionalCol.field) {
            entry.columns.add(additionalCol.field as FieldName);
          }
          referenced.set(refKey, entry);
        });
      });

      referenced.forEach((entry, refKey) => {
        if (this.resourceRefMap().has(refKey)) {
          return;
        }
        const keys = computed(() => {
          const data = this.store.page().rows;
          const values = new Set<string>();
          for (const row of data) {
            for (const fkColumn of entry.fkColumns) {
              const value = row[fkColumn];
              if (value !== undefined && value !== null && value !== '') {
                values.add(String(value));
              }
            }
          }
          return [...values];
        });
        const ref = this.store.resourceListByKeys(
          signal(entry.route),
          signal(entry.keyField),
          keys,
          signal([...entry.columns]),
        );
        this.resourceRefMap.update((map) => new Map(map).set(refKey, ref));
      });

      effect(() => {
        // What the server said of a list or of a related read it refused, or how the read
        // failed, is for whoever looks into it: it is written to the console, once, with
        // the resource and the read it belongs to. The screen says it in the page's words.
        // Each line's words are a constant, and the console puts the names where they keep
        // a place (%s): a name is never itself read as words to print.
        const resource = this.config().primaryResource;
        const pageError: unknown = this.store.pageError();
        if (pageError && this.firstSeen(pageError)) {
          console.error('%s: the list could not be loaded:', resource, pageError);
        }
        for (const [refKey, error] of this.unanswered()) {
          if (this.firstSeen(error)) {
            console.error('%s: the records read for %s could not be loaded:', resource, refKey, error);
          }
        }
      });

      effect(() => {
        this.filter();
        this.relatedData();
        const columns = this.listColumns();
        this.store.filter.set(this.filters());
        this.store.disableCacheForFilterPii.set(this.config().disableCacheForFilterPii);
        this.store.searchPII.set(this.searchPII());
        this.store.liveChoice.set(this.config().live);
        // A list whose rows offer methods asks for each row's Execute answer.
        this.store.pageCapabilities.set(this.rowMethodNames().length > 0 ? ['Execute'] : []);
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
