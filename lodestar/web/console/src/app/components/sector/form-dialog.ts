import { TemplateRef } from '@angular/core';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';

/**
 * Opens one of a deck's own forms in a dialog over the page, as the library's lists open
 * their create. The dialog takes the focus as it opens. Escape closes it, as the form's
 * Cancel does, with nothing sent; a click outside does nothing, so what was typed is not
 * lost to a stray click. It is as wide as a form needs and never wider than the window.
 */
export function openFormDialog(dialog: MatDialog, form: TemplateRef<unknown>): MatDialogRef<unknown> {
  const opened = dialog.open(form, { delayFocusTrap: false, disableClose: true, width: '480px', maxWidth: '95vw' });
  opened.keydownEvents().subscribe((event) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      opened.close();
    }
  });
  return opened;
}
