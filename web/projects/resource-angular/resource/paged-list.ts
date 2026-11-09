import { computed, effect, EffectRef, Injector, signal, Signal, untracked } from '@angular/core';
import { GridPageState, PageTurn } from '@impulseframework/resource-angular/grid';
import { Page } from '@impulseframework/resource';
import { PagePosition, positionAfter } from './resource-list/list-request';

/**
 * The request a paged reader makes for its first page: the route, the filter, the
 * columns, the sorts, and the page size (none, and the descriptor's default applies).
 * The store binds it to the selected tenant.
 */
export interface PagedListRequest {
  route: string;
  filter?: string;
  sensitive?: boolean;
  columns?: string[];
  sorts?: { field: string; direction?: 'asc' | 'desc' }[];
  limit?: number;
}

export type PagedListStatus = 'idle' | 'loading' | 'resolved' | 'error';

/**
 * One server page of a list, held for a reader that pages a bounded source (a picker over
 * a resource with a maximum page size): whenever the request the params describe
 * changes, the first page is asked for with its count; `turn` follows the server's own
 * relations; `reload` repeats the page's request. The rows, the position, the status, and
 * the error are signals. One paging model, the server's: nothing here gathers rows past
 * the page the server answered.
 */
export class PagedList<Row> {
  private readonly current = signal<Page<Row> | undefined>(undefined);
  private readonly position = signal<PagePosition>({ offset: 0 });
  private readonly effectRef: EffectRef;
  private readonly params: Signal<unknown>;
  private readonly request: (params: unknown) => Promise<Page<Row>>;
  private seq = 0;

  readonly status = signal<PagedListStatus>('idle');
  /** The request's error when it failed, an ApiError for a server refusal, else undefined. */
  readonly error = signal<unknown>(undefined);
  /** The rows of the page the server answered. */
  readonly rows = computed<Row[]>(() => this.current()?.rows ?? []);
  /** Where the page sits: the rows before it, whether neighbors exist, and the first page's total. */
  readonly page = computed<GridPageState>(() => {
    const current = this.current();
    return {
      offset: this.position().offset,
      hasPrev: current?.prev !== undefined,
      hasNext: current?.next !== undefined,
      total: this.position().total,
    };
  });

  /**
   * A paged list following `params`: whenever they change, `request` is asked for the
   * first page; undefined params hold the list idle and empty.
   */
  static over<Row, Params>(
    injector: Injector,
    params: Signal<Params | undefined>,
    request: (params: Params) => Promise<Page<Row>>,
  ): PagedList<Row> {
    return new PagedList<Row>(injector, params, request as (params: unknown) => Promise<Page<Row>>);
  }

  private constructor(injector: Injector, params: Signal<unknown>, request: (params: unknown) => Promise<Page<Row>>) {
    this.params = params;
    this.request = request;
    this.effectRef = untracked(() =>
      effect(
        () => {
          const current = this.params();
          untracked(() => this.first(current));
        },
        { injector },
      ),
    );
  }

  /** The page the current one names as previous or next, or the first page again; nothing when there is none. */
  turn(direction: PageTurn): void {
    if (direction === 'first') {
      this.first(untracked(() => this.params()));
      return;
    }
    const current = this.current();
    const step = direction === 'next' ? current?.next : current?.prev;
    if (!current || !step) {
      return;
    }
    void this.run(step, direction, current.rows.length);
  }

  /** The page again, by its own request, so a write shows without losing the position. */
  reload(): void {
    const current = this.current();
    if (!current) {
      this.first(untracked(() => this.params()));
      return;
    }
    void this.run(current.reload, 'reload', current.rows.length);
  }

  /** Stops following the params. */
  destroy(): void {
    this.effectRef.destroy();
  }

  private first(params: unknown): void {
    if (params === undefined) {
      this.seq++;
      this.current.set(undefined);
      this.position.set({ offset: 0 });
      this.error.set(undefined);
      this.status.set('idle');
      return;
    }
    void this.run(() => this.request(params), 'first', 0);
  }

  /** Runs one page request; a request that lands after a later one started is dropped. */
  private async run(request: () => Promise<Page<Row>>, turn: PageTurn | 'reload', leftRows: number): Promise<void> {
    const seq = ++this.seq;
    this.status.set('loading');
    try {
      const landed = await request();
      if (seq !== this.seq) {
        return;
      }
      this.position.set(positionAfter(this.position(), turn, leftRows, { rows: landed.rows.length, total: landed.total }));
      this.current.set(landed);
      this.error.set(undefined);
      this.status.set('resolved');
    } catch (error) {
      if (seq !== this.seq) {
        return;
      }
      this.current.set(undefined);
      this.position.set({ offset: 0 });
      this.error.set(error);
      this.status.set('error');
    }
  }
}
