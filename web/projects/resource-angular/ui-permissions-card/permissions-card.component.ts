import {
  afterNextRender,
  afterRenderEffect,
  ChangeDetectorRef,
  Component,
  computed,
  DestroyRef,
  effect,
  ElementRef,
  inject,
  input,
  signal,
  untracked,
  viewChild,
} from '@angular/core';
import { MatCardModule } from '@angular/material/card';
import { PermissionDigestState } from '@impulseframework/resource';

/** One resource on the card and what the person holds on it. */
export interface PermissionsCardResource {
  /** The resource's name, as the card writes it. */
  name: string;
  /** Where the answer holds, written small beside the name: a tenant's name, `global`. */
  scope?: string;
  /** What the person holds: the state of each permission by its name, and nothing for one they do not hold. */
  held: Readonly<Record<string, PermissionDigestState | undefined>>;
}

/** The permissions the card answers for unless a page names its own: reading a resource and writing it. */
export const READ_AND_WRITE: readonly string[] = ['List', 'Read', 'Create', 'Update', 'Delete'];

/** How the card is drawn: `table` while its columns fit the card, `blocks` when they do not. */
export type PermissionsCardLayout = 'table' | 'blocks';

/**
 * The card for what a person may do: per resource, what they hold of each permission,
 * `granted`, `terms apply` (a conditional grant, which the server decides row by row), or
 * a dash for nothing. A page hands it the resources and what the person holds on each;
 * the card reads no session and asks the server for nothing.
 *
 * The card is laid out by its own width, not the window's: a table, a column per
 * permission, while the columns fit the card, and each resource as a block of its own
 * where they do not, its name over its permissions with each permission named beside its
 * answer. So it fits a phone, and it fits a wide window where the page gives it a narrow
 * column, with no frame of its own that scrolls sideways. The card measures what its
 * table needs, so a page with long names or more permissions changes over at the width
 * its own content needs.
 *
 * `masked` names the permissions a session's mask has removed, which the card stripes.
 * What the page writes inside the element is drawn under the table, and an element
 * marked `permissionsCardEmpty` replaces the words the card says when it lists nothing.
 */
@Component({
  selector: 'impulse-permissions-card',
  imports: [MatCardModule],
  templateUrl: './permissions-card.component.html',
  styleUrl: './permissions-card.component.scss',
})
export class PermissionsCardComponent {
  /** The card's title; a card with neither a title nor a line under it draws no header. */
  heading = input('');
  /** The line under the title. */
  subheading = input('');
  /** The resources, in the order the card lists them, and what the person holds on each. */
  resources = input.required<readonly PermissionsCardResource[]>();
  /** The permissions the card answers for, in column order. */
  permissions = input<readonly string[]>(READ_AND_WRITE);
  /** The permissions a session's mask has removed: the card stripes each one's answers. */
  masked = input<readonly string[]>([]);

  /** How the card is drawn now. */
  readonly layout = signal<PermissionsCardLayout>('table');

  private readonly mark = viewChild.required<ElementRef<HTMLElement>>('mark');
  private readonly table = viewChild.required<ElementRef<HTMLElement>>('table');
  private readonly maskedNames = computed(() => new Set(this.masked()));
  private readonly changes = inject(ChangeDetectorRef);

  /**
   * What reports a new width: the card's, by its mark, and its table's while the card is
   * drawn as one. There is none where nothing is laid out, nor before the card is drawn.
   */
  private observer?: ResizeObserver;

  /**
   * The width the table was last seen to need, which it is drawn as a table again at. It
   * is known only from a table that did not fit: a table that fits is as wide as the card.
   */
  private needed = 0;

  constructor() {
    // What the card lists decides the width its table needs, so a card drawn as blocks is
    // drawn as a table again when its resources or its permissions change, to be measured.
    effect(() => {
      this.resources();
      this.permissions();
      this.needed = 0;
      untracked(() => this.lay('table'));
    });
    // Where nothing is laid out (a test, a page drawn on the server) the card stays a table.
    if (typeof ResizeObserver === 'undefined') {
      return;
    }
    // The card is measured when its own width changes, which the browser reports, and
    // when what it lists changes, once that is drawn. The browser reports a width after
    // it has laid the page out and before it paints, so a change of layout is drawn inside
    // the report itself: the frame that paints is the layout its width calls for, never
    // one frame of a table too wide for the card, nor of blocks where a table fits.
    const observer = new ResizeObserver(() => {
      const before = this.layout();
      this.fit();
      if (this.layout() !== before) {
        this.changes.detectChanges();
      }
    });
    afterNextRender(() => {
      this.observer = observer;
      observer.observe(this.mark().nativeElement);
      if (this.layout() === 'table') {
        observer.observe(this.table().nativeElement);
      }
    });
    afterRenderEffect({
      read: () => {
        this.resources();
        this.permissions();
        untracked(() => this.fit());
      },
    });
    inject(DestroyRef).onDestroy(() => observer.disconnect());
  }

  /** Whether a session's mask has removed the permission. */
  isMasked(permission: string): boolean {
    return this.maskedNames().has(permission);
  }

  /**
   * Chooses the layout by the room the card has. A table whose cells are each on one line
   * is wider than its frame when its columns do not fit, and that width is what it needs:
   * the card is drawn as blocks until its frame is that wide again.
   */
  private fit(): void {
    const room = this.mark().nativeElement.clientWidth;
    if (room === 0) {
      return;
    }
    if (this.layout() === 'blocks') {
      if (room >= this.needed) {
        this.lay('table');
      }
      return;
    }
    const needs = this.table().nativeElement.offsetWidth;
    if (needs > room) {
      this.needed = needs;
      this.lay('blocks');
    }
  }

  /**
   * Draws the card as the layout given. The table's own width is watched only while the
   * card is drawn as a table: as blocks its width says nothing, and a report asked of an
   * element the report itself has just resized is one the browser cannot deliver in the
   * same frame, which it says as an error on the page.
   */
  private lay(layout: PermissionsCardLayout): void {
    if (this.layout() === layout) {
      return;
    }
    this.layout.set(layout);
    if (!this.observer) {
      return;
    }
    if (layout === 'table') {
      this.observer.observe(this.table().nativeElement);
    } else {
      this.observer.unobserve(this.table().nativeElement);
    }
  }
}
