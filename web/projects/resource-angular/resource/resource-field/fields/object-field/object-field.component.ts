import { Component, computed } from '@angular/core';
import { ReactiveFormsModule } from '@angular/forms';
import { placeWords } from '../../../place-words';
import { BaseInputComponent } from '../../base-field.directive';

/**
 * An object field, read-only in every mode. A value that is a place (GeoJSON: a point, a
 * line, an area) is written in the words a person reads a place by, as a list's cell
 * writes it: a point is `34.0522° N, 118.2437° W`, never its JSON. Any other value is
 * pretty-printed as JSON: a derived struct, an imported type, a value with no fixed
 * shape, and an `unknown` that is a string, a number, or an array all print the same way.
 * No mode offers an input; the object editors are later items.
 */
@Component({
  selector: 'impulse-object-field',
  imports: [ReactiveFormsModule],
  templateUrl: './object-field.component.html',
  styleUrl: './object-field.component.scss',
})
export class ObjectFieldComponent extends BaseInputComponent {
  /** The field's value, whatever JSON it is. */
  private value = computed<unknown>(() => {
    return this.form().get(this.fieldConfig().name)?.value;
  });

  /** The value as a place in words; undefined for a value that is not a place. */
  place = computed(() => {
    return placeWords(this.value());
  });

  /** The value as indented JSON; empty for null. */
  json = computed(() => {
    const value = this.value();
    if (value === null || value === undefined) {
      return '';
    }
    return JSON.stringify(value, null, 2);
  });
}
