import { DOCUMENT, inject, Injectable, InjectionToken, signal } from '@angular/core';

/** The two schemes a person chooses between. */
export type ColorScheme = 'light' | 'dark';

/**
 * One palette an application offers: `id` is the key of the application's palette map
 * (the `palettes` mixin of the theme sheet emits its tokens under `html[data-palette='<id>']`),
 * `label` the name the picker shows beside its swatch.
 */
export interface ThemePalette {
  id: string;
  label: string;
}

/**
 * The palettes the application offers, in the order the picker shows them; the first is
 * the default. Empty, the default, when the application offers no choice of colors: the
 * picker then offers light and dark alone. provideTheme sets it.
 */
export const THEME_PALETTES = new InjectionToken<readonly ThemePalette[]>('THEME_PALETTES', {
  providedIn: 'root',
  factory: () => [],
});

/** The key the choice is remembered under in the browser's local storage. */
export const THEME_STORAGE_KEY = 'impulse-theme';

/** What the browser remembers, as read back: anything, until checked. */
interface StoredChoice {
  scheme?: unknown;
  palette?: unknown;
}

/**
 * The look a person chose: light or dark, and one of the palettes the application offers.
 * The service applies the choice to the root element, `color-scheme` as an inline style
 * (the theme emits both values of every Material token, so the one property flips the whole
 * page) and `data-palette` (the attribute the theme sheet's `palettes` mixin emits each
 * palette's tokens under), and remembers it in the browser's local storage, so the login
 * page honors it before sign-in and the next visit starts with it. provideTheme calls
 * start() as the application starts. A choice the browser remembers but the application no
 * longer offers, a storage the browser refuses, and a value that is not one of the choices
 * all leave the defaults in force: light, and the first palette.
 */
@Injectable({
  providedIn: 'root',
})
export class ThemeService {
  /** The palettes the application offers, as provided. */
  readonly palettes = inject(THEME_PALETTES);
  private document = inject(DOCUMENT);
  private schemeSignal = signal<ColorScheme>('light');
  private paletteSignal = signal<string | undefined>(this.palettes[0]?.id);
  /** The scheme in force. */
  readonly scheme = this.schemeSignal.asReadonly();
  /** The id of the palette in force, undefined when the application offers none. */
  readonly palette = this.paletteSignal.asReadonly();

  /** Reads the remembered choice, keeps what is still offered, and applies the result. */
  start(): void {
    const stored = this.read();
    if (stored.scheme === 'light' || stored.scheme === 'dark') {
      this.schemeSignal.set(stored.scheme);
    }
    if (typeof stored.palette === 'string' && this.offers(stored.palette)) {
      this.paletteSignal.set(stored.palette);
    }
    this.apply();
  }

  /** Switches to light or dark, applies it, and remembers the choice. */
  setScheme(scheme: ColorScheme): void {
    if (scheme !== 'light' && scheme !== 'dark') {
      return;
    }
    this.schemeSignal.set(scheme);
    this.apply();
    this.remember();
  }

  /** Switches to one of the offered palettes by id, applies it, and remembers the choice; an id not offered changes nothing. */
  setPalette(id: string): void {
    if (!this.offers(id)) {
      return;
    }
    this.paletteSignal.set(id);
    this.apply();
    this.remember();
  }

  private offers(id: string): boolean {
    return this.palettes.some((palette) => palette.id === id);
  }

  private apply(): void {
    const root = this.document.documentElement;
    root.style.setProperty('color-scheme', this.schemeSignal());
    const palette = this.paletteSignal();
    if (palette === undefined) {
      root.removeAttribute('data-palette');
    } else {
      root.setAttribute('data-palette', palette);
    }
  }

  private read(): StoredChoice {
    try {
      const raw = this.document.defaultView?.localStorage.getItem(THEME_STORAGE_KEY);
      if (!raw) {
        return {};
      }
      const parsed: unknown = JSON.parse(raw);
      return parsed !== null && typeof parsed === 'object' ? (parsed as StoredChoice) : {};
    } catch {
      return {};
    }
  }

  private remember(): void {
    try {
      const choice = JSON.stringify({ scheme: this.schemeSignal(), palette: this.paletteSignal() });
      this.document.defaultView?.localStorage.setItem(THEME_STORAGE_KEY, choice);
    } catch {
      // The browser refuses storage: the choice holds for this page and is asked again next time.
    }
  }
}
