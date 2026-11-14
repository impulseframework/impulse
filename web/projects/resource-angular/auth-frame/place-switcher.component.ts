import { CdkMenuTrigger } from '@angular/cdk/menu';
import { ChangeDetectionStrategy, Component, computed, ElementRef, inject, signal, viewChild } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { Frame, inSentence } from './frame';
import { ShownEntry, ShownGroup } from './menu';
import { MenuListComponent } from './menu-list.component';
import { Place, PlaceService } from './place.service';

/** More places than this and the list opens with a box to find one by name. */
export const findFrom = 8;

/**
 * The place the page is in, beside the application's name, and the way to another: the
 * place outside every tenant for those who hold something there, then each tenant by name,
 * the open one checked. A person with one place sees its name and nothing to open. A long
 * list opens with a box that narrows it as they type; Enter opens the first match.
 */
@Component({
  selector: 'impulse-place-switcher',
  imports: [CdkMenuTrigger, MatIconModule, MenuListComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (places.places().length > 1) {
      <button
        type="button"
        class="switcher"
        [cdkMenuTriggerFor]="list"
        (cdkMenuOpened)="opened()"
        [attr.aria-label]="'In ' + place().label + '. Switch to another ' + lower(word.one)">
        <mat-icon class="kind">{{ place().id ? 'domain' : outside.icon }}</mat-icon>
        <span class="lines">
          @if (place().id) {
            <span class="caption">{{ word.one }}</span>
          }
          <span class="name">{{ place().label }}</span>
        </span>
        <mat-icon class="chevron">expand_more</mat-icon>
      </button>
    } @else if (places.places().length === 1) {
      <span class="switcher alone" [attr.aria-label]="(place().id ? word.one + ': ' : '') + place().label">
        <mat-icon class="kind">{{ place().id ? 'domain' : outside.icon }}</mat-icon>
        <span class="lines">
          @if (place().id) {
            <span class="caption">{{ word.one }}</span>
          }
          <span class="name">{{ place().label }}</span>
        </span>
      </span>
    }

    <ng-template #list>
      <impulse-menu-list class="places" [groups]="groups()">
        @if (findable()) {
          <div class="find">
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
          @if (!groups().length) {
            <div class="none">No {{ lower(word.one) }} has that name</div>
          }
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
  readonly filter = signal('');
  readonly findable = computed(() => this.places.places().length >= findFrom);

  private matches = computed(() => {
    const words = this.filter().trim().toLowerCase();
    return this.places.places().filter((place) => !words || place.label.toLowerCase().includes(words));
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
