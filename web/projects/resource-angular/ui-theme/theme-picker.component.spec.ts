import { ComponentFixture, TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { provideTheme } from './provide-theme';
import { ThemePickerComponent } from './theme-picker.component';
import { THEME_STORAGE_KEY, ThemePalette, ThemeService } from './theme.service';

// The picker over the service: the schemes and the palettes it renders, a click applying
// and remembering the choice, and the palettes left out where there is no choice of them.

const palettes: ThemePalette[] = [
  { id: 'blue', label: 'Blue' },
  { id: 'workspace', label: 'Workspace' },
];

interface Rendered {
  fixture: ComponentFixture<ThemePickerComponent>;
  element: HTMLElement;
  theme: ThemeService;
}

function render(offered: readonly ThemePalette[]): Rendered {
  TestBed.configureTestingModule({
    imports: [ThemePickerComponent],
    providers: [provideTheme({ palettes: offered })],
  });
  const fixture = TestBed.createComponent(ThemePickerComponent);
  fixture.detectChanges();
  return { fixture, element: fixture.nativeElement as HTMLElement, theme: TestBed.inject(ThemeService) };
}

const texts = (element: HTMLElement, selector: string): (string | undefined)[] =>
  [...element.querySelectorAll(selector)].map((node) => node.textContent?.trim());

describe('ThemePickerComponent', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    localStorage.clear();
    document.documentElement.removeAttribute('data-palette');
    document.documentElement.style.removeProperty('color-scheme');
  });

  it('offers light and dark, and the palettes with their swatches', () => {
    const { element } = render(palettes);
    expect(texts(element, 'mat-button-toggle')).toEqual(['Light', 'Dark']);
    expect(texts(element, 'mat-radio-button')).toEqual(['Blue', 'Workspace']);
    const swatches = [...element.querySelectorAll('.swatch')].map((node) => node.getAttribute('data-impulse-swatch'));
    expect(swatches).toEqual(['blue', 'workspace']);
  });

  it('applies and remembers what is clicked', () => {
    const { element, fixture, theme } = render(palettes);
    (element.querySelector('mat-button-toggle[value="dark"] button') as HTMLElement).click();
    fixture.detectChanges();
    expect(theme.scheme()).toBe('dark');
    expect(document.documentElement.style.getPropertyValue('color-scheme')).toBe('dark');
    (element.querySelectorAll('mat-radio-button input')[1] as HTMLElement).click();
    fixture.detectChanges();
    expect(theme.palette()).toBe('workspace');
    expect(document.documentElement.getAttribute('data-palette')).toBe('workspace');
    expect(JSON.parse(localStorage.getItem(THEME_STORAGE_KEY) ?? 'null')).toEqual({
      scheme: 'dark',
      palette: 'workspace',
    });
  });

  const alone: { name: string; offered: ThemePalette[] }[] = [
    { name: 'one palette', offered: [palettes[0]] },
    { name: 'no palette', offered: [] },
  ];
  for (const tt of alone) {
    it(`offers light and dark alone with ${tt.name}`, () => {
      const { element } = render(tt.offered);
      expect(texts(element, 'mat-button-toggle')).toEqual(['Light', 'Dark']);
      expect(element.querySelector('mat-radio-group')).toBeNull();
    });
  }
});
