import { MatDialog, MatDialogConfig, MatDialogRef } from '@angular/material/dialog';
import { FeatureFlagHistoryData, FeatureFlagHistoryDialogComponent } from './feature-flag-history-dialog.component';

/** The pop-up's default size; a config passed to openFeatureFlagHistory overrides any of it. */
export const featureFlagHistoryDefaults: MatDialogConfig = { width: '560px', maxWidth: '95vw' };

/**
 * Opens one flag's history over the page: each time the flag was turned on or off, newest
 * first, with who did it. Something a person only reads, so it is a pop-up and the address
 * does not change. The Feature flags page opens it from a row's History button; the flag is
 * named as the server names it, with the words the page lists it by for the title.
 */
export function openFeatureFlagHistory(
  dialog: MatDialog,
  flag: FeatureFlagHistoryData,
  config: MatDialogConfig = {},
): MatDialogRef<FeatureFlagHistoryDialogComponent> {
  return dialog.open(FeatureFlagHistoryDialogComponent, { ...featureFlagHistoryDefaults, ...config, data: flag });
}
