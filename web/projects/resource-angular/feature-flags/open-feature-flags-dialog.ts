import { MatDialog, MatDialogConfig, MatDialogRef } from '@angular/material/dialog';
import { FeatureFlagsDialogComponent } from './feature-flags-dialog.component';

/** The dialog's default size; a config passed to openFeatureFlagsDialog overrides any of it. */
export const featureFlagsDialogDefaults: MatDialogConfig = { width: '560px', maxWidth: '95vw' };

/**
 * Opens the feature flags dialog over the application, from wherever the application
 * puts the link: a menu item, a toolbar button, a settings page. The application passes
 * its MatDialog, so the call works from an event handler as well as from an injection
 * context, and may pass a dialog config of its own.
 *
 *     constructor(private dialog: MatDialog) {}
 *     openFlags(): void { openFeatureFlagsDialog(this.dialog); }
 */
export function openFeatureFlagsDialog(
  dialog: MatDialog,
  config: MatDialogConfig = {},
): MatDialogRef<FeatureFlagsDialogComponent> {
  return dialog.open(FeatureFlagsDialogComponent, { ...featureFlagsDialogDefaults, ...config });
}
