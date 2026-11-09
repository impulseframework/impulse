import { computed, Signal, signal } from '@angular/core';
import { VirtualScrollConfig } from './grid-types';

const DEFAULT_VIRTUALIZED_PADDING = 5;

/** Number of rows rendered up front, before a row height has been measured, to sample for the average. */
export const INITIAL_PROBE_ROW_COUNT = 50;

export interface VirtualRange {
  start: number;
  end: number;
}

/**
 * Tracks scroll position and viewport size for a virtualized table body, and derives the
 * slice of rows that should actually be rendered (padded above/below by `virtualizedPadding`
 * rows). When `rowHeight` isn't configured, the grid renders an initial unvirtualized probe
 * batch, measures the average rendered row height from it via `measureRowHeight`, and reuses
 * that measurement for the rest of the grid's lifetime.
 */
export class VirtualScrollState {
  private readonly scrollTop = signal(0);
  private readonly viewportHeight = signal(0);
  private readonly measuredRowHeight = signal<number | undefined>(undefined);

  constructor(
    private readonly rowCount: Signal<number>,
    private readonly config: Signal<VirtualScrollConfig>,
  ) {}

  readonly rowHeight = computed<number | undefined>(() => this.config().rowHeight ?? this.measuredRowHeight());

  readonly padding = computed(() => this.config().virtualizedPadding ?? DEFAULT_VIRTUALIZED_PADDING);

  readonly visibleRowCount = computed(() => {
    const rowHeight = this.rowHeight();
    return rowHeight ? Math.ceil(this.viewportHeight() / rowHeight) : 0;
  });

  readonly range = computed<VirtualRange>(() => {
    const total = this.rowCount();
    const rowHeight = this.rowHeight();

    if (!rowHeight || !total) {
      return { start: 0, end: Math.min(total, INITIAL_PROBE_ROW_COUNT) };
    }

    const padding = this.padding();
    // Clamp firstVisible so the window can't be pushed past the last row: without this,
    // scrolling beyond the natural end keeps growing `start` (since `end` is already
    // clamped to `total`), eventually passing `end` and rendering zero rows while the top
    // spacer balloons to fill the extra scroll distance - an ever-growing dead zone past
    // the last row that snaps back the moment you scroll up again.
    const maxFirstVisible = Math.max(0, total - this.visibleRowCount());
    const firstVisible = Math.min(maxFirstVisible, Math.max(0, Math.floor(this.scrollTop() / rowHeight)));
    const start = Math.max(0, firstVisible - padding);
    const end = Math.min(total, firstVisible + this.visibleRowCount() + padding);
    return { start, end };
  });

  readonly topPadding = computed(() => this.range().start * (this.rowHeight() ?? 0));
  readonly bottomPadding = computed(() => (this.rowCount() - this.range().end) * (this.rowHeight() ?? 0));

  setScrollTop(scrollTop: number): void {
    this.scrollTop.set(scrollTop);
  }

  setViewportHeight(height: number): void {
    this.viewportHeight.set(height);
  }

  /** Sets the measured row height, unless one has already been measured or was explicitly configured. */
  measureRowHeight(averageHeight: number): void {
    if (this.measuredRowHeight() === undefined && averageHeight > 0) {
      this.measuredRowHeight.set(averageHeight);
    }
  }
}
