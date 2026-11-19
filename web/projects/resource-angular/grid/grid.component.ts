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
import {
  ColumnConfig,
  iconCellKey,
  RecordData,
  RowAction,
  rowActionsKey,
} from '@impulseframework/resource-angular/types';
import { CutShortTooltipDirective } from './cut-short-tooltip.directive';
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

/** The narrowest a column goes: the least a person can drag one to. */
const MIN_COLUMN_WIDTH = 48;
const ACTION_COLUMN_WIDTH = 66;
const SELECT_COLUMN_WIDTH = 48;
const EXPAND_COLUMN_WIDTH = 40;

/**
 * The narrowest a column of text is drawn in any list, in pixels, where the column
 * declares no width of its own and nobody has dragged it: 135 pixels of text between the
 * cell's padding and its line, nineteen characters of the list's 14 pixel type, which is
 * a person's first and last name. It is also the widest that still shows one more column
 * whole beside the held first one on a phone 390 pixels wide, on a page and in a pop-up,
 * in a list with no select or expand column.
 */
export const MIN_TEXT_COLUMN_WIDTH = 160;

/**
 * Whether a color, as the browser reports the one an element is drawn with, covers what is
 * under it: any color but a clear or part-clear one.
 */
function coversWhatIsUnder(color: string): boolean {
  if (color === '' || color === 'transparent') {
    return false;
  }
  const alpha = /^rgba\(.*,\s*([\d.]+)\s*\)$/.exec(color) ?? /\/\s*([\d.]+)(%?)\s*\)$/.exec(color);
  if (alpha === null) {
    return true;
  }
  return parseFloat(alpha[1]) >= (alpha[2] === '%' ? 100 : 1);
}

/** What a companion-only column's disabled filter control says. */
export const COMPANION_FILTER_HINT =
  'Filter on an indexed column first; the server filters this column only beside one.';

/**
 * The grid renders one page of rows and emits intents. The rows are the page the data
 * source holds; a header click emits the next sorts, a filter menu emits the next column
 * filters, and the pager emits a turn — the source asks the server and hands back the
 * page. Nothing here filters, sorts, or slices rows in the browser: one paging model, the
 * server's; a table told its rows have one order (`sortable` false) draws its headers as
 * words, and so does one column that opts out (`sortable: false` on the column). A filter control is drawn only on a column the generated metadata says the
 * server filters (`filterability`), with a companion-only column waiting until an indexed
 * filter is in the request, and its menu offers the text matches the column's field
 * answers (`searches`), which stand alone; the page's search box (`boxActive`) counts as
 * the indexed filter. A row is identified by `rowKey`, the caller's function from a
 * row to its identity (a list page passes the row's key fields joined), or by its position
 * in the page when the caller passes none: the track expression, the selection, and the
 * expansion are keyed by it, and nothing here assumes a field called `id`. Selection is
 * held as rows, so it spans pages. Virtual scroll renders the one page it is given; row
 * expansion is as before. A cell is one line, and a text or a header wider than its column
 * is cut with an ellipsis and offered whole in a tooltip (CutShortTooltipDirective).
 *
 * A column is never squeezed under the narrowest it is drawn (`narrowestFor`): the table
 * fills its frame while every column fits, and where they do not it stays as wide as they
 * need and the frame scrolls sideways, the page never. The first column is then held at
 * the left edge, with the select and expand columns, while the others scroll under it.
 */
