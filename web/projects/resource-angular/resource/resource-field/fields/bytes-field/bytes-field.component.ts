import { Component, computed, effect, signal } from '@angular/core';
import { ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { base64ByteLength, formatByteSize } from '../../../format-fns';
import { BaseInputComponent } from '../../base-field.directive';

/**
 * A bytes field, read-only in every mode: the value's size in a human unit and a
 * download of the decoded bytes, named after the field. The value is the base64 the
 * server carries a byte slice as; nothing here shows it as text, and no mode offers an
 * input, since a typed word would fail the server's base64 decode. Binary content is
 * written through `@upload` and `@file`, never through a form.
 */
@Component({
  selector: 'impulse-bytes-field',
  imports: [MatFormFieldModule, MatInputModule, MatIconModule, MatButtonModule, ReactiveFormsModule],
  templateUrl: './bytes-field.component.html',
  styleUrl: './bytes-field.component.scss',
})
export class BytesFieldComponent extends BaseInputComponent {
  /** The base64 the control holds; undefined for a null or empty value. */
  value = computed<string | undefined>(() => {
    const value: unknown = this.form().get(this.fieldConfig().name)?.value;
    return typeof value === 'string' && value !== '' ? value : undefined;
  });

  /** How many bytes the value decodes to. */
  byteCount = computed(() => {
    const value = this.value();
    return value === undefined ? 0 : base64ByteLength(value);
  });

  /** The size as the field shows it: `32 B`, `12.4 KB`. */
  sizeLabel = computed(() => {
    return this.value() === undefined ? '' : formatByteSize(this.byteCount());
  });

  /**
   * A Blob URL of the decoded bytes for the download anchor, revoked when the value
   * changes and when the field is destroyed. Undefined for no value, and where the
   * environment has no object URLs (a spec under jsdom), which draws no anchor.
   */
  readonly downloadUrl = signal<string | undefined>(undefined);

  constructor() {
    super();
    effect((onCleanup) => {
      const value = this.value();
      if (value === undefined || typeof URL.createObjectURL !== 'function') {
        this.downloadUrl.set(undefined);
        return;
      }
      const url = URL.createObjectURL(new Blob([decodeBase64(value)]));
      this.downloadUrl.set(url);
      onCleanup(() => {
        URL.revokeObjectURL(url);
      });
    });
  }
}

/** The bytes a base64 value encodes. */
function decodeBase64(value: string): Uint8Array<ArrayBuffer> {
  const binary = atob(value);
  const bytes = new Uint8Array(new ArrayBuffer(binary.length));
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}
