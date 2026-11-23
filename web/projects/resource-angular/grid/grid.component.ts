import { CommonModule } from '@angular/common';
import {
  afterRenderEffect,
  Component,
  computed,
  DestroyRef,
  effect,
  ElementRef,
  inject,
  input,
  output,
  signal,
  TemplateRef,
  untracked,
  viewChild,
} from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDividerModule } from '@angular/material/divider';
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
  listValues,
  operatorOption,
  operatorsFor,
  withChoice,
} from './grid-filter.util';
import {
  ColumnFilter,
  ColumnFilterability,
  ColumnFilterChoices,
  ColumnFilterInputs,
  ColumnSearches,
  FilterChoice,
  FilterControl,
  FilterOperator,
  FilterOperatorOption,
  FilterValueInput,
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
 * The most the narrowest of a column of text can be, in pixels, where the column declares
 * no width of its own and nobody has dragged it: a column whose heading and values need
 * less goes as narrow as they need, and one whose values need more stays at this and cuts
 * them short. It is 135 pixels of text between the cell's padding and its line, nineteen
 * characters of the list's 14 pixel type, which is a person's first and last name. It is
 * also the widest that still shows one more column whole beside the held first one on a
 * phone 390 pixels wide, on a page and in a pop-up, in a list with no select or expand
 * column: two columns of this width are 320 of the 336 pixels a list has there, and a
 * list holds its first column only while its widest other column can be seen whole
 * beside it (`holdsFirstColumn`).
 */
export const MIN_TEXT_COLUMN_WIDTH = 160;

/**
 * The room a column keeps beyond what its widest content measures, in pixels. A text that
 * comes within a pixel of its box counts as cut short (CutShortTooltipDirective), and a
 * table whose cells share their lines keeps half a pixel of its width for its own edge, so
 * a column drawn at exactly what its content measures would cut what it was measured for.
 */
const SPARE_WIDTH = 2;

/**
 * The room a heading a person can sort by keeps after its words for the sort's arrow, in
 * pixels, whether or not the arrow is drawn: the arrow's 14 and the gap of 2 before it, as
 * the stylesheet draws them (`.sort-icon`, `.header-label`). The room is kept from the
 * start, so a column as wide as its heading needs does not grow when a person first sorts
 * by it.
 */
const SORT_ARROW_ROOM = 16;

/**
 * A length as the page lays it out, from one read off what is drawn: the length divided by
 * how much the page draws the list larger or smaller than it lays it out (`scale`, see
 * `drawnScale`). A browser hands back what it draws, so a list inside something that is
 * drawn smaller for a moment (a dialog that grows open) would be read as needing less
 * than it does once that has ended. Where nothing scales the list the length is the read
 * itself. Where something does, it is kept to a thousandth of a pixel, so that what the
 * division leaves over never carries a whole number of pixels on to the next.
 */
function laidOut(drawn: number, scale: number): number {
  return scale === 1 ? drawn : Math.round((drawn / scale) * 1000) / 1000;
}

/** How wide what an element holds is laid out, cut short or not: its text's own line. */
function contentWidth(element: Element, range: Range, scale: number): number {
  range.selectNodeContents(element);
  return laidOut(range.getBoundingClientRect().width, scale);
}

/**
 * How wide what a cell of a row holds is laid out, cut short or not. An icon is read by
 * its box, which the stylesheet fixes, and never as a line of text: an icon is written as
 * the word that names it, and where the icons' font is not there (still on its way when
 * the wait for it ran out, or failed) that word is laid out in other letters, wider than
 * the box that clips it, so the column would be read as needing the word's room and keep
 * it. A list drawn a line each is read by its longest line, each by its own text: a
 * line's box is as wide as its cell whatever the line says, so read by its box the column
 * would need whatever it happened to be drawn at. Anything else is its text's own line.
 */
function cellContentWidth(cell: Element, range: Range, scale: number): number {
  const icon = cell.querySelector(':scope > mat-icon');
  if (icon) {
    return laidOut(icon.getBoundingClientRect().width, scale);
  }
  const lines = Array.from(cell.querySelectorAll(':scope > .cell-line'));
  if (lines.length === 0) {
    return contentWidth(cell, range, scale);
  }
  return lines.reduce((widest, line) => Math.max(widest, contentWidth(line, range, scale)), 0);
}

/**
 * How long the lists of a page wait for the fonts the page asked for, in milliseconds,
 * counted from when one of them first found a font on its way: the three seconds a browser
 * itself gives a font before it draws other letters in its place. A list that has not been
 * read is not drawn while it waits, so the wait has an end: a font that never arrives must
 * not keep a list off the screen.
 */
const FONT_WAIT = 3000;

/** Since when a list has known the fonts of its page to be on their way, by the page's set of fonts. */
const fontsAwaitedSince = new WeakMap<FontFaceSet, number>();

/**
 * How much longer a list waits for the fonts of its page, in milliseconds: undefined where
 * the page has no font on its way (one it asked for that has neither arrived nor failed),
 * and where its browser keeps no account of its fonts; nothing once the wait has run its
 * length (FONT_WAIT). The wait is the page's, not one list's: it starts when the first of
 * its lists finds a font on its way and ends for all of them together, so a font that never
 * arrives holds back the lists on the screen then, once, and no list opened after that.
 */
function fontWaitLeft(page: Document): number | undefined {
  const fonts = page.fonts as FontFaceSet | undefined;
  if (fonts === undefined) {
    return undefined;
  }
  if (fonts.status !== 'loading') {
    fontsAwaitedSince.delete(fonts);
    return undefined;
  }
  let since = fontsAwaitedSince.get(fonts);
  if (since === undefined) {
    since = Date.now();
    fontsAwaitedSince.set(fonts, since);
    // The next fonts to be on their way are waited for from their own start, whether or
    // not a list is on the screen when these arrive.
    fonts.addEventListener('loadingdone', () => fontsAwaitedSince.delete(fonts), { once: true });
  }
  return Math.max(0, since + FONT_WAIT - Date.now());
}

/** What a cell takes of its column beside its content: its padding and its lines. */
function aroundContent(cell: Element, view: Window): number {
  const style = view.getComputedStyle(cell);
  return [style.paddingLeft, style.paddingRight, style.borderLeftWidth, style.borderRightWidth].reduce(
    (sum, length) => sum + (parseFloat(length) || 0),
    0,
  );
}

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
 * A column is never squeezed under the narrowest it is drawn (`narrowestFor`): what its
 * heading and the values it has shown need, measured off what is drawn, and never more
 * than MIN_TEXT_COLUMN_WIDTH. The table fills its frame while every column fits, and the
 * room the columns leave goes first to those that cut a value at their narrowest, as far
 * as each needs to show its values whole; with every value whole, the columns share the
 * frame equally but for one that needs more than its share. Where the columns do not
 * fit, each is as narrow as it goes, the table stays as wide as that and the frame scrolls
 * sideways, the page never. The first column is then held at the left edge, with the
 * select and expand columns, while the others scroll under it, as long as the widest of
 * the others can be seen whole beside it; no second column is ever held.
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
    MatDividerModule,
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
  /** The values each column's field takes where it takes one of a fixed set, by column id, from the generated field metadata's `enumeration`: the column's menu offers their names, to pick one or tick several, and the filter carries the values. A column absent here takes its value from its control. */
  filterChoices = input<ColumnFilterChoices>({});
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
  /**
   * The id of the column whose filter menu opened, each time one opens: the page may then
   * ask for values the column's menu offers (`filterChoices`), which the open menu shows
   * as they arrive.
   */
  filterMenuOpened = output<string>();
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
  /**
   * What each column's heading and the values it has shown need, in pixels, by column id:
   * the narrowest a column goes where nobody dragged it and the page gave it no width. A
   * column is here once it has been measured, between the least a column goes and
   * MIN_TEXT_COLUMN_WIDTH, and its entry only grows while the list keeps its columns, so
   * a page of shorter values, a sort or a filter narrows nothing under a person's eyes. A
   * font that arrives changes how wide every text is drawn: the list is read again then,
   * and a column grows where its text needs more in the font's letters and narrows nowhere.
   * Nothing is read while a font is on its way, so no column is held as wide as the
   * letters drawn in a font's place, until the wait for it has run its length (FONT_WAIT).
   */
  private readonly needs = signal<ReadonlyMap<string, number>>(new Map());
  /**
   * What each column's heading and the values it has shown need to be drawn whole, in
   * pixels, by column id: its need without MIN_TEXT_COLUMN_WIDTH over it. A column whose
   * entry is more than its need is one the list cuts at its narrowest, and the room the
   * list has left over goes to it before any other (`givenWidths`). It is read with
   * `needs` and grows as that does; no entry is more than the frame was wide when it was
   * read, which is as much as a column is ever given (`readAsFarAs`).
   */
  private readonly wholes = signal<ReadonlyMap<string, number>>(new Map());
  /**
   * How wide the frame was when a column was last read no further than that, in pixels: a
   * column with a value that needs the whole frame or more. A frame that grows wider than
   * this has more to give that column than the list knows it needs, and the list is read
   * again. It is undefined while every column was read to the end of what it needs.
   */
  private readAsFarAs: number | undefined;
  /** The columns `needs` was measured for, by id and heading, in order: other columns start again. */
  private measuredColumns = '';
  /**
   * Whether the list is held back: its columns have not been read, so how wide each is
   * drawn is not known yet, and nothing of the list is drawn till it is. Not its headings,
   * its rows or its pager, and no loading cover over them: a column drawn at the library's
   * width now would narrow under a person's eyes once it is read. Three things hold a
   * list back. It is loading and has never been read with the columns it has: its first
   * load, whose rows are what its columns are read from. Or a font its page asked for is
   * on its way, loading or not, since a text is not drawn in its own letters till the font
   * arrives. Or something around it draws it at no size at all, so nothing can be read off
   * it. It stays held back until it is read: once it has loaded, once the font has arrived
   * or failed, or the wait for it has run its length (FONT_WAIT), and at the first frame
   * that draws it at some size. It is then drawn whole. A list that has been read with its
   * columns is never held back: it keeps what it shows while it loads again (a page turn,
   * a sort, a filter), under the loading cover, and a font on its way or a moment at no
   * size only keeps it from being read again. A list that cannot be read at all (one that
   * is not laid out, a browser that measures nothing) is not held back either, so nothing
   * holds a list back without an end.
   */
  readonly heldBack = signal(false);
  /** The end of the wait for a font, where the list has one coming: it is read then, font or no font. */
  private fontWait: ReturnType<typeof setTimeout> | undefined;
  /** The next frame the list is read at, where it was drawn at no size when it was last to be read. */
  private nextFrame: { view: Window; id: number } | undefined;
  /** Whether the frame was not laid out when the columns were last to be read, so its first width reads them. */
  private unlaid = true;
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
  private readonly table = viewChild<ElementRef<HTMLTableElement>>('table');
  private readonly tableBody = viewChild<ElementRef<HTMLTableSectionElement>>('tableBody');
  /** A length the stylesheet sets, drawn with the list: what the page scales the list by is read off it. */
  private readonly rule = viewChild<ElementRef<HTMLElement>>('rule');
  /** What offers a cut-short text whole, and makes the cells whose text is cut stops of the Tab key. */
  private readonly cutShort = viewChild(CutShortTooltipDirective);

  /**
   * How wide the frame around the table is, as the browser lays it out: read when the
   * columns are read, which is before the list's first drawing is shown, and whenever the
   * browser reports another size. Undefined where nothing is laid out (a spec under
   * jsdom, a list inside something hidden): the table then fills its frame and nothing is
   * held.
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

  /**
   * The width the list gives each column that has none of its own, by id; a column that
   * is not here shares equally what the others leave, which is what a list that fits has
   * always done. In a frame too narrow for the columns every one is here at the narrowest
   * it goes, and the frame scrolls. In a frame with room left over, that room goes first
   * to the columns that cut a value at their narrowest (`wholes`), each as far as showing
   * its values whole: while it does not reach for all of them they share it, the least cut
   * made whole first, and every other column stays at what it needs. Once every value is
   * whole, a column that needs more than an equal share of the frame is given what it
   * needs and the rest share what is left, so in a frame with room to spare none is here
   * and the list is drawn as it always was. None is here where the frame is not measured.
   *
   * In a frame the columns fit, one column that shares is never here: the table is as wide
   * as its frame only while a column is left to take what the others do not. A table whose
   * every column carries a width is drawn as wide as they and its border add up to, which
   * is past a frame the columns fill to the pixel, and that frame would scroll sideways.
   */
  private readonly givenWidths = computed<ReadonlyMap<string, number>>(() => {
    const given = new Map<string, number>();
    const frame = this.frameWidth();
    if (frame === undefined) {
      return given;
    }
    const sharing = this.columnDefs()
      .filter((col) => this.widthFor(col) === null)
      .map((col) => ({ id: col.id, narrowest: this.narrowestFor(col), whole: this.wholeFor(col) }));
    const atNarrowest = sharing.reduce((sum, col) => sum + col.narrowest, 0);
    let room = frame - this.tableMinWidth() + atNarrowest;
    if (sharing.reduce((sum, col) => sum + col.whole, 0) > room) {
      // Not every value can be whole. What the frame has over the columns at their
      // narrowest, if anything, is shared by the columns that cut a value: equally, but
      // that none takes more than makes it whole, so the least cut are whole first.
      let over = Math.max(0, room - atNarrowest);
      let cut = sharing.filter((col) => col.whole > col.narrowest).length;
      for (const col of sharing.sort((a, b) => a.whole - a.narrowest - (b.whole - b.narrowest))) {
        const more = col.whole > col.narrowest ? Math.min(col.whole - col.narrowest, Math.floor(over / cut--)) : 0;
        given.set(col.id, col.narrowest + more);
        over -= more;
      }
      const mostCut = sharing.at(-1);
      if (mostCut !== undefined && !this.scrollsSideways()) {
        // The columns fit. The most cut is the last to take, and takes all that is left:
        // it is given no width, and the table gives it that to the pixel.
        given.delete(mostCut.id);
      }
      return given;
    }
    let left = sharing.length;
    for (const col of sharing.sort((a, b) => b.whole - a.whole)) {
      // An equal share is enough for this column, and so for every narrower one after it.
      if (col.whole * left <= room) {
        break;
      }
      given.set(col.id, col.whole);
      room -= col.whole;
      left--;
    }
    return given;
  });

  /** How much of the frame's left edge the held columns take: the select and expand columns and the first column. */
  heldWidth = computed(() => {
    const first = this.columnDefs()[0];
    return this.firstColumnLeft() + (first ? this.narrowestFor(first) : 0);
  });

  /**
   * The widest of the columns that scroll under the held one: every column after the
   * first, a button's column too, each as wide as it is drawn while the list scrolls
   * sideways. Nothing in a list of one column.
   */
  private readonly widestScrollingColumn = computed(() =>
    this.columnDefs()
      .slice(1)
      .reduce((widest, col) => Math.max(widest, this.narrowestFor(col)), 0),
  );

  /**
   * Whether the first column stays at the left edge while the others scroll under it,
   * with the select and expand columns: so while the list scrolls sideways and the widest
   * of the columns that scroll can be seen whole beside the held part, which is every one
   * of them then, each in its turn. With less room than that beside the held part (a
   * narrow frame, a first column dragged wide, a list of one column) nothing is held and
   * every column scrolls: a column that could never be seen whole is worse than a first
   * column that scrolls away. One column is held and never two: the first, which is the
   * one that names the row.
   */
  holdsFirstColumn = computed(() => {
    const frame = this.frameWidth();
    return frame !== undefined && this.scrollsSideways() && this.heldWidth() + this.widestScrollingColumn() <= frame;
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
    // A list that leaves while it waits, for a font or to be drawn at some size, is not read
    // when the wait ends.
    inject(DestroyRef).onDestroy(() => {
      clearTimeout(this.fontWait);
      this.nextFrame?.view.cancelAnimationFrame(this.nextFrame.id);
    });

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
        // A list drawn inside something hidden had nothing to measure until now. One that was
        // laid out when it was drawn was read then, and a frame that only changes width
        // changes nothing a column needs. It changes how much of a need the list knows,
        // where a value needed all of a narrower frame and was read no further.
        if ((this.unlaid && width > 0) || (this.readAsFarAs !== undefined && width > this.readAsFarAs)) {
          this.measureColumns();
        }
      });
      observer.observe(container);
      // A change from light to dark made while the list is on the screen is set on the
      // page's root or its body (the theme service sets the root's color scheme and palette).
      const look = new MutationObserver(() => this.readGround(container));
      const page = container.ownerDocument;
      look.observe(page.documentElement, { attributes: true });
      look.observe(page.body, { attributes: true });
      // A font that arrives changes how wide every text is drawn. A list that waited for it
      // is read now, for the first time, and drawn; one that was read before is read again,
      // and what a column needs only grows, so nothing a person has seen narrows. A text is
      // cut or whole by its letters, so the cells the Tab key stops on are looked for again
      // at the key's next press.
      const lettered = (): void => {
        this.measureColumns();
        this.cutShort()?.redrawn();
      };
      const fonts = page.fonts as FontFaceSet | undefined;
      fonts?.addEventListener('loadingdone', lettered);
      onCleanup(() => {
        observer.disconnect();
        look.disconnect();
        fonts?.removeEventListener('loadingdone', lettered);
      });
    });

    // What each column needs, read off what is drawn once the rows, the columns or a
    // heading's marks (its sort arrow, its filter button) have changed and been drawn, or
    // the list has finished loading: one pass of reads, and the widths it finds are drawn
    // in the same turn.
    afterRenderEffect({
      read: () => {
        this.loading();
        this.visibleRows();
        this.columnDefs();
        this.sorts();
        this.sortable();
        this.filterability();
        this.searches();
        this.indexedFilterActive();
        untracked(() => this.measureColumns());
      },
    });

    // The cells whose text is cut short are stops of the Tab key, and which they are is
    // looked for when the key is pressed, never when the list is drawn. The list says here
    // that what it draws has changed, its rows or anything a width comes from, so that the
    // next press looks again: nothing is read off the page for it.
    effect(() => {
      this.visibleRows();
      this.columnDefs();
      this.needs();
      this.wholes();
      this.frameWidth();
      this.columnWidths();
      untracked(() => this.cutShort()?.redrawn());
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

  /**
   * Reads what each column of no width of its own needs: the widest of its heading, with
   * the marks beside it, and the values the list is showing, with the cell's padding and
   * lines. It is read off what is drawn (the text's own line, an icon's box, the longest
   * line of a list drawn a line each: `cellContentWidth`), not worked
   * out from the type, so it holds for whatever a cell draws and however the page styles
   * it, and each read is brought back to what the page lays out (`drawnScale`), so it also
   * holds while something around the list is drawn smaller or larger than it is: a dialog
   * that grows open draws the list it holds at nine tenths of its size at first, and a
   * column read at that would cut its widest value once the dialog has opened. Nothing has
   * to be read again when it has, and no column moves then. A list drawn at no size at all
   * has nothing to read: it is read at the next frame that draws it at some size, and one
   * that has never been read is not drawn until then (`heldBack`).
   * Every read comes before the one write, so the browser lays the list out once; a
   * column already at the widest its narrowest goes is not read at all, and a value the
   * column has shown in this pass is not read twice. A need only grows: see `needs`. A
   * list that is not laid out (inside something hidden, a spec that lays nothing out) has
   * nothing to read. Nor has a list that is loading: what it draws under the loading cover
   * is not yet what it shows (a related record not yet read is drawn as its key, longer
   * than the name that replaces it), and a need read from it would never come down. It is
   * read once the list has loaded. Nor has a list whose page has a font on its way: a text
   * is drawn in other letters until its font arrives, and a need read from those would
   * never come down either (an icon is read by its box, whatever is drawn in it, so the
   * word that names it counts for nothing then or after). Laying a text out is what
   * starts its font's load, so a pass of reads that leaves a font on its way is not kept.
   * The list is read when the font has arrived, or has failed to (`loadingdone`), and a
   * list that has never been read with its columns is not drawn until then (`heldBack`).
   * The wait has an end (FONT_WAIT): from then on the list is read in the letters the
   * screen shows, and a font that arrives later only grows a column, as a longer value does.
   * A list that is loading and has never been read with its columns is not drawn either,
   * until it has loaded and is read: its first load shows nothing of it, and then all of it.
   */
  private measureColumns(): void {
    const frame = this.scrollContainer()?.nativeElement;
    const table = this.table()?.nativeElement;
    const view = frame?.ownerDocument.defaultView;
    if (!frame || !table || !view) {
      return;
    }
    const width = frame.clientWidth;
    this.unlaid = width === 0;
    // How wide the frame is decides which columns are drawn at their narrowest, and the
    // browser reports a frame's size only after it has drawn it once: a list drawn with
    // its rows from the start (a list in a dialog) would be drawn for one frame as if it
    // had all the room, and then again. The width is read here, with the columns, so the
    // list's first drawing has both.
    this.frameWidth.set(width > 0 ? width : undefined);
    const range = frame.ownerDocument.createRange();
    if (typeof range.getBoundingClientRect !== 'function') {
      return;
    }
    const columns = this.columnDefs();
    const these = columns.map((col) => `${col.id}\n${col.header ?? ''}`).join('\n\n');
    const readBefore = these === this.measuredColumns;
    // The wait for the page's fonts is counted from the first a list knows of them, and a
    // list not read yet is held back from then, though it is still loading: its headings
    // are not drawn under the loading cover only to be taken away when its rows arrive.
    const wait = fontWaitLeft(frame.ownerDocument);
    if (wait !== undefined && wait > 0) {
      this.awaitFont(wait, readBefore);
      return;
    }
    if (this.unlaid) {
      return;
    }
    if (this.loading()) {
      // A first load: what the columns need is read from the rows it brings.
      if (!readBefore) {
        this.heldBack.set(true);
      }
      return;
    }
    const scale = this.drawnScale();
    if (scale === undefined) {
      // Drawn at no size: nothing can be read off it now, and a list never read is not
      // drawn at the size it comes to until it has been.
      if (!readBefore) {
        this.heldBack.set(true);
      }
      this.readAtNextFrame(view);
      return;
    }
    const known = readBefore ? this.needs() : new Map<string, number>();
    const next = new Map(known);
    const wholes = new Map(readBefore ? this.wholes() : []);
    // The select and expand columns come before the first of `columns`.
    const before =
      (this.selectionType() !== 'none' ? 1 : 0) + (this.enableRowExpansion() && this.detailTemplate() ? 1 : 0);
    const headings = table.tHead?.rows[0]?.cells;
    const rows = Array.from(table.tBodies[0]?.rows ?? []).filter((row) => row.classList.contains('impulse-row'));
    // A column is read on, past the most its narrowest can be, until one value needs the
    // whole frame: the room left over goes to a column by how much it is cut, and no
    // column is given more than the frame. A frame narrower than the most a narrowest can
    // be is read as far as that, since the narrowest a column goes is not the frame's to cut.
    const farthest = Math.max(width, MIN_TEXT_COLUMN_WIDTH);
    columns.forEach((col, index) => {
      // What the column needs to show every value whole.
      let need = wholes.get(col.id) ?? 0;
      if (this.widthFor(col) !== null || need >= farthest) {
        return;
      }
      const heading = headings?.[before + index];
      if (heading) {
        need = Math.max(need, this.headingNeeds(heading, range, view, scale));
      }
      let around: number | undefined;
      const read = new Set<string>();
      for (const row of rows) {
        const cell = row.cells[before + index] as HTMLTableCellElement | undefined;
        const words = cell?.textContent ?? '';
        if (need >= farthest) {
          break;
        }
        if (!cell || read.has(words)) {
          continue;
        }
        read.add(words);
        around ??= aroundContent(cell, view);
        need = Math.max(need, Math.ceil(cellContentWidth(cell, range, scale) + around) + SPARE_WIDTH);
      }
      need = Math.min(farthest, Math.max(MIN_COLUMN_WIDTH, need));
      wholes.set(col.id, need);
      next.set(col.id, Math.min(MIN_TEXT_COLUMN_WIDTH, need));
    });
    // The reads laid the list out, and that started the load of a font it had not asked
    // for before: what was read is in the letters drawn in the font's place.
    const started = wait === undefined ? fontWaitLeft(frame.ownerDocument) : undefined;
    if (started !== undefined && started > 0) {
      this.awaitFont(started, readBefore);
      return;
    }
    const same =
      next.size === this.needs().size && Array.from(next).every(([id, need]) => this.needs().get(id) === need);
    const sameWholes =
      wholes.size === this.wholes().size && Array.from(wholes).every(([id, whole]) => this.wholes().get(id) === whole);
    this.measuredColumns = these;
    this.readAsFarAs = Array.from(wholes.values()).some((whole) => whole >= farthest) ? farthest : undefined;
    if (!same) {
      this.needs.set(next);
    }
    if (!sameWholes) {
      this.wholes.set(wholes);
    }
    clearTimeout(this.fontWait);
    this.fontWait = undefined;
    this.heldBack.set(false);
  }

  /**
   * How much the page draws the list larger or smaller than it lays it out, across: what
   * the rule is drawn at over the length the stylesheet gives it. It is 1 for a list
   * nothing scales, and where the rule has no length to compare (a spec that lays nothing
   * out). It is undefined where the list is drawn at no width at all, scaled to nothing by
   * something around it: nothing can be read off it then. It is the size across of a list
   * drawn smaller or larger as a whole; a list that is turned or slanted is not read to
   * the pixel.
   */
  private drawnScale(): number | undefined {
    const rule = this.rule()?.nativeElement;
    const laid = rule?.offsetWidth ?? 0;
    if (!rule || laid <= 0) {
      return 1;
    }
    const drawn = rule.getBoundingClientRect().width;
    return drawn > 0 ? drawn / laid : undefined;
  }

  /**
   * Reads the list at the next frame: it is drawn at no size now, by something around it
   * that no change of the list's own tells of, so it is asked again each frame until it is
   * drawn at some size, loads again or leaves.
   */
  private readAtNextFrame(view: Window): void {
    if (this.nextFrame !== undefined) {
      return;
    }
    const id = view.requestAnimationFrame(() => {
      this.nextFrame = undefined;
      this.measureColumns();
    });
    this.nextFrame = { view, id };
  }

  /**
   * Leaves the list unread until the fonts its page asked for arrive, or the wait for them
   * runs out in `left` milliseconds, whichever is first. A list never read with the columns
   * it has now (`readBefore` false) is not drawn from now until it is read.
   */
  private awaitFont(left: number, readBefore: boolean): void {
    if (!readBefore) {
      this.heldBack.set(true);
    }
    this.fontWait ??= setTimeout(() => {
      this.fontWait = undefined;
      this.measureColumns();
    }, left);
  }

  /**
   * What a column's heading needs: its words whole, the marks after them (the sort's
   * arrow and its number, the filter's button) with the gaps between, and the cell's
   * padding and line. The words are measured as they would be drawn whole, and the marks
   * from where the words end to where the last mark ends, which is the same however much
   * room the heading has now. A heading a person can sort by, which is drawn as a button,
   * needs the room of the sort's arrow whether or not the arrow is drawn
   * (SORT_ARROW_ROOM), and more only where what is drawn after its words takes more: the
   * number of a second sort. Every length is as the page lays it out (`laidOut`).
   */
  private headingNeeds(heading: HTMLElement, range: Range, view: Window, scale: number): number {
    const cell = heading.querySelector(':scope > .header-cell');
    const label = cell?.querySelector(':scope > .header-label');
    if (!cell || !label) {
      return 0;
    }
    const words = label.querySelector(':scope > .col-header');
    const lastOfLabel = label.lastElementChild;
    const lastOfCell = cell.lastElementChild;
    let need = words ? contentWidth(words, range, scale) : 0;
    let marks = 0;
    if (lastOfLabel && lastOfLabel !== words) {
      const from = words ? words.getBoundingClientRect().right : label.getBoundingClientRect().left;
      marks = laidOut(lastOfLabel.getBoundingClientRect().right - from, scale);
    }
    need += label.tagName === 'BUTTON' ? Math.max(SORT_ARROW_ROOM, marks) : marks;
    if (lastOfCell && lastOfCell !== label) {
      need += laidOut(lastOfCell.getBoundingClientRect().right - label.getBoundingClientRect().right, scale);
    }
    return Math.ceil(need + aroundContent(heading, view)) + SPARE_WIDTH;
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

  /** A click makes the column the sort (asc, then desc, then none); shift-click adds it as a secondary. Either shows the list from its first row. */
  toggleSort(col: ColumnConfig, event: MouseEvent): void {
    const field = col.id;
    const current = this.sorts();

    this.showFirstRow();
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

  /** The control the column's menu takes a value from under the draft's operator: the names of the field's values where it takes fixed ones, else the column's own for one value and typed text for a list. */
  valueInput(col: ColumnConfig): FilterValueInput {
    return filterInputFor(col.id, this.filterInputs(), this.draftFor(col).operator, this.filterChoices());
  }

  /** The values the column's field takes, as its menu offers them; none on a column whose field takes any value. */
  choicesFor(col: ColumnConfig): readonly FilterChoice[] {
    return this.filterChoices()[col.id] ?? [];
  }

  /** Whether the draft's list holds the value, so its name is drawn ticked. */
  isTicked(col: ColumnConfig, choice: FilterChoice): boolean {
    return listValues(this.draftFor(col).value).includes(choice.value);
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
    const next = filterInputFor(col.id, this.filterInputs(), operator, this.filterChoices());
    const value = fitsInput(current.value, next, this.choicesFor(col)) ? current.value : '';
    this.drafts.set({ ...this.drafts(), [col.id]: { ...current, operator, value } });
  }

  /** The value from a text box, or the value behind the name picked on a column whose field takes fixed values. */
  setFilterValue(col: ColumnConfig, event: Event): void {
    this.setDraftValue(col, (event.target as HTMLInputElement | HTMLSelectElement).value);
  }

  /** A name ticked or cleared under a list operator: the draft's list gains or loses the value behind it. */
  tickFilterChoice(col: ColumnConfig, choice: FilterChoice, event: Event): void {
    const ticked = (event.target as HTMLInputElement).checked;
    this.setDraftValue(col, withChoice(this.draftFor(col).value, this.choicesFor(col), choice.value, ticked));
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
    this.showFirstRow();
  }

  clearFilter(col: ColumnConfig): void {
    this.dropDraft(col);
    if (!this.hasFilter(col)) {
      return;
    }
    this.filterChange.emit(this.filters().filter((filter) => filter.field !== col.id));
    this.showFirstRow();
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

  /** A column's filter menu opened: the page is told which column's. */
  onFilterMenuOpened(col: ColumnConfig): void {
    this.filterMenuOpened.emit(col.id);
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

  /**
   * The key a link column opens a row by: the row's value in the column's key field, and
   * nothing where the row holds none, so a row that names no row of the page it would
   * open draws no link.
   */
  linkKeyOf(row: RecordData, col: ColumnConfig): string | undefined {
    const key = row[col.buttonConfig?.keyField ?? 'id'];
    return key === undefined || key === null || key === '' ? undefined : String(key);
  }

  /**
   * The lines of a cell whose column draws its list a line each (`lines`): the list's
   * items, and nothing for a column that draws one line or a row whose list is empty.
   */
  linesOf(row: RecordData, col: ColumnConfig): string[] | undefined {
    const value = (row as Record<string, unknown>)[col.id];
    return col.lines && Array.isArray(value) && value.length > 0 ? (value as string[]) : undefined;
  }

  widthFor(col: ColumnConfig): number | null {
    const defaultWidth = col.buttonConfig ? ACTION_COLUMN_WIDTH : null;
    return this.columnWidths()[col.id] ?? col.width ?? defaultWidth;
  }

  /**
   * The narrowest the column is drawn: the width a person dragged it to, else the width
   * it declares, else the action column's own, else what its heading and the values it
   * has shown need (`needs`), which is MIN_TEXT_COLUMN_WIDTH at the most and is that
   * until the column has been measured. A column with none of the first three shares the
   * room the table has left over, and this is the least that share can be.
   */
  narrowestFor(col: ColumnConfig): number {
    return this.widthFor(col) ?? this.needs().get(col.id) ?? MIN_TEXT_COLUMN_WIDTH;
  }

  /**
   * What the column needs to show its heading and every value it has shown whole: its
   * own width where it has one, else what was read off them (`wholes`), which is its
   * narrowest for a column that cuts nothing there and until the column has been read.
   */
  private wholeFor(col: ColumnConfig): number {
    return this.widthFor(col) ?? Math.max(this.narrowestFor(col), this.wholes().get(col.id) ?? 0);
  }

  /**
   * The width the column's heading cell carries, which is the column's in a table of
   * fixed layout: its own (dragged, declared, a button column's), else what the list
   * gives it of the frame (`givenWidths`), else none, and it shares what is left over.
   */
  drawnWidthFor(col: ColumnConfig): number | null {
    return this.widthFor(col) ?? this.givenWidths().get(col.id) ?? null;
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
    this.showFirstRow();
  }

  /**
   * The frame is scrolled back to the list's first row, as a list a person pages, sorts or
   * filters is shown; how far it is scrolled sideways stays.
   */
  private showFirstRow(): void {
    const frame = this.scrollContainer()?.nativeElement;
    if (frame) {
      frame.scrollTop = 0;
    }
  }
}
