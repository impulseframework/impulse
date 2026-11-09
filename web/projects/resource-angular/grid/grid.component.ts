import { CommonModule } from '@angular/common';
import {
  afterRenderEffect,
  Component,
  computed,
  effect,
  ElementRef,
  input,
  output,
  signal,
  TemplateRef,
  viewChild,
} from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatMenuModule } from '@angular/material/menu';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterModule } from '@angular/router';
import { CamelCaseToTitlePipe } from '@impulseframework/resource-angular/camel-case-to-title';
import { ColumnConfig, iconCellKey, RecordData } from '@impulseframework/resource-angular/types';
import {
  dateTimeParts,
  filterControlFor,
  filterInputFor,
  fitsInput,
  indexedFilterActive,
  isComplete,
  joinDateTime,
  operatorOption,
  operatorsFor,
} from './grid-filter.util';
import {
  ColumnFilter,
  ColumnFilterability,
  ColumnFilterInputs,
  ColumnSearches,
  FilterControl,
  FilterInput,
  FilterOperator,
  FilterOperatorOption,
  GridPageState,
  PageTurn,
  RowKey,
  SortRule,
  VirtualScrollConfig,
} from './grid-types';
import { VirtualScrollState } from './grid-virtual-scroll';
import { TableButtonComponent } from './table-button/table-button.component';

const MIN_COLUMN_WIDTH = 48;
const ACTION_COLUMN_WIDTH = 66;

/** What a companion-only column's disabled filter control says. */
export const COMPANION_FILTER_HINT =
  'Filter on an indexed column first; the server filters this column only beside one.';

/**
 * The grid renders one page of rows and emits intents. The rows are the page the data
 * source holds; a header click emits the next sorts, a filter menu emits the next column
 * filters, and the pager emits a turn — the source asks the server and hands back the
 * page. Nothing here filters, sorts, or slices rows in the browser: one paging model, the
 * server's. A filter control is drawn only on a column the generated metadata says the
 * server filters (`filterability`), with a companion-only column waiting until an indexed
 * filter is in the request, and its menu offers the text matches the column's field
 * answers (`searches`), which stand alone; the page's search box (`boxActive`) counts as
 * the indexed filter. A row is identified by `rowKey`, the caller's function from a
 * row to its identity (a list page passes the row's key fields joined), or by its position
 * in the page when the caller passes none: the track expression, the selection, and the
 * expansion are keyed by it, and nothing here assumes a field called `id`. Selection is
 * held as rows, so it spans pages. Virtual scroll renders the one page it is given; row
 * expansion is as before.
 */
@Component({
  selector: 'impulse-grid',
  standalone: true,
  imports: [
    CommonModule,
    TableButtonComponent,
    CamelCaseToTitlePipe,
    RouterModule,
    MatButtonModule,
    MatIconModule,
    MatTooltipModule,
    MatMenuModule,
    MatFormFieldModule,
    MatInputModule,
    MatProgressSpinnerModule,
  ],
  templateUrl: './grid.component.html',
  styleUrl: './grid.component.scss',
})
export class AppGridComponent {
  /* eslint-disable  @typescript-eslint/no-explicit-any */
  /** The page of rows to render, as the data source holds it. */
  rowData = input<any[]>([]);
  columnDefs = input<ColumnConfig[]>([]);
  // Detail rows aren't accounted for in the virtual scroll row-height model, so expansion
  // is disabled while virtual scroll is active to avoid spacer math drifting out of sync.
  enableRowExpansion = input<boolean>(false);
  detailTemplate = input<TemplateRef<unknown>>();
  selectionType = input<'multiple' | 'single' | 'none'>('none');
  loading = input<boolean>(false);
  /** What the table says when it has no rows: the caller decides whether that is an empty list or a refusal. */
  emptyMessage = input<string>('No records found');
  enableVirtualScroll = input<boolean>(false);
  virtualScrollConfig = input<VirtualScrollConfig>({});
  /** The sorts the rows were requested in, the first primary. Drawn on the headers; a click emits the next set. */
  sorts = input<SortRule[]>([]);
  /** The column filters the rows were requested with. Drawn on the headers; a menu commit emits the next set. */
  filters = input<ColumnFilter[]>([]);
  /** Which columns the server filters, by column id, from the generated field metadata. A column absent here draws no filter control. */
  filterability = input<ColumnFilterability>({});
  /** The fields the page's own filter names outside the grid, so a companion-only column knows an indexed filter is already in the request. */
  externalFilterFields = input<string[]>([]);
  /** Which text matches each column's field answers, by column id, from the generated field metadata's `search`. A column absent here offers none. */
  searches = input<ColumnSearches>({});
  /** Which control each column's menu takes a value from, by column id, from the generated field metadata's display type: a date control on a `civildate` column, a date and a time on a `date` column. A column absent here takes typed text. */
  filterInputs = input<ColumnFilterInputs>({});
  /** Whether the page's search box is in the request: it counts as the indexed filter a companion-only column waits for. */
  boxActive = input<boolean>(false);
  /** Where the rows sit in the server's list. The pager is drawn only when given. */
  page = input<GridPageState | undefined>(undefined);
  /**
   * A row's identity: what the track expression, the selection, and the expansion are
   * keyed by. Given none, a row is identified by its position in the page, so a page
   * whose rows carry no key (a key-less resource served whole) still selects and
   * expands one row at a time.
   */
  rowKey = input<RowKey | undefined>(undefined);

