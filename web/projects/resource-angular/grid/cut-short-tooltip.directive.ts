import { DestroyRef, Directive, ElementRef, inject, input, NgZone, OnInit } from '@angular/core';
import { MatTooltip } from '@angular/material/tooltip';

/** The class the tooltip of a cut-short text carries, which the grid's styles widen to hold a whole value. */
const CUT_SHORT_TOOLTIP_CLASS = 'impulse-grid-whole-text';

/**
 * How long after a tooltip is closed its anchor is taken back, in milliseconds: longer
 * than the tooltip takes to fade, since a tooltip that is still fading follows its anchor
 * when the list scrolls.
 */
const ANCHOR_KEPT_MS = 200;

/**
 * Whether a one-line text is wider than the box that holds it, so its end is cut off.
 * The browser gives the two widths in whole pixels, and a text a fraction of a pixel too
 * wide is cut with an ellipsis all the same. So a text the whole pixels call a fit is
 * measured again, by the width its own characters take, and counts as cut when that comes
 * within a pixel of the room its box leaves: a text that only just fits is then offered
 * too, which costs nothing, and one that is cut is never passed over.
 */
function isCutShort(text: HTMLElement): boolean {
  if (text.scrollWidth > text.clientWidth) {
    return true;
  }
  const range = text.ownerDocument.createRange();
  range.selectNodeContents(text);
  // An environment that lays nothing out (a spec under jsdom) has no such measure.
  if (typeof range.getBoundingClientRect !== 'function') {
    return false;
  }
  const style = getComputedStyle(text);
  const room = text.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
  return style.whiteSpace === 'nowrap' && range.getBoundingClientRect().width > room - 1;
}

/**
 * The lines of a cell that draws a list a line each: its child elements, where it has
 * some and every one of them is a line (`.cell-line`). Null for any other text.
 */
function linesOf(text: HTMLElement): HTMLElement[] | null {
  const children = Array.from(text.children);
  if (children.length === 0 || children.some((child) => !child.classList.contains('cell-line'))) {
    return null;
  }
  return children as HTMLElement[];
}

/**
 * Whether a text is cut short: a cell drawn a line each where any of its lines is, each
 * measured as a one-line text; any other text as one line.
 */
function isTextCutShort(text: HTMLElement): boolean {
  return linesOf(text)?.some(isCutShort) ?? isCutShort(text);
}

/** The words the tooltip says of a text: a cell's lines with a comma between them, any other text whole. */
function wordsOf(text: HTMLElement): string {
  const lines = linesOf(text);
  if (lines === null) {
    return text.textContent ?? '';
  }
  return lines.map((line) => line.textContent ?? '').join(', ');
}

/**
 * Offers the whole text of a table cell or a column header that is cut short. Every cell
 * of the grid is one line, and a text wider than its column ends in an ellipsis; while
 * the pointer or the keyboard's focus is on such a text, the whole of it shows in the
 * library's tooltip, as an icon cell shows its words. A text that fits offers nothing.
 * A cell that draws a list a line each is offered by its lines: where any one of them is
 * cut, the tooltip says them all, with a comma between them, under the pointer on any of
 * its lines and at the Tab key, and the tooltip is hung over the whole cell.
 *
 * A text is cut short when it is wider than its box, measured as the pointer or the focus
 * arrives, so the answer follows a column being resized and the window changing width
 * with nothing watching either. The keyboard's focus arrives only on what takes it, and a
 * cell of text does not: when the Tab key is pressed, wherever the focus is then, each
 * cell whose text is cut is made a stop of the Tab key before the browser picks the next
 * stop, and the stop is taken from each cell whose text fits. The cells are looked at for
 * that at the key and never when the table is drawn, and not again until the table's
 * owner says that what it draws has changed (`redrawn`).
 *
 * One tooltip and a handful of listeners serve every cell: the grid draws every row of
 * its page unless it scrolls virtually, and a list served whole is one long page, so a
 * tooltip, a listener or an observer on each cell would be paid for once per cell. The
 * listeners stay on the table while its rows come and go, so virtual scroll is served the
 * same way, and rows read anew under a resting pointer are too: the browser says what the
 * pointer is on now, as if it had moved.
 *
 * The directive sits on an empty element in the positioned box around the table, and
 * that element is the tooltip's anchor: it is laid over the cut text before the tooltip
 * opens, is never seen, and takes no pointer events, so what lies under it is hovered
 * and clicked as before.
 *
 *     <table #table>…</table>
 *     <span class="cut-short-anchor" [impulseCutShortTooltip]="table"></span>
 */
