import {
  ChangeDetectionStrategy,
  Component,
  computed,
  DestroyRef,
  ElementRef,
  inject,
  input,
  linkedSignal,
  output,
  viewChild,
} from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { searchLimits } from '@impulseframework/resource';

/** How long the box waits after the last keystroke before it commits the text. */
export const SEARCH_DEBOUNCE_MS = 300;

/**
 * The search box: one input whose text the host sends to the server as the filter's
 * `*:search:"…"`. It commits the typed text after a pause (SEARCH_DEBOUNCE_MS), at
 * Enter at once, and the clear button commits nothing, which restores the unnarrowed
 * page; a host that changes `value` from outside resets the draft. The placeholder names
 * the fields the box searches, as the host computed them from the metadata and the
 * caller's grants, and while text is committed the hint reads the host's total as the
 * matches. It sits on a list page over the grid and inside a paged picker's panel, where
 * its keystrokes stay in the input rather than reaching the panel's typeahead.
 */
@Component({
  selector: 'impulse-search-box',
  imports: [MatFormFieldModule, MatInputModule, MatIconModule, MatButtonModule],
  template: `
    <mat-form-field class="search-box" appearance="outline" subscriptSizing="dynamic">
      <mat-icon matPrefix>search</mat-icon>
      <input
        #field
        matInput
        type="text"
        autocomplete="off"
        [attr.maxlength]="maxLength"
        [placeholder]="placeholder()"
        [attr.aria-label]="placeholder()"
        [value]="draft()"
        (input)="type($event)"
        (keydown.enter)="commit()"
        (keydown)="keep($event)" />
      @if (draft() !== '') {
        <button matSuffix mat-icon-button type="button" aria-label="Clear the search" (click)="clear()">
          <mat-icon>close</mat-icon>
        </button>
      }
      @if (hintText(); as hint) {
        <mat-hint>{{ hint }}</mat-hint>
      }
    </mat-form-field>
  `,
  styles: `
    .search-box {
      width: 100%;
    }
  `,
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SearchBoxComponent {
  /** What the empty box says: the fields it searches. */
  placeholder = input<string>('Search');
  /** The committed text, as the host holds it; the draft follows it when the host changes it. */
  value = input<string>('');
  /** The host's total while text is committed: the rows that match. */
  total = input<number | undefined>(undefined);
  /** A line under the box: the fields the caller's role leaves out, say. */
  hint = input<string>('');
  /** The text to search for next; empty to clear. */
  textChange = output<string>();

  readonly maxLength = searchLimits.textLength;
  /** The text as typed, ahead of the commit. */
  readonly draft = linkedSignal(() => this.value());

  private readonly field = viewChild.required<ElementRef<HTMLInputElement>>('field');
  private timer: ReturnType<typeof setTimeout> | undefined;

  /** The hint: the matches while text is committed, then the host's line. */
  readonly hintText = computed(() => {
    const total = this.total();
    const matches =
      this.value().trim() !== '' && total !== undefined ? (total === 1 ? '1 row matches' : `${total} rows match`) : '';
    return [matches, this.hint()].filter((line) => line !== '').join('. ');
  });

  constructor() {
    inject(DestroyRef).onDestroy(() => this.cancel());
  }

  focus(): void {
    this.field().nativeElement.focus();
  }

  type(event: Event): void {
    this.draft.set((event.target as HTMLInputElement).value);
    this.cancel();
    this.timer = setTimeout(() => {
      this.timer = undefined;
      this.commit();
    }, SEARCH_DEBOUNCE_MS);
  }

  /** Sends the draft now; blank text is a clear. */
  commit(): void {
    this.cancel();
    const text = this.draft().trim() === '' ? '' : this.draft();
    if (text !== this.value()) {
      this.textChange.emit(text);
    }
  }

  clear(): void {
    this.cancel();
    this.draft.set('');
    if (this.value() !== '') {
      this.textChange.emit('');
    }
    this.focus();
  }

  /** A keystroke stays in the box: inside a select's panel it would otherwise be read as typeahead. Escape passes, closing the panel. */
  keep(event: KeyboardEvent): void {
    if (event.key !== 'Escape') {
      event.stopPropagation();
    }
  }

  private cancel(): void {
    if (this.timer !== undefined) {
      clearTimeout(this.timer);
      this.timer = undefined;
    }
  }
}
