import { Component, computed } from '@angular/core';
import { ReactiveFormsModule } from '@angular/forms';
import { MatChipsModule } from '@angular/material/chips';
import { elementTypeOf, formatByDisplayType } from '../../../format-fns';
import { BaseInputComponent } from '../../base-field.directive';

/**
 * An array field, read-only in every mode: the elements as chips, each written the way
 * a grid cell writes a value of the element type (a date as a date, an object as its
 * JSON, a bytes value as its size). No mode offers an input: a typed string is not the
 * array the server accepts, and the array editor is a later item.
 */
@Component({
  selector: 'impulse-array-field',
  imports: [MatChipsModule, ReactiveFormsModule],
  templateUrl: './array-field.component.html',
  styleUrl: './array-field.component.scss',
})
export class ArrayFieldComponent extends BaseInputComponent {
  /** The element type of the field's display type: `number[]` is `number`. */
  elementType = computed(() => {
    return elementTypeOf(this.fieldMeta().displayType);
  });

  /** The elements as text, one chip each; none for a value that is not an array. */
  items = computed<string[]>(() => {
    const value: unknown = this.form().get(this.fieldConfig().name)?.value;
    if (!Array.isArray(value)) {
      return [];
    }
    const elementType = this.elementType();
    return value.map((element) => formatByDisplayType(elementType, element));
  });
}