  /** The selected rows, across pages. */
  selectedRows = output<RecordData[]>();
  /** The sorts to request next, after a header click. */
  sortChange = output<SortRule[]>();
  /** The column filters to request next, after a filter menu commits or clears. */
  filterChange = output<ColumnFilter[]>();
  /** The page to show next. */
  pageTurn = output<PageTurn>();

  readonly companionHint = COMPANION_FILTER_HINT;

  /** The selected rows by their identity, so a selection survives the page's rows being replaced. */
  private readonly selected = signal<Map<unknown, RecordData>>(new Map());
  private readonly expandedIds = signal<Set<unknown>>(new Set());
  /** Each row of the page to its identity, by `rowKey` or by position, computed once per page. */
  private readonly rowKeys = computed<Map<RecordData, unknown>>(() => {
    const key = this.rowKey();
    return new Map(this.rowData().map((row: RecordData, index: number) => [row, key ? key(row, index) : index]));
  });
  private readonly columnWidths = signal<Record<string, number>>({});
  /** The filter each open menu is editing, before it commits; keyed by column id. */
  private readonly drafts = signal<Record<string, ColumnFilter>>({});

  totalColumnCount = computed(
    () => this.columnDefs().length + (this.selectionType() !== 'none' ? 1 : 0) + (this.enableRowExpansion() ? 1 : 0),
  );

  /** Whether the request already carries a filter the server indexes, so companion columns may join. */
  indexedFilterActive = computed(() =>
    indexedFilterActive(this.filters(), this.filterability(), this.externalFilterFields(), this.searches(), this.boxActive()),
  );

  private readonly scrollContainer = viewChild<ElementRef<HTMLDivElement>>('scrollContainer');
  private readonly tableBody = viewChild<ElementRef<HTMLTableSectionElement>>('tableBody');

  private readonly virtualScroll = new VirtualScrollState(
    computed(() => this.rowData().length),
    this.virtualScrollConfig,
  );

  visibleRows = computed(() => {
    if (!this.enableVirtualScroll()) {
      return this.rowData();
    }
    const { start, end } = this.virtualScroll.range();
    return this.rowData().slice(start, end);
  });

  virtualTopPadding = computed(() => (this.enableVirtualScroll() ? this.virtualScroll.topPadding() : 0));
  virtualBottomPadding = computed(() => (this.enableVirtualScroll() ? this.virtualScroll.bottomPadding() : 0));

  /** The pager's text: the rows this page spans, of the total when the first page asked for it. */
  pageLabel = computed(() => {
    const page = this.page();
    if (!page) {
      return '';
    }
    const count = this.rowData().length;
    const range = count === 0 ? '0' : `${page.offset + 1}–${page.offset + count}`;
    return page.total === undefined ? range : `${range} of ${page.total}`;
  });

