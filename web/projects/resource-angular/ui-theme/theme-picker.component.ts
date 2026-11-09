import { Component, inject } from '@angular/core';
import { MatButtonToggleChange, MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatRadioChange, MatRadioModule } from '@angular/material/radio';
import { ColorScheme, ThemeService } from './theme.service';

/**
 * The control for the look a person chooses, for the page an application keeps a person's
 * own settings on: Light or Dark, and, when the application offers more than one palette,
 * the palettes by name, each with a swatch of its primary color. A choice applies as it is
 * made and is remembered by ThemeService; there is nothing to save.
 *
 *     <impulse-theme-picker />
 */
@Component({
  selector: 'impulse-theme-picker',
  imports: [MatButtonToggleModule, MatRadioModule],
  templateUrl: './theme-picker.component.html',
  styleUrl: './theme-picker.component.scss',
})
export class ThemePickerComponent {
  theme = inject(ThemeService);

  chooseScheme(change: MatButtonToggleChange): void {
    this.theme.setScheme(change.value as ColorScheme);
  }

  choosePalette(change: MatRadioChange): void {
    this.theme.setPalette(String(change.value));
  }
}
