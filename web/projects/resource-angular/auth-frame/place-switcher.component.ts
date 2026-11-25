import { CdkMenuTrigger } from '@angular/cdk/menu';
import { ChangeDetectionStrategy, Component, computed, ElementRef, inject, signal, viewChild } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { Frame, inSentence } from './frame';
import { ShownEntry, ShownGroup } from './menu';
import { MenuListComponent } from './menu-list.component';
import { ClosedPlace, Place, PlaceService } from './place.service';

/** More places than this and the list opens with a box to find one by name. */
export const findFrom = 8;

/**
 * The place the page is in, beside the application's name, and the way to another: the
 * place outside every tenant for those who hold something there, then each tenant by name,
 * the open one checked. A person with one place sees its name and nothing to open. A page
 * in none of the person's places (a tenant's refusal page, for a person who holds nothing
 * outside every tenant) names no place: the button asks them to choose one of theirs. A
 * long list opens with a box that narrows it as they type; Enter opens the first match.
 *
 * A tenant the person holds a seat in and cannot enter is listed too, under their places
 * and apart from them, headed "Not available": its name with a lock and the server's
 * reason ("Acme admits only people who sign in through its own directory. You signed in
 * with a password."), and nothing to open. The list opens for it even when the person has
 * one place, so a seat is never missing without a word.
 *
 * Nothing is drawn until the person's places are known (PlaceService.known): a list of
 * tenants that has not been read, or could not be, is never shown as a person with one
 * place or with none to switch to.
 */