  constructor() {
    // Falls back to a measured rowHeight from the initial probe batch (INITIAL_PROBE_ROW_COUNT)
    afterRenderEffect(() => {
      if (!this.enableVirtualScroll() || this.virtualScroll.rowHeight() !== undefined) {
        return;
      }
      const rows = this.visibleRows();
      const body = this.tableBody()?.nativeElement;
      if (!body || !rows.length) {
        return;
      }
      const sampleRows = Array.from(body.querySelectorAll<HTMLTableRowElement>('tr.impulse-row'));
      if (!sampleRows.length) {
        return;
      }
      const average = sampleRows.reduce((sum, row) => sum + row.getBoundingClientRect().height, 0) / sampleRows.length;
      this.virtualScroll.measureRowHeight(average);
    });

    afterRenderEffect((onCleanup) => {
      if (!this.enableVirtualScroll()) {
        return;
      }
      const container = this.scrollContainer()?.nativeElement;
      if (!container) {
        return;
      }

      this.virtualScroll.setViewportHeight(container.clientHeight);
      // An environment with no ResizeObserver (a spec under jsdom) keeps the one
      // measurement; the browser follows the container as it resizes.
      if (typeof ResizeObserver === 'undefined') {
        return;
      }
      const observer = new ResizeObserver((entries) => {
        const height = entries[0]?.contentRect.height;
        if (height !== undefined) {
          this.virtualScroll.setViewportHeight(height);
        }
      });
      observer.observe(container);
      onCleanup(() => observer.disconnect());
    });

    effect(() => {
      if (this.enableVirtualScroll()) {
        this.expandedIds.set(new Set());
      }
    });
  }

  onScroll(event: Event): void {
    this.virtualScroll.setScrollTop((event.target as HTMLElement).scrollTop);
  }

  /** The row's identity, as `rowKey` answers it or its position in the page. */
  keyOf(row: RecordData): unknown {
    return this.rowKeys().get(row);
  }

  // Selection: held as rows keyed by their identity, so it spans the pages the source turns.

  allSelected = computed(() => {
    const rows = this.rowData();
    return rows.length > 0 && rows.every((row: RecordData) => this.selected().has(this.keyOf(row)));
  });

  someSelected = computed(
    () => !this.allSelected() && this.rowData().some((row: RecordData) => this.selected().has(this.keyOf(row))),
  );

  isSelected(row: RecordData): boolean {
    return this.selected().has(this.keyOf(row));
  }

  toggleRow(row: RecordData): void {
    const mode = this.selectionType();
    if (mode === 'none') {
      return;
    }

    const id = this.keyOf(row);
    const current = new Map(this.selected());
    if (mode === 'single') {
      const wasSelected = current.has(id);
      current.clear();
      if (!wasSelected) {
        current.set(id, row);
      }
    } else if (current.has(id)) {
      current.delete(id);
    } else {
      current.set(id, row);
    }

    this.selected.set(current);
    this.emitSelectedRows();
  }

  /** Selects or clears every row of this page; rows selected on other pages stay. */
  toggleSelectAll(): void {
    const rows = this.rowData();
    const current = new Map(this.selected());
    if (this.allSelected()) {
      rows.forEach((row: RecordData) => current.delete(this.keyOf(row)));
    } else {
      rows.forEach((row: RecordData) => current.set(this.keyOf(row), row));
    }
    this.selected.set(current);
    this.emitSelectedRows();
  }

  private emitSelectedRows(): void {
    this.selectedRows.emit([...this.selected().values()]);
  }

  isExpanded(row: RecordData): boolean {
    return this.expandedIds().has(this.keyOf(row));
  }

  toggleExpand(row: RecordData): void {
    if (this.enableVirtualScroll()) {
      return;
    }
    const id = this.keyOf(row);
    const current = new Set(this.expandedIds());
    if (current.has(id)) {
      current.delete(id);
    } else {
      current.add(id);
    }
    this.expandedIds.set(current);
  }

