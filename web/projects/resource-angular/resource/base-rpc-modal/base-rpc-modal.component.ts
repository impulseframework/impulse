import { Component, computed, inject, signal } from '@angular/core';
import { FormControl, FormGroup, FormsModule, ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogActions, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatInputModule } from '@angular/material/input';
import { DataType, FieldElement, Method, METHOD_META, RPCBaseFormData } from '@impulseframework/resource-angular/types';
import { flattenElements } from '../gui-constants';
import { ResourceLayoutComponent } from '../resource-layout/resource-layout.component';
import { ResourceStore } from '../resource-store.service';

@Component({
  selector: 'impulse-base-rpc-modal',
  imports: [
    MatDialogModule,
    MatDialogActions,
    MatButtonModule,
    ResourceLayoutComponent,
    FormsModule,
    MatInputModule,
    ReactiveFormsModule,
  ],
  templateUrl: './base-rpc-modal.component.html',
  styleUrls: ['./base-rpc-modal.component.scss'],
  providers: [ResourceStore],
})
export class BaseRPCModalComponent {
  methodMeta = inject(METHOD_META);
  private dialogRef = inject(MatDialogRef);

  data = inject<{ elements: FieldElement[]; label: string; method: Method; width: string }>(MAT_DIALOG_DATA);
  formData = signal({} as RPCBaseFormData);
  meta = computed(() => {
    return this.methodMeta(this.formData().method as Method);
  });

  constructor() {
    this.formData.set(this.data);
    if (this.data.width) {
      this.dialogRef.updateSize(this.data.width);
    }
  }

  onConfirm(): void {
    this.dialogRef.close(this.form());
  }

  form = computed(() => {
    const fg = new FormGroup({});
    this.formData();
    const meta = this.meta();
    const allElements = flattenElements(this.data.elements);
    if (meta) {
      for (const field of meta.fields || []) {
        // A field that holds a list starts as an empty list: the control of several
        // picked keys reads and writes a list.
        const value: string | string[] = field.displayType.endsWith('[]') ? [] : '';
        const control = new FormControl(value);
        const findConfig = allElements.find((element) => element.type === 'field' && element.name === field.fieldName);
        const fieldConfig = findConfig as FieldElement | undefined;
        if (!fieldConfig) {
          continue;
        }
        if (fieldConfig.validators.length > 0) {
          control.setValidators(fieldConfig.validators);
        }
        fg.addControl(field.fieldName, control);
        this.pristineForm[field.fieldName] = value;
      }
    }
    return fg;
  });

  pristineForm: Record<string, DataType> = {};
}