@Directive({
  selector: '[impulseCutShortTooltip]',
  standalone: true,
  hostDirectives: [MatTooltip],
  host: { 'aria-hidden': 'true' },
})
export class CutShortTooltipDirective implements OnInit {
  /** The table whose cells and headers are offered. */
  readonly table = input.required<HTMLTableElement>({ alias: 'impulseCutShortTooltip' });

  private readonly anchor = inject<ElementRef<HTMLElement>>(ElementRef).nativeElement;
  private readonly tooltip = inject(MatTooltip);
  private readonly zone = inject(NgZone);
  private readonly destroyRef = inject(DestroyRef);
  /** Whether the tooltip was opened and not yet withdrawn, so a pointer crossing the table asks for nothing. */
  private offered = false;
  /** Hears the pointer's one move after a scroll closed the tooltip. */
  private readonly moved = (event: Event): void => {
    this.arrive(event.target);
  };
  /** The frame in which what the focus arrived on is offered, while it has not come. */
  private awaited: number | undefined;
  /** The wait after which the anchor of a closed tooltip is taken back, while it runs. */
  private kept: ReturnType<typeof setTimeout> | undefined;
  /** Whether the cells' stops of the Tab key are those of what the table draws now. */
  private reached = false;

  constructor() {
    this.tooltip.position = 'above';
    this.tooltip.tooltipClass = CUT_SHORT_TOOLTIP_CLASS;
  }

  ngOnInit(): void {
    const table = this.table();
    // Outside the zone: a pointer crossing the table checks nothing in an application
    // that runs with zone.js, and opening or closing the tooltip is what asks for a check.
    this.zone.runOutsideAngular(() => {
      this.listen(table, 'mouseover', (event) => {
        this.arrive(event.target);
      });
      this.listen(table, 'mouseout', (event) => {
        this.leave((event as MouseEvent).relatedTarget);
      });
      this.listen(table, 'focusin', (event) => {
        this.arriveOnceInView(event.target);
      });
      this.listen(table, 'focusout', () => {
        this.forget();
        this.withdraw();
      });
      // The Tab key is on its way to its next stop, wherever the focus is now: the cells
      // whose text is cut are stops before the browser picks one. Heard on the way down,
      // so that nothing on the page keeps the key from here.
      this.listen(
        table.ownerDocument,
        'keydown',
        (event) => {
          if ((event as KeyboardEvent).key === 'Tab') {
            this.reach();
          }
        },
        true,
      );
      // A scroll does not bubble, so the box around the table hears the table's scroll
      // container on the way down.
      const frame = this.anchor.parentElement;
      if (frame) {
        this.listen(
          frame,
          'scroll',
          () => {
            this.scrolled();
          },
          true,
        );
      }
    });
    this.destroyRef.onDestroy(() => {
      table.removeEventListener('mousemove', this.moved);
      this.forget();
      clearTimeout(this.kept);
    });
  }

  /**
   * What the table draws has changed, its rows or a width: the cells whose text is cut may
   * be others now, and the next press of the Tab key looks again. Said by the table's
   * owner, which knows; nothing is read here.
   */
  redrawn(): void {
    this.reached = false;
  }

  /**
   * Lets the keyboard reach the cells whose text is cut short: each is made a stop of the
   * Tab key, where its whole text is offered as it is under the pointer, and a cell whose
   * text fits, or that draws its value, is no stop. One read of each cell of this table,
   * and none of a table inside it, every read before the first stop is set; nothing at all
   * where the stops are already those of what the table draws.
   */
  private reach(): void {
    if (this.reached) {
      return;
    }
    this.reached = true;
    const cells = Array.from(this.table().querySelectorAll<HTMLElement>(':scope > tbody > tr > td'));
    const cut = cells.map((cell) => this.textAt(cell) === cell && isTextCutShort(cell));
    cells.forEach((cell, index) => {
      if (cut[index]) {
        cell.tabIndex = 0;
      } else if (cell.hasAttribute('tabindex')) {
        cell.removeAttribute('tabindex');
      }
    });
  }

  private listen(target: EventTarget, type: string, handler: (event: Event) => void, capture = false): void {
    const options = { capture, passive: true };
    target.addEventListener(type, handler, options);
    this.destroyRef.onDestroy(() => {
      target.removeEventListener(type, handler, options);
    });
  }

  /**
   * The focus arrived somewhere in the table. A control that is out of view, or under a
   * column held at the table's edge, is scrolled into view as it takes the focus, and a
   * scroll closes the tooltip; so its text is measured and offered in the next frame,
   * where it has come to rest and the scroll has been heard. An environment with no
   * frames (a spec) offers it at once.
   */
  private arriveOnceInView(target: EventTarget | null): void {
    this.forget();
    const view = this.anchor.ownerDocument.defaultView;
    if (!view || typeof view.requestAnimationFrame !== 'function') {
      this.arrive(target);
      return;
    }
    this.awaited = view.requestAnimationFrame(() => {
      this.awaited = undefined;
      this.arrive(target);
    });
  }