  // Sorting: the headers draw the sorts the rows came in; a click emits the next set.

  sortInfo(col: ColumnConfig): { direction: 'asc' | 'desc'; priority: number } | null {
    const sorts = this.sorts();
    const index = sorts.findIndex((sort) => sort.field === col.id);
    return index === -1 ? null : { direction: sorts[index].direction, priority: index + 1 };
  }

  hasMultipleSorts(): boolean {
    return this.sorts().length > 1;
  }

  /** A click makes the column the sort (asc, then desc, then none); shift-click adds it as a secondary. */
  toggleSort(col: ColumnConfig, event: MouseEvent): void {
    const field = col.id;
    const current = this.sorts();

    if (!event.shiftKey) {
      const isSoleSort = current.length === 1 && current[0].field === field;
      if (!isSoleSort) {
        this.sortChange.emit([{ field, direction: 'asc' }]);
      } else if (current[0].direction === 'asc') {
        this.sortChange.emit([{ field, direction: 'desc' }]);
      } else {
        this.sortChange.emit([]);
      }
      return;
    }

    const existingIndex = current.findIndex((sort) => sort.field === field);
    if (existingIndex === -1) {
      this.sortChange.emit([...current, { field, direction: 'asc' }]);
    } else if (current[existingIndex].direction === 'asc') {
      const next = [...current];
      next[existingIndex] = { field, direction: 'desc' };
      this.sortChange.emit(next);
    } else {
      this.sortChange.emit(current.filter((_, index) => index !== existingIndex));
    }
  }

  // Filtering: the menu edits a draft and emits the next filters when it commits.

  filterControl(col: ColumnConfig): FilterControl {
    return filterControlFor(col, this.filterability(), this.indexedFilterActive(), this.searches());
  }

  /** The operators the column's menu offers: the comparisons the server filters it with now, and the matches its field lists. */
  operatorsFor(col: ColumnConfig): FilterOperatorOption[] {
    return operatorsFor(col.id, this.filterability(), this.indexedFilterActive(), this.searches());
  }

  private committedFilter(col: ColumnConfig): ColumnFilter | undefined {
    return this.filters().find((filter) => filter.field === col.id);
  }

  /** The filter the column's menu shows: the draft being edited, else the committed filter, else an empty one on the first operator offered. */
  draftFor(col: ColumnConfig): ColumnFilter {
    return (
      this.drafts()[col.id] ??
      this.committedFilter(col) ?? { field: col.id, operator: this.operatorsFor(col)[0]?.value ?? 'eq', value: '' }
    );
  }

  hasFilter(col: ColumnConfig): boolean {
    return this.committedFilter(col) !== undefined;
  }

  operatorTakesValue(col: ColumnConfig): boolean {
    return operatorOption(this.draftFor(col).operator).takesValue;
  }

  /** The control the column's menu takes a value from under the draft's operator: the column's own for one value, typed text for a list. */
  valueInput(col: ColumnConfig): FilterInput {
    return filterInputFor(col.id, this.filterInputs(), this.draftFor(col).operator);
  }

  /** What the text box says when empty: a list's shape, in the forms the server reads a date in on a date or timestamp column. */
  valuePlaceholder(col: ColumnConfig): string {
    if (!operatorOption(this.draftFor(col).operator).takesList) {
      return 'Value';
    }
    return this.filterInputs()[col.id] === undefined ? 'a, b, c' : '2026-12-15, 12/2026';
  }

  /** The day a date control shows: the draft's value up to its time of day. */
  dateOf(col: ColumnConfig): string {
    return dateTimeParts(this.draftFor(col).value).date;
  }

  /** The time of day a time control shows: the draft's value after its day, empty for a whole day. */
  timeOf(col: ColumnConfig): string {
    return dateTimeParts(this.draftFor(col).value).time;
  }

