import { afterNextRender, Component, ElementRef, inject, Injector, viewChild } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialog, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { ChildResourceConfig, RecordData } from '@impulseframework/resource-angular/types';
import { ResourceCreateComponent } from '../resource-create/resource-create.component';

/** What the dialog of a list's create is opened with. */
export interface ResourceCreateDialogData {
  /** The dialog's title and the words on the button that creates: the label of the button that opened it. */
  label: string;
  /** The config the form is drawn from: the list's `createConfig` where it has one, else the list's own. */
  config: ChildResourceConfig;
  /**
   * The row the list sits under, whose key fills the new row's reference to it; empty
   * for a list that is a page of its own.
   */
  parentData: RecordData;
  /** Whether the new row's page opens once the row is created (the list's `loadCreatedResource`). */
  loadCreatedResource: boolean;
}

/**
 * The dialog every list's create opens in: the create form, over the page. It is titled
 * with the label of the button that opened it and carries Cancel and that label as its
 * buttons, as the dialog of a method does. A create the server refuses is shown here in
 * the server's words and the dialog stays open as it was filled; it closes with true
 * once the row is created, and with false when it is canceled, by its button or by
 * Escape. A click outside it does nothing, so what was typed is not lost to a stray
 * click.
 */
@Component({
  selector: 'impulse-resource-create-dialog',
  imports: [MatDialogModule, MatButtonModule, ResourceCreateComponent],
  templateUrl: './resource-create-dialog.component.html',
  styleUrl: './resource-create-dialog.component.scss',
})
export class ResourceCreateDialogComponent {
  private readonly dialogRef = inject<MatDialogRef<ResourceCreateDialogComponent, boolean>>(MatDialogRef);
  readonly data = inject<ResourceCreateDialogData>(MAT_DIALOG_DATA);

  private readonly injector = inject(Injector);

  /** The create form the dialog draws. */
  private readonly form = viewChild(ResourceCreateComponent);
  /** The sentence about a create the form did not send, while the dialog shows it. */
  private readonly said = viewChild<ElementRef<HTMLElement>>('said');
  /** Set once Cancel or Escape leaves the dialog, so its closing says no row was created. */
  private canceled = false;

  constructor() {
    this.dialogRef
      .keydownEvents()
      .pipe(takeUntilDestroyed())
      .subscribe((event) => {
        if (event.key === 'Escape') {
          event.preventDefault();
          this.cancel();
        }
      });
  }

  /** Whether nothing is filled yet: the create button waits for the first input. */
  pristine(): boolean {
    return !(this.form()?.isDirty() ?? false);
  }

  /** Whether the create is on its way: both buttons wait, and a second click does nothing. */
  saving(): boolean {
    return this.form()?.saving() ?? false;
  }

  /**
   * What the form says about a create asked for while a field was missing or wrong, a
   * refused field that is not on the form named in it; empty once every field holds.
   */
  incomplete(): string {
    const form = this.form();
    return form !== undefined && form.submitted() && !form.form().valid ? form.incomplete() : '';
  }

  /** The server's refusal of the last create, in its words; empty while there is none. */
  refusal(): string {
    return this.form()?.refusal() ?? '';
  }

  /**
   * Sends the create. The form closes the dialog once the row is created. Where the form
   * does not send it and has no refused field to bring into view (each one it refuses is
   * not on the form), the sentence that names them is what the person must see: it sits
   * under the fields, so it is brought into view in what the dialog scrolls.
   */
  create(): void {
    if (this.saving()) {
      return;
    }
    const form = this.form();
    form?.saveForm();
    if (form?.nothingRefusedIsDrawn() && this.incomplete() !== '') {
      afterNextRender(
        () => {
          this.said()?.nativeElement.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
        },
        { injector: this.injector },
      );
    }
  }

  /** Leaves the dialog with no row created; what was typed is dropped. */
  cancel(): void {
    if (this.saving()) {
      return;
    }
    this.canceled = true;
    const form = this.form();
    if (!form) {
      this.dialogRef.close(false);
      return;
    }
    form.cancelForm();
  }

  /** The form is done: it created the row, or it was canceled. */
  close(): void {
    this.dialogRef.close(!this.canceled);
  }
}

/**
 * Opens the dialog of a list's create. The form reads the page it was opened from (the
 * page's config on its route, and the tenant), so the dialog is made under `injector`,
 * the list's own. The answer is the dialog's: true once a row was created, false when
 * the dialog was canceled.
 */
export function openCreateDialog(
  dialog: MatDialog,
  data: ResourceCreateDialogData,
  injector: Injector,
): MatDialogRef<ResourceCreateDialogComponent, boolean> {
  return dialog.open<ResourceCreateDialogComponent, ResourceCreateDialogData, boolean>(ResourceCreateDialogComponent, {
    data,
    injector,
    delayFocusTrap: false,
    disableClose: true,
  });
}