@Component({
  selector: 'impulse-grid',
  standalone: true,
  imports: [
    CommonModule,
    TableButtonComponent,
    CutShortTooltipDirective,
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
  /**
   * Whether a header is a button that asks for the next sort. False for rows that come in
   * the one order they have, a history newest first: the headers are then words, with
   * nothing to press that would do nothing.
   */
  sortable = input<boolean>(true);
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
  /** How far the frame was scrolled sideways when it last scrolled. */
  private lastScrollLeft = 0;
  /** Whether the pointer was just pressed on the list: the focus that follows a press is the pointer's, not the keyboard's. */
  private pressed = false;
  /** The filter each open menu is editing, before it commits; keyed by column id. */
  private readonly drafts = signal<Record<string, ColumnFilter>>({});

  totalColumnCount = computed(
    () => this.columnDefs().length + (this.selectionType() !== 'none' ? 1 : 0) + (this.enableRowExpansion() ? 1 : 0),
  );

  /** Whether the request already carries a filter the server indexes, so companion columns may join. */
  indexedFilterActive = computed(() =>
    indexedFilterActive(
      this.filters(),
      this.filterability(),
      this.externalFilterFields(),
      this.searches(),
      this.boxActive(),
    ),
  );

  private readonly scrollContainer = viewChild<ElementRef<HTMLDivElement>>('scrollContainer');
  private readonly tableBody = viewChild<ElementRef<HTMLTableSectionElement>>('tableBody');

  /**
   * How wide the frame around the table is, as the browser lays it out. Undefined until
   * the browser has measured it, and where nothing is laid out (a spec under jsdom, a
   * list inside something hidden): the table then fills its frame and nothing is held.
   */
  private readonly frameWidth = signal<number | undefined>(undefined);

  /**
   * The color the list sits on: the nearest thing around it that is drawn in a color
   * that covers, as the browser reports it, read when the frame is measured, when it
   * scrolls sideways and when the page's root or body is given another attribute (a
   * change from light to dark is set there). Null where nothing around the list has one.
   * The list draws nothing of its own under its rows, and a held cell, which other cells
   * scroll under, has to be drawn whole.
   */
  readonly ground = signal<string | null>(null);

  readonly selectColumnWidth = SELECT_COLUMN_WIDTH;
  readonly expandColumnWidth = EXPAND_COLUMN_WIDTH;

  /** Where the expand column stays while the others scroll: after the select column, where the list has one. */
  expandColumnLeft = computed(() => (this.selectionType() !== 'none' ? SELECT_COLUMN_WIDTH : 0));

  /** Where the first column stays while the others scroll: after the select and expand columns, where the list has them. */
  firstColumnLeft = computed(
    () => this.expandColumnLeft() + (this.enableRowExpansion() && this.detailTemplate() ? EXPAND_COLUMN_WIDTH : 0),
  );

  /**
   * The narrowest the table is drawn: every column at the narrowest it goes. A frame
   * narrower than this does not squeeze the columns; the table stays this wide and the
   * frame scrolls sideways.
   */
  tableMinWidth = computed(
    () => this.firstColumnLeft() + this.columnDefs().reduce((sum, col) => sum + this.narrowestFor(col), 0),
  );

  /** Whether the columns do not fit the frame, so the table is wider than it and the frame scrolls sideways. */
  scrollsSideways = computed(() => {
    const frame = this.frameWidth();
    return frame !== undefined && this.tableMinWidth() > frame;
  });

  /** How much of the frame's left edge the held columns take: the select and expand columns and the first column. */
  heldWidth = computed(() => {
    const first = this.columnDefs()[0];
    return this.firstColumnLeft() + (first ? this.narrowestFor(first) : 0);
  });

  /**
   * Whether the first column stays at the left edge while the others scroll under it,
   * with the select and expand columns: so while the list scrolls sideways, unless the
   * held columns would leave less room beside them than the narrowest a column goes (a
   * first column dragged as wide as the frame, a list of one column), where holding
   * them would keep every other column, and the handle that drags the first one back,
   * out of reach.
   */
  holdsFirstColumn = computed(() => {
    const frame = this.frameWidth();
    return frame !== undefined && this.scrollsSideways() && this.heldWidth() + MIN_COLUMN_WIDTH <= frame;
  });

  /**
   * How wide a row that is one cell across the table draws its words (the empty list's
   * message, an expanded row's detail): the frame's width while the table is wider than
   * its frame, so they stay in view whole while the columns scroll, and the cell's own
   * width otherwise.
   */
  acrossWidth = computed(() => (this.scrollsSideways() ? (this.frameWidth() ?? null) : null));

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

    // The frame's width, followed as the window, the page around the list or a scrollbar
    // changes it. An environment with no ResizeObserver (a spec under jsdom) measures
    // nothing, and a frame that is not laid out measures as nothing.
    afterRenderEffect((onCleanup) => {
      const container = this.scrollContainer()?.nativeElement;
      if (!container || typeof ResizeObserver === 'undefined') {
        return;
      }
      const observer = new ResizeObserver(() => {
        const width = container.clientWidth;
        this.frameWidth.set(width > 0 ? width : undefined);
        this.readGround(container);
      });
      observer.observe(container);
      // A change from light to dark made while the list is on the screen is set on the
      // page's root or its body (the theme service sets the root's color scheme and palette).
      const look = new MutationObserver(() => this.readGround(container));
      const page = container.ownerDocument;
      look.observe(page.documentElement, { attributes: true });
      look.observe(page.body, { attributes: true });
      onCleanup(() => {
        observer.disconnect();
        look.disconnect();
      });
    });

    effect(() => {
      if (this.enableVirtualScroll()) {
        this.expandedIds.set(new Set());
      }
    });
  }

  onScroll(event: Event): void {
    const frame = event.target as HTMLElement;
    this.virtualScroll.setScrollTop(frame.scrollTop);
    if (this.holdsFirstColumn() && frame.scrollLeft !== this.lastScrollLeft) {
      this.readGround(frame);
    }
    this.lastScrollLeft = frame.scrollLeft;
  }

  /** The pointer is pressed on the list. The focus a press gives arrives before anything else is done, so the mark is taken back right after. */
  onPress(): void {
    this.pressed = true;
    setTimeout(() => {
      this.pressed = false;
    });
  }

  /**
   * The keyboard's focus landed on a control of the list. The browser brings a control
   * that is out of the frame into view beside the held columns (the frame's scroll
   * padding), and leaves one that is inside the frame where it is, under the held columns
   * or not; this brings such a one out from under them. A control in a held cell, and one
   * in a row that is one cell across, is under nothing. The pointer's own focus moves
   * nothing: the list must not slide from under a press before the click lands.
   */
  onFocusIn(event: FocusEvent): void {
    if (this.pressed || !this.holdsFirstColumn() || !(event.target instanceof HTMLElement)) {
      return;
    }
    const frame = event.currentTarget as HTMLElement;
    const cell = event.target.closest('.impulse-row > td, .impulse-grid-header-row > th');
    if (
      cell === null ||
      cell.closest('.impulse-grid-scroll') !== frame ||
      cell.matches('.select-col, .expand-col, .first-col')
    ) {
      return;
    }
    const under = frame.getBoundingClientRect().left + this.heldWidth() - event.target.getBoundingClientRect().left;
    if (under > 0) {
      frame.scrollLeft -= under;
    }
  }

  /** Reads the color the list sits on off the page: the frame's own, else the nearest of the elements it is inside. */
  private readGround(frame: HTMLElement): void {
    const view = frame.ownerDocument.defaultView;
    let ground: string | null = null;
    for (let element: HTMLElement | null = frame; view && element && ground === null; element = element.parentElement) {
      const color = view.getComputedStyle(element).backgroundColor;
      if (coversWhatIsUnder(color)) {
        ground = color;
      }
    }
    this.ground.set(ground);
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

  /**
   * The actions a row offers in its menu column: the entries the list wrote on the
   * display row under the reserved key, none where it wrote nothing. A row that offers
   * none draws no button.
   */
  actionsOf(row: RecordData): RowAction[] {
    return ((row as Record<string, unknown>)[rowActionsKey] as RowAction[] | undefined) ?? [];
  }

  widthFor(col: ColumnConfig): number | null {
    const defaultWidth = col.buttonConfig ? ACTION_COLUMN_WIDTH : null;
    return this.columnWidths()[col.id] ?? col.width ?? defaultWidth;
  }

  /**
   * The narrowest the column is drawn: the width a person dragged it to, else the width
   * it declares, else the action column's own, else the width the library gives every
   * column of text. A column with none of the first three shares the room the table has
   * left over, and this is the least that share can be.
   */
  narrowestFor(col: ColumnConfig): number {
    return this.widthFor(col) ?? MIN_TEXT_COLUMN_WIDTH;
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