  /** Lets go of an offer that waits for its frame: the focus has left, or come somewhere else. */
  private forget(): void {
    if (this.awaited === undefined) {
      return;
    }
    this.anchor.ownerDocument.defaultView?.cancelAnimationFrame(this.awaited);
    this.awaited = undefined;
  }

  /** The pointer or the focus arrived somewhere in the table: a text cut short there is offered, and anything else closes the tooltip. */
  private arrive(target: EventTarget | null): void {
    const text = target instanceof Element ? this.textAt(target) : null;
    if (text === null || !isTextCutShort(text)) {
      this.withdraw();
      return;
    }
    this.place(text);
    this.offered = true;
    this.zone.run(() => {
      this.tooltip.message = wordsOf(text);
      this.tooltip.show();
    });
  }

  /**
   * The table scrolled, so the text moved from under the tooltip, which closes. The
   * browser says when the pointer comes onto another element and nothing while the same
   * one stays under it, so the pointer's next move in the table is heard, once, and
   * what it is on then is offered.
   */
  private scrolled(): void {
    if (!this.offered) {
      return;
    }
    this.withdraw();
    this.table().addEventListener('mousemove', this.moved, { once: true, passive: true });
  }

  /** The pointer left what it was on. Onto the tooltip itself keeps it open, as Material's own stays, so a long text can be read. */
  private leave(onto: EventTarget | null): void {
    if (onto instanceof Element && onto.closest(`.${CUT_SHORT_TOOLTIP_CLASS}`)) {
      return;
    }
    this.withdraw();
  }

  /**
   * Closes the tooltip and, once it has faded, takes the anchor back to nothing. An
   * anchor left where a text was would stay there while the list around it changed
   * width, and could come to lie past the list's edge, where it would give the list
   * something to scroll to.
   */
  private withdraw(): void {
    if (!this.offered) {
      return;
    }
    this.offered = false;
    this.zone.run(() => {
      this.tooltip.hide();
    });
    clearTimeout(this.kept);
    this.kept = setTimeout(() => {
      const style = this.anchor.style;
      style.left = '0';
      style.top = '0';
      style.width = '0';
      style.height = '0';
    }, ANCHOR_KEPT_MS);
  }

  /**
   * The text an element of the table stands for: a header's label, for the header's
   * button and anything in it, or the cell the element is or is in, where the cell holds
   * text alone or a list drawn a line each. A row that is one cell across the table holds
   * its words in a box of their own (the empty list's message), and that box is the text.
   * A cell that draws its value (an icon, a check, a button, an expanded row's detail) has
   * none, and a cell of a table inside this one belongs to that table's own tooltip.
   */
  private textAt(target: Element): HTMLElement | null {
    const label = target.closest('.header-label')?.querySelector<HTMLElement>('.col-header') ?? null;
    const cell = label ?? target.closest('td');
    if (cell === null || cell.closest('table') !== this.table()) {
      return null;
    }
    const only = label === null && cell.childElementCount === 1 ? cell.firstElementChild : null;
    const text = only instanceof HTMLElement && only.classList.contains('impulse-grid-across') ? only : cell;
    return label === null && text.childElementCount > 0 && linesOf(text) === null ? null : text;
  }

  /**
   * Lays the anchor over the text, in the coordinates of the box the anchor is positioned
   * in, and over no more of the text than lies inside that box's line: a cell the list has
   * partly scrolled out of view is offered over the part in view, and the anchor never
   * reaches past the list's edge.
   */
  private place(text: HTMLElement): void {
    const frame = this.anchor.offsetParent;
    if (frame === null) {
      return;
    }
    clearTimeout(this.kept);
    const over = text.getBoundingClientRect();
    const box = frame.getBoundingClientRect();
    // Inside the box's line, where what it holds lies.
    const inside = {
      left: box.left + frame.clientLeft,
      top: box.top + frame.clientTop,
      right: box.left + frame.clientLeft + frame.clientWidth,
      bottom: box.top + frame.clientTop + frame.clientHeight,
    };
    const left = Math.max(over.left, inside.left);
    const top = Math.max(over.top, inside.top);
    const style = this.anchor.style;
    style.left = `${left - inside.left + frame.scrollLeft}px`;
    style.top = `${top - inside.top + frame.scrollTop}px`;
    style.width = `${Math.max(0, Math.min(over.right, inside.right) - left)}px`;
    style.height = `${Math.max(0, Math.min(over.bottom, inside.bottom) - top)}px`;
  }
}