@Component({
  selector: 'impulse-place-switcher',
  imports: [CdkMenuTrigger, MatIconModule, MenuListComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (listed()) {
      <button
        type="button"
        class="impulse-place-switcher-place"
        [cdkMenuTriggerFor]="list"
        (cdkMenuOpened)="opened()"
        [attr.aria-label]="
          inPlace() ? 'In ' + place().label + '. Switch to another ' + lower(word.one) : 'Choose a ' + lower(word.one)
        ">
        @if (inPlace()) {
          <mat-icon class="impulse-place-switcher-kind">{{ place().id ? 'domain' : outside.icon }}</mat-icon>
          <span class="impulse-place-switcher-lines">
            @if (place().id) {
              <span class="impulse-place-switcher-caption">{{ word.one }}</span>
            }
            <span class="impulse-place-switcher-name">{{ place().label }}</span>
          </span>
        } @else {
          <mat-icon class="impulse-place-switcher-kind">swap_horiz</mat-icon>
          <span class="impulse-place-switcher-lines">
            <span class="impulse-place-switcher-name">Choose a {{ lower(word.one) }}</span>
          </span>
        }
        <mat-icon class="impulse-place-switcher-chevron">expand_more</mat-icon>
      </button>
    } @else if (alone()) {
      <span
        class="impulse-place-switcher-place impulse-place-switcher-alone"
        [attr.aria-label]="(place().id ? word.one + ': ' : '') + place().label">
        <mat-icon class="impulse-place-switcher-kind">{{ place().id ? 'domain' : outside.icon }}</mat-icon>
        <span class="impulse-place-switcher-lines">
          @if (place().id) {
            <span class="impulse-place-switcher-caption">{{ word.one }}</span>
          }
          <span class="impulse-place-switcher-name">{{ place().label }}</span>
        </span>
      </span>
    }

    <ng-template #list>
      <impulse-menu-list class="impulse-place-switcher-places" [groups]="groups()">
        @if (findable()) {
          <div class="impulse-place-switcher-find">
            <mat-icon>search</mat-icon>
            <input
              #find
              type="search"
              [placeholder]="'Find a ' + lower(word.one)"
              [attr.aria-label]="'Find a ' + lower(word.one)"
              autocomplete="off"
              [value]="filter()"
              (input)="filter.set(find.value)"
              (keydown)="findKey($event)" />
          </div>
          @if (!groups().length && !closed().length) {
            <div class="impulse-place-switcher-none">No {{ lower(word.one) }} has that name</div>
          }
        }
        @if (closed().length) {
          <div menuListEnd class="impulse-place-switcher-closed" role="group" aria-label="Not available">
            <div class="impulse-place-switcher-closed-heading">Not available</div>
            @for (place of closed(); track place.id) {
              <div class="impulse-place-switcher-closed-place">
                <mat-icon class="impulse-place-switcher-closed-icon">lock</mat-icon>
                <span class="impulse-place-switcher-closed-name">{{ place.label }}</span>
                <span class="impulse-place-switcher-closed-reason">{{ place.reason }}</span>
              </div>
            }
          </div>
        }
      </impulse-menu-list>
    </ng-template>
  `,
  styleUrl: './place-switcher.component.scss',
})
export class PlaceSwitcherComponent {
  readonly places = inject(PlaceService);
  private frame = inject(Frame);
  private find = viewChild<ElementRef<HTMLInputElement>>('find');

  /** What the application calls a tenant. */
  readonly word = this.frame.tenantWord;
  /** The place outside every tenant, which needs no caption: its name says it. */
  readonly outside = this.frame.outside;
  readonly lower = inSentence;

  readonly place = this.places.current;
  /** Whether the page is in one of the person's places. */
  readonly inPlace = computed(() => this.places.places().some((place) => place.id === this.place().id));
  /**
   * Whether there is a list to open: more than one place, a way back to the one from a
   * page in none, or a tenant that is closed to this sign-in, which the list names. Never
   * before the person's places are known: a list of tenants that has not been read, or
   * could not be, is not a person with no tenant to switch to.
   */
  readonly listed = computed(() => {
    if (!this.places.known()) {
      return false;
    }
    const count = this.places.places().length;
    return count > 1 || (count === 1 && !this.inPlace()) || this.places.closed().length > 0;
  });
  /** Whether the person has one place and nothing to switch to, once their places are known. */
  readonly alone = computed(() => this.places.known() && this.places.places().length === 1);
  readonly filter = signal('');
  readonly findable = computed(() => this.places.places().length + this.places.closed().length >= findFrom);

  private matches = computed(() => {
    const words = this.filter().trim().toLowerCase();
    return this.places.places().filter((place) => !words || place.label.toLowerCase().includes(words));
  });

  /** The tenants that are closed to this sign-in, narrowed as the places are. */
  readonly closed = computed<readonly ClosedPlace[]>(() => {
    const words = this.filter().trim().toLowerCase();
    return this.places.closed().filter((place) => !words || place.label.toLowerCase().includes(words));
  });

  /** The place outside every tenant apart, then the tenants. */
  readonly groups = computed<readonly ShownGroup[]>(() => {
    const current = this.place().id;
    const entry = (place: Place): ShownEntry => ({
      label: place.label,
      icon: place.id ? 'domain' : this.outside.icon,
      checked: place.id === current,
      disabled: false,
      run: (): void => void this.places.open(place),
    });
    const outside = this.matches().filter((place) => !place.id);
    const tenants = this.matches().filter((place) => place.id);
    return [
      ...(outside.length ? [{ entries: outside.map(entry) }] : []),
      ...(tenants.length ? [{ label: this.word.many, entries: tenants.map(entry) }] : []),
    ];
  });

  /** The list opened: an empty box, ready for typing. */
  opened(): void {
    this.filter.set('');
    setTimeout(() => this.find()?.nativeElement.focus());
  }

  /**
   * Typing stays in the box: only the keys that move through the menu or close it reach
   * the menu. Enter opens the first match.
   */
  findKey(event: KeyboardEvent): void {
    if (event.key === 'Enter') {
      event.preventDefault();
      const first = this.matches()[0];
      if (first) {
        void this.places.open(first);
        (event.target as HTMLElement).dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
      }
      return;
    }
    if (['ArrowDown', 'ArrowUp', 'Escape', 'Tab'].includes(event.key)) {
      if (event.key.startsWith('Arrow')) event.preventDefault();
      return;
    }
    event.stopPropagation();
  }
}
