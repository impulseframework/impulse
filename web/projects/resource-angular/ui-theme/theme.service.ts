import { DOCUMENT, inject, Injectable, InjectionToken, signal } from '@angular/core';

/**
 * The three schemes a person chooses between: light, dark, and their device's (`device`),
 * under which the page is drawn light or dark as the device it is on is set.
 */
export type ColorScheme = 'light' | 'dark' | 'device';

/** Whether a value read from elsewhere is one of the schemes. */
function isScheme(value: unknown): value is ColorScheme {
  return value === 'light' || value === 'dark' || value === 'device';
}

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
 * picker then offers Light, Dark and Match my device alone. provideTheme sets it.
 */
export const THEME_PALETTES = new InjectionToken<readonly ThemePalette[]>('THEME_PALETTES', {
  providedIn: 'root',
  factory: () => [],
});

/**
 * A look as it is kept outside the browser, with the person's account: light, dark or the
 * device's, and a palette's id. Either half may be absent, for none chosen.
 */
export interface ThemeChoice {
  readonly scheme?: ColorScheme;
  readonly palette?: string;
}

/** The key the choice is remembered under in the browser's local storage. */
export const THEME_STORAGE_KEY = 'impulse-theme';

/** What the browser remembers, as read back: anything, until checked. */
interface StoredChoice {
  scheme?: unknown;
  palette?: unknown;
}

/**
 * The look a person chose: light, dark or as their device is set, and one of the palettes
 * the application offers. The service applies the choice to the root element,
 * `color-scheme` as an inline style (the theme emits both values of every Material token,
 * so the one property flips the whole page; for the device's it is `light dark`, under
 * which the browser draws whichever the device is set to and follows a change of that
 * setting while the page is open) and `data-palette` (the attribute the theme sheet's `palettes` mixin emits each
 * palette's tokens under), and remembers it in the browser's local storage, so the login
 * page honors it before sign-in and the next visit starts with it. provideTheme calls
 * start() as the application starts. A choice the browser remembers but the application no
 * longer offers, a storage the browser refuses, and a value that is not one of the choices
 * all leave the defaults in force: light, and the first palette.
 *
 * The browser's copy is one browser's. An application that keeps the look with the
 * person's account, so it follows them to another device, reads `chosen`, the person's own
 * last change on this page, and hands the look the account holds to `adopt`; the frame
 * does both (provideFrame).
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
  /** The scheme in force: light, dark, or the device's, which the browser draws as its device is set. */
  readonly scheme = this.schemeSignal.asReadonly();
  /** The id of the palette in force, undefined when the application offers none. */
  readonly palette = this.paletteSignal.asReadonly();
  private chosenSignal = signal<ThemeChoice | undefined>(undefined);
  /**
   * The look as the person last changed it on this page, undefined until they change it:
   * the whole look in force after the change, for whoever keeps it beside the browser. A
   * look taken from elsewhere (adopt) and the one the browser remembered are not changes.
   */
  readonly chosen = this.chosenSignal.asReadonly();

  /** Reads the remembered choice, keeps what is still offered, and applies the result. */
  start(): void {
    const stored = this.read();
    if (isScheme(stored.scheme)) {
      this.schemeSignal.set(stored.scheme);
    }
    if (typeof stored.palette === 'string' && this.offers(stored.palette)) {
      this.paletteSignal.set(stored.palette);
    }
    this.apply();
  }

  /** Switches to light, dark or the device's, applies it, and remembers the choice. */
  setScheme(scheme: ColorScheme): void {
    if (!isScheme(scheme)) {
      return;
    }
    this.schemeSignal.set(scheme);
    this.apply();
    this.remember();
    this.changed();
  }

  /** Switches to one of the offered palettes by id, applies it, and remembers the choice; an id not offered changes nothing. */
  setPalette(id: string): void {
    if (!this.offers(id)) {
      return;
    }
    this.paletteSignal.set(id);
    this.apply();
    this.remember();
    this.changed();
  }

  /**
   * Takes a look kept elsewhere for the person, their account's: each half that is one of
   * the choices is applied and remembered in the browser, and a half that is absent, or a
   * palette the application does not offer, leaves what is in force. It is not a change
   * the person made here, so `chosen` does not move.
   */
  adopt(choice: ThemeChoice): void {
    let moved = false;
    if (isScheme(choice.scheme) && choice.scheme !== this.schemeSignal()) {
      this.schemeSignal.set(choice.scheme);
      moved = true;
    }
    if (typeof choice.palette === 'string' && this.offers(choice.palette) && choice.palette !== this.paletteSignal()) {
      this.paletteSignal.set(choice.palette);
      moved = true;
    }
    if (moved) {
      this.apply();
      this.remember();
    }
  }

  private changed(): void {
    this.chosenSignal.set({ scheme: this.schemeSignal(), palette: this.paletteSignal() });
  }

  private offers(id: string): boolean {
    return this.palettes.some((palette) => palette.id === id);
  }

  private apply(): void {
    const root = this.document.documentElement;
    const scheme = this.schemeSignal();
    root.style.setProperty('color-scheme', scheme === 'device' ? 'light dark' : scheme);
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
