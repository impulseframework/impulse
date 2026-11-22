import { Component, computed } from '@angular/core';
import { ReactiveFormsModule } from '@angular/forms';
import { MatDatepickerModule } from '@angular/material/datepicker';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { BaseInputComponent } from '../../base-field.directive';
import { DecimalInputDirective } from './decimal-input.directive';

@Component({
  selector: 'impulse-number-field',
  imports: [MatFormFieldModule, MatInputModule, MatDatepickerModule, ReactiveFormsModule, DecimalInputDirective],
  templateUrl: './number-field.component.html',
  styleUrl: './number-field.component.scss',
})
export class NumberFieldComponent extends BaseInputComponent {
  /**
   * Whether the field is a decimal (the metadata's `decimal` mark): its value is text in
   * JSON, so the input writes what the person typed into the form as a string, with
   * every digit. A field with no mark is an integer or a float and is written as a number.
   */
  decimal = computed(() => this.fieldMeta().decimal === true);
}
