import { MatDialog, MatDialogConfig, MatDialogRef } from '@angular/material/dialog';
import { ChangeHistoryDialogComponent, ChangeHistoryDialogData } from './change-history-dialog.component';

/**
 * The pop-up's default size: on a desk's screen, room for each of its columns at the
 * narrowest it is drawn, so the table scrolls sideways only on a narrower screen. A
 * config passed to openChangeHistory overrides any of it.
 */
const changeHistoryDefaults: MatDialogConfig = { width: '960px', maxWidth: '95vw' };

/**
 * Opens one record's history over the page: each change, newest first, with when, by
 * whom, and each field it changed from what to what. Something a person only reads, so it
 * is a pop-up and the address does not change. A list opens it from a row's button
 * (`rowButtons`), which the record's page draws too; the caller names the title, reads the
 * record's history through its handle's `changes`, and names the page's fields with their
 * labels.
 */
export function openChangeHistory(
  dialog: MatDialog,
  options: ChangeHistoryDialogData,
  config: MatDialogConfig = {},
): MatDialogRef<ChangeHistoryDialogComponent> {
  return dialog.open(ChangeHistoryDialogComponent, { ...changeHistoryDefaults, ...config, data: options });
}
