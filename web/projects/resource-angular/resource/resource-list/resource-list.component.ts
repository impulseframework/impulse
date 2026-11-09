import { NgComponentOutlet } from '@angular/common';
import {
  Component,
  computed,
  effect,
  inject,
  Injector,
  input,
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
import {
  AppGridComponent,
  ColumnFilter,
  filterFields,
  PageTurn,
  RowKey,
  SortRule,
  withoutOrphanedCompanions,
} from '@impulseframework/resource-angular/grid';
import { pageRouteResolver, resourcePageRoute } from '@impulseframework/resource-angular/resource-nav';
import {
  ChildResourceConfig,
  ColumnConfig,
  DataType,
  DeletePermission,
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
import { DeleteResourceConfirmationModalComponent } from '../delete-resource-confirmation-modal/delete-resource-confirmation-modal.component';
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
  searchEligibility,
} from './list-request';
import { listableColumns, sameColumns } from './listable-columns';

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

  hideCreateButton = input<boolean>(true);
  createMode = output<boolean>();
  resourceConfig = input<ChildResourceConfig>();
  viewRoute = input<string>();
  filter = input<string>('');
  linkCreateType = input<boolean>(false);
  isRootList = input<boolean>(true);

  showCreateButton = computed(() => {
    const config = this.config();
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

  listTitle = computed(() => {
    if (this.config()?.collapsible) return '';

    const title = this.config().title || '';
    if (!title) return '';

    if (!this.config().showRowCount) return title;
    // The server's total, answered with the first page and kept while turning.
    const total = this.store.page().total;
    return total !== undefined && total > 0 ? `${title} (${total})` : title;
  });

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
  rowTarget = computed(() => rowRouteTarget(this.config().primaryResource, this.config().rowRoute, this.resourceMeta, resourcePageRoute));
  /** Puts the page's tenant into a registered route a row link is built from, where the route names the tenant parameter. */
  private readonly resolvePageRoute = pageRouteResolver();
  /**
   * Whether rows are deleted from the list: the write resource's key is compound, so a
   * row has no page of its own to delete from, and the digest grants Delete on it. A
   * single-key row is deleted from its page, as before.
   */
  deletesFromList = computed(() => {
    const meta = this.writeMeta();
    if (!meta || meta.deleteDisabled || keyFields(meta).length < 2) {
      return false;
    }
    return this.auth.hasPermission({ resource: this.writeResourceName(), permission: DeletePermission });
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
    return this.auth.hasPermission({ resource: target.resource, permission: ReadPermission });
  });

  /** Whether the digest left this page no column to ask for. */
  noListableColumns = computed(
    () =>
      this.listableFields() !== undefined && (this.config().listColumns || []).length > 0 && this.listColumns().length === 0,
  );

  /**
   * What the empty table says: the refusal when the digest leaves no column, the server's
   * own message when the request was refused (the list, a filter, or a page size) or
   * failed, otherwise the plain empty-list text. A refusal must never read as an empty list.
   */
  emptyMessage = computed(() => listEmptyMessage(this.store.pageError(), this.noListableColumns()));

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
      withoutOrphanedCompanions(filters, this.filterability(), this.configFilterFields(), this.searches(), this.boxActive()),
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
          valueGetter: (data) => {
            const concatArray: string[] = [];

            col.additionalIds.forEach((additionalCol) => {
              if (!additionalCol.resource) {
                concatArray.push(data[additionalCol.id]);
              } else {
                const refKey = this.resourceRefKey(additionalCol.resource, additionalCol.id as FieldName);
                const resourceRef = refmap.get(refKey);
                const resData = resourceRef?.value();
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
              return col.emptyDataValue as string;
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
        // calendar date for a date, the value's text for the rest.
        const displayType = this.meta()?.fields?.find((field) => field.fieldName === col.id)?.displayType;
        const valueFormatter = (params: DataType | null): string => {
          if (col.formatType) {
            const retValue = applyFormatting(col.formatType, params as string);
            return retValue || (col.emptyDataValue as string);
          }
          return formatByDisplayType(displayType, params) || (col.emptyDataValue as string);
        };
        const shared = { id: indexId, header: col.header, width: col.width, resizable: col.resizable, valueFormatter };
        // An icon column keeps its map and tooltip position for the grid; the cell itself is
        // resolved with the display row (processedRowData), never at render time.
        columns.push(
          col.iconConfig ? { ...shared, iconConfig: col.iconConfig, tooltipPosition: col.tooltipPosition } : shared,
        );
      }
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
        buttonConfig: {
          label: 'View',
          icon: 'arrow_forward',
          viewRoute: this.resolvePageRoute(ownPage ? this.viewRouteFallback() || '' : target.route),
          keyField: target.keyField,
          actionType: 'link',
        },
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

  /** Asks before a row leaves the list, then deletes it through the write resource. */
  confirmDeleteRow(row: RecordData): void {
    const dialogRef = this.dialog.open(DeleteResourceConfirmationModalComponent, { delayFocusTrap: false });
    dialogRef.afterClosed().subscribe((confirmed) => {
      if (confirmed === true) {
        this.deleteRow(row);
      }
    });
  }

  /**
   * Deletes one row through the write resource, lifting the key fields the row carries
   * under the table's names — the compound key an association view carries whole.
   */
  deleteRow(row: RecordData): void {
    const handle = this.store.handle();
    const operation = handle.ops.remove(handle.keyOf(row));
    void this.store.apply([operation], `${this.store.resourceName()} deleted successfully`).then(() => {
      this.store.reloadPage();
    });
  }

  /** Requests the page the table is on again, by its own cursor, so a write shows without losing the position. */
  reloadListData(): void {
    this.store.reloadPage();
  }

  /** The page's rows with each column's getter and formatter applied. */
  processedRowData = computed(() => {
    const data = this.store.page().rows;
    const columns = this.columns();

    return data.map((row) => {
      const updatedRow = { ...row };
      for (const col of columns) {
        if (col.valueGetter) {
          updatedRow[col.id] = col.valueGetter(row);
        } else {
          updatedRow[col.id] = row[col.id];
        }

        if (typeof col.valueFormatter === 'function') {
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
      return updatedRow;
    });
  });

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
    this.createMode.emit(true);
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

  ngOnInit(): void {
    // A key-less resource's page is the whole list, identified by position: a page size
    // or a row expansion asked of it is a configuration error, raised here naming the
    // resource, before anything is requested.
    refuseKeylessConfig(this.config().primaryResource, this.config(), this.meta());
    // A column over a write-only field would be a 400 from the server and an empty cell
    // otherwise: a configuration error, raised here naming the resource and the field.
    refuseWriteOnlyColumns(this.config().primaryResource, this.config().listColumns ?? [], this.meta());
    if (this.meta()) {
      // The store reads the listed resource and writes the write resource.
      this.store.resourceName.set(this.writeResourceName());
      this.store.resourceMeta.set(this.meta());
      this.store.listColumns.set(this.listColumns());
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
        this.filter();
        this.relatedData();
        const columns = this.listColumns();
        this.store.filter.set(this.filters());
        this.store.disableCacheForFilterPii.set(this.config().disableCacheForFilterPii);
        this.store.searchPII.set(this.searchPII());
        this.store.liveChoice.set(this.config().live);
        this.store.listColumns.set(columns);
        // Nothing the digest grants is on this page: there is nothing to ask for, and
        // the table says so instead of provoking the refusal it already predicts.
        this.store.listSuspended.set(this.noListableColumns());
      });

      // From here the store holds the server's page: a change to the filter, the columns,
      // the sorts, or the column filters asks for a first page; the pager turns it.
      this.store.buildStorePage();
    });
  }
}
