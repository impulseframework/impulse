import { EnvironmentProviders, inject, makeEnvironmentProviders, provideEnvironmentInitializer } from '@angular/core';
import { THEME_PALETTES, ThemePalette, ThemeService } from './theme.service';

/** What provideTheme takes: the palettes the application offers, in the order shown; the first is the default. */
export interface ThemeOptions {
  palettes?: readonly ThemePalette[];
}

/**
 * Starts ThemeService with the application, so the look the browser remembers is applied
 * before the first page renders, and names the palettes the application offers (the ids of
 * its theme's palette map): add it to the application's providers. The picker,
 * `<impulse-theme-picker>`, goes on whichever page the application keeps a person's own
 * settings on.
 */
export function provideTheme(options: ThemeOptions = {}): EnvironmentProviders {
  return makeEnvironmentProviders([
    { provide: THEME_PALETTES, useValue: options.palettes ?? [] },
    provideEnvironmentInitializer(() => inject(ThemeService).start()),
  ]);
}
