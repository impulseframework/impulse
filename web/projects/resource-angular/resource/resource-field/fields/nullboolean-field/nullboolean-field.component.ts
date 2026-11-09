import { ChangeDetectionStrategy, Component, computed } from '@angular/core';
import { ReactiveFormsModule, Validators } from '@angular/forms';
import { MatAutocompleteModule } from '@angular/material/autocomplete';
import { MatButtonModule } from '@angular/material/button';
import { MatOptionModule } from '@angular/material/core';
import { MatDatepickerModule } from '@angular/material/datepicker';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterModule } from '@angular/router';
import { NullBoolean } from '@impulseframework/resource-angular/types';
import { BaseInputComponent } from '../../base-field.directive';

@Component({
  selector: 'impulse-nullboolean-field',
  imports: [
    MatFormFieldModule,
    MatInputModule,
    MatDatepickerModule,
    ReactiveFormsModule,
    MatOptionModule,
    MatSelectModule,
    MatIconModule,
    MatButtonModule,
    MatTooltipModule,
    RouterModule,
    MatAutocompleteModule,
  ],
  templateUrl: './nullboolean-field.component.html',
  styleUrl: './nullboolean-field.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class NullBooleanFieldComponent extends BaseInputComponent {
  availableEnumOptions = computed(() => {
    const fieldConfig = this.fieldConfig();
    if (fieldConfig === undefined) {
      return;
    }
    const valueMap = new Map<NullBoolean, string>();

    Object.values(fieldConfig.nullBooleanConfig.displayValues).forEach((value) => {
      valueMap.set(value.value, value.label);
    });

    return valueMap;
  });

  currentValue = computed(() => {
    const form = this.form();
    const fieldConfig = this.fieldConfig();

    if (form === undefined || fieldConfig === undefined) {
      return;
    }

    return form.get(fieldConfig.name)?.value;
  });

  currentDisplayText = computed(() => {
    const value = this.currentValue();
    const availableOptions = this.availableEnumOptions();

    if (availableOptions === undefined || value === undefined) {
      return;
    }

    return availableOptions.get(value);
  });

  hasRequiredValidator = computed(() => {
    const form = this.form();
    const fieldConfig = this.fieldConfig();

    if (form === undefined || fieldConfig === undefined) {
      return false;
    }

    const control = form.get(fieldConfig.name);
    if (control === null) {
      return false;
    }

    return control.hasValidator(Validators.required);
  });
}
