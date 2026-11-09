import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { provideTheme } from './provide-theme';
import { THEME_STORAGE_KEY, ThemePalette, ThemeService } from './theme.service';

// The service over the real document and the browser's local storage (jsdom's): the
// defaults, a remembered choice applied at start, what is no longer offered falling back
// to them, a change applied and remembered, and a storage the browser refuses tolerated.

const palettes: ThemePalette[] = [
  { id: 'blue', label: 'Blue' },
  { id: 'workspace', label: 'Workspace' },
];

const root = (): HTMLElement => document.documentElement;
const remembered = (): unknown => JSON.parse(localStorage.getItem(THEME_STORAGE_KEY) ?? 'null');

/** Configures the application with the palettes and starts the service, as provideTheme does at bootstrap. */
function start(offered: readonly ThemePalette[] = palettes): ThemeService {
  TestBed.configureTestingModule({ providers: [provideTheme({ palettes: offered })] });
  return TestBed.inject(ThemeService);
}

describe('ThemeService', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
    root().removeAttribute('data-palette');
    root().style.removeProperty('color-scheme');
  });

  it('starts light on the first palette when nothing is remembered', () => {
    const theme = start();
    expect(theme.scheme()).toBe('light');
    expect(theme.palette()).toBe('blue');
    expect(root().style.getPropertyValue('color-scheme')).toBe('light');
    expect(root().getAttribute('data-palette')).toBe('blue');
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBeNull();
  });

  it('applies the remembered choice at start', () => {
    localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify({ scheme: 'dark', palette: 'workspace' }));
    const theme = start();
    expect(theme.scheme()).toBe('dark');
    expect(theme.palette()).toBe('workspace');
    expect(root().style.getPropertyValue('color-scheme')).toBe('dark');
    expect(root().getAttribute('data-palette')).toBe('workspace');
  });

  const fallbacks: { name: string; raw: string; scheme: string; palette: string }[] = [
    {
      name: 'a palette no longer offered',
      raw: JSON.stringify({ scheme: 'dark', palette: 'violet' }),
      scheme: 'dark',
      palette: 'blue',
    },
    {
      name: 'a scheme that is not one',
      raw: JSON.stringify({ scheme: 'sepia', palette: 'workspace' }),
      scheme: 'light',
      palette: 'workspace',
    },
    { name: 'something that is not a choice', raw: '"dark"', scheme: 'light', palette: 'blue' },
    { name: 'text that is not JSON', raw: '{dark', scheme: 'light', palette: 'blue' },
  ];
  for (const tt of fallbacks) {
    it(`keeps the defaults for ${tt.name}`, () => {
      localStorage.setItem(THEME_STORAGE_KEY, tt.raw);
      const theme = start();
      expect(theme.scheme()).toBe(tt.scheme);
      expect(theme.palette()).toBe(tt.palette);
    });
  }

  it('applies and remembers a change', () => {
    const theme = start();
    theme.setScheme('dark');
    expect(root().style.getPropertyValue('color-scheme')).toBe('dark');
    expect(remembered()).toEqual({ scheme: 'dark', palette: 'blue' });
    theme.setPalette('workspace');
    expect(root().getAttribute('data-palette')).toBe('workspace');
    expect(remembered()).toEqual({ scheme: 'dark', palette: 'workspace' });
  });

  it('ignores a palette the application does not offer', () => {
    const theme = start();
    theme.setPalette('violet');
    expect(theme.palette()).toBe('blue');
    expect(root().getAttribute('data-palette')).toBe('blue');
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBeNull();
  });

  it('offers no palette when the application names none', () => {
    const theme = start([]);
    expect(theme.palette()).toBeUndefined();
    expect(root().hasAttribute('data-palette')).toBe(false);
    theme.setPalette('blue');
    expect(theme.palette()).toBeUndefined();
    theme.setScheme('dark');
    expect(remembered()).toEqual({ scheme: 'dark' });
  });

  it('works without storage', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('refused');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('refused');
    });
    const theme = start();
    expect(theme.scheme()).toBe('light');
    theme.setScheme('dark');
    expect(theme.scheme()).toBe('dark');
    expect(root().style.getPropertyValue('color-scheme')).toBe('dark');
  });
});
