import { Component, computed, linkedSignal } from '@angular/core';
import { ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDatepickerModule } from '@angular/material/datepicker';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { BaseInputComponent } from '../../base-field.directive';

@Component({
  selector: 'impulse-text-field',
  imports: [
    MatFormFieldModule,
    MatInputModule,
    MatDatepickerModule,
    ReactiveFormsModule,
    MatIconModule,
    MatButtonModule,
  ],
  templateUrl: './text-field.component.html',
  styleUrl: './text-field.component.scss',
})
export class TextFieldComponent extends BaseInputComponent {
  /**
   * A field that holds a secret (the metadata's `secret`): what is typed is masked until
   * the person asks to see it.
   */
  readonly secret = computed(() => this.fieldMeta().secret === true);
  /**
   * Whether what is being typed is shown. It starts hidden, and it shows the input's own
   * text and nothing else: the server returns no value for a secret, so there is none to
   * reveal. It is hidden again whenever the form enters or leaves edit, so a box a person
   * showed, then saved or abandoned, is masked the next time it is typed into.
   */
  readonly shown = linkedSignal<'edit' | 'view', boolean>({
    source: this.editMode,
    computation: () => false,
  });

  toggleShown(): void {
    this.shown.update((shown) => !shown);
  }
}
