import { Directive, ElementRef, forwardRef, inject } from '@angular/core';
import { ControlValueAccessor, NG_VALUE_ACCESSOR } from '@angular/forms';

/**
 * The value accessor of a decimal's number input: the form holds the text the person
 * typed, never a JavaScript number.
 *
 * A decimal is text in JSON in both directions (FieldMeta.decimal), because a JavaScript
 * number keeps about fifteen digits and a decimal column holds more. Angular's own
 * accessor for a number input parses what was typed into a number, which loses the digits
 * past those and the zeros after the point before the request is ever built. This one
 * hands the form the input's text as it stands: every digit, and null for an input left
 * empty. The input stays a number input, so the browser still takes digits alone.
 *
 * An accessor a control declares outranks the ones Angular supplies, so the number
 * accessor on the same input is never asked.
 */
@Directive({
  selector: 'input[impulseDecimalInput]',
  host: {
    '(input)': 'typed()',
    '(blur)': 'touched()',
  },
  providers: [{ provide: NG_VALUE_ACCESSOR, useExisting: forwardRef(() => DecimalInputDirective), multi: true }],
})
export class DecimalInputDirective implements ControlValueAccessor {
  private input = inject<ElementRef<HTMLInputElement>>(ElementRef).nativeElement;

  private changed: (value: string | null) => void = () => undefined;
  touched: () => void = () => undefined;

  /** Shows the form's value in the input: the text as served, or nothing. */
  writeValue(value: unknown): void {
    this.input.value = value === null || value === undefined ? '' : String(value);
  }

  registerOnChange(fn: (value: string | null) => void): void {
    this.changed = fn;
  }

  registerOnTouched(fn: () => void): void {
    this.touched = fn;
  }

  setDisabledState(isDisabled: boolean): void {
    this.input.disabled = isDisabled;
  }

  /** Hands the form what the input holds now: its text, or null when it is empty. */
  typed(): void {
    const text = this.input.value;
    this.changed(text === '' ? null : text);
  }
}
