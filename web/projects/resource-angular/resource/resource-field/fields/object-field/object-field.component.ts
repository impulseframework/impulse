import { Component, computed } from '@angular/core';
import { ReactiveFormsModule } from '@angular/forms';
import { BaseInputComponent } from '../../base-field.directive';

/**
 * An object field, read-only in every mode: the value pretty-printed as JSON. A derived
 * struct, an imported type, a value with no fixed shape, and an `unknown` that is a
 * string, a number, or an array all print the same way. No mode offers an input; the
 * object editors are later items.
 */
@Component({
  selector: 'impulse-object-field',
  imports: [ReactiveFormsModule],
  templateUrl: './object-field.component.html',
  styleUrl: './object-field.component.scss',
})
export class ObjectFieldComponent extends BaseInputComponent {
  /** The value as indented JSON; empty for null. */
  json = computed(() => {
    const value: unknown = this.form().get(this.fieldConfig().name)?.value;
    if (value === null || value === undefined) {
      return '';
    }
    return JSON.stringify(value, null, 2);
  });
}