  /**
   * A new operator keeps the value where its control can show it and starts over where it
   * cannot (a typed list under a date control). It stays a draft, as a value does: the menu
   * commits on Apply, Enter, or closing, so an operator and a value changed together are
   * one request, and a null test chosen alone commits as the menu closes.
   */
  setFilterOperator(col: ColumnConfig, event: Event): void {
    const operator = (event.target as HTMLSelectElement).value as FilterOperator;
    const current = this.draftFor(col);
    const value = fitsInput(current.value, filterInputFor(col.id, this.filterInputs(), operator)) ? current.value : '';
    this.drafts.set({ ...this.drafts(), [col.id]: { ...current, operator, value } });
  }

  setFilterValue(col: ColumnConfig, event: Event): void {
    this.setDraftValue(col, (event.target as HTMLInputElement).value);
  }

  /** The day from a date control, kept with the time of day already given. */
  setFilterDate(col: ColumnConfig, event: Event): void {
    this.setDraftValue(col, joinDateTime((event.target as HTMLInputElement).value, this.timeOf(col)));
  }

  /** The time of day from a time control, kept with the day already given; a time without a day is nothing yet. */
  setFilterTime(col: ColumnConfig, event: Event): void {
    this.setDraftValue(col, joinDateTime(this.dateOf(col), (event.target as HTMLInputElement).value));
  }

  private setDraftValue(col: ColumnConfig, value: string): void {
    this.drafts.set({ ...this.drafts(), [col.id]: { ...this.draftFor(col), value } });
  }

  /** Emits the filters with this column's draft in place of its committed filter; an emptied draft clears it. */
  commitFilter(col: ColumnConfig): void {
    const draft = this.draftFor(col);
    const others = this.filters().filter((filter) => filter.field !== col.id);
    const next = isComplete(draft) ? [...others, draft] : others;
    this.dropDraft(col);
    if (!isComplete(draft) && !this.hasFilter(col)) {
      return;
    }
    this.filterChange.emit(next);
  }

  clearFilter(col: ColumnConfig): void {
    this.dropDraft(col);
    if (!this.hasFilter(col)) {
      return;
    }
    this.filterChange.emit(this.filters().filter((filter) => filter.field !== col.id));
  }

  /**
   * Typing in the filter menu stays in its controls: the menu would otherwise read a letter
   * as an item shortcut. Escape passes through, so it closes the menu as anywhere else, and
   * the close commits a complete draft.
   */
  keepTyping(event: KeyboardEvent): void {
    if (event.key !== 'Escape') {
      event.stopPropagation();
    }
  }

  /** A menu that closes with a complete draft commits it; an incomplete draft is dropped and the committed filter stays. */
  onFilterMenuClosed(col: ColumnConfig): void {
    const draft = this.drafts()[col.id];
    if (draft === undefined) {
      return;
    }
    if (!isComplete(draft)) {
      this.dropDraft(col);
      return;
    }
    this.commitFilter(col);
  }

  private dropDraft(col: ColumnConfig): void {
    if (!(col.id in this.drafts())) {
      return;
    }
    const next = { ...this.drafts() };
    delete next[col.id];
    this.drafts.set(next);
  }

  /** The display-row key the list wrote an icon column's icon under; the column's own key holds the words. */
  iconKeyOf(col: ColumnConfig): string {
    return iconCellKey(col.id);
  }

  widthFor(col: ColumnConfig): number | null {
    const defaultWidth = col.buttonConfig ? ACTION_COLUMN_WIDTH : null;
    return this.columnWidths()[col.id] ?? col.width ?? defaultWidth;
  }

  startResize(event: MouseEvent, col: ColumnConfig): void {
    event.preventDefault();
    event.stopPropagation();

    const header = (event.currentTarget as HTMLElement).parentElement;
    const startWidth = this.widthFor(col) ?? header?.getBoundingClientRect().width ?? 120;
    const startX = event.clientX;

    const onMove = (moveEvent: MouseEvent): void => {
      const nextWidth = Math.max(MIN_COLUMN_WIDTH, startWidth + (moveEvent.clientX - startX));
      this.columnWidths.set({ ...this.columnWidths(), [col.id]: nextWidth });
    };
    const onUp = (): void => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    };

    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  }

  turn(direction: PageTurn): void {
    this.pageTurn.emit(direction);
  }
}
