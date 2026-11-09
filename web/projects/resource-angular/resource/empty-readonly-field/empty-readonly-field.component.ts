import { Component, input } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { defaultEmptyFieldValue } from '@impulseframework/resource-angular/types';

@Component({
  selector: 'impulse-empty-readonly-field',
  imports: [MatFormFieldModule, MatButtonModule, MatInputModule],
  templateUrl: './empty-readonly-field.component.html',
  styleUrl: './empty-readonly-field.component.scss',
})
export class EmptyReadonlyFieldComponent {
  label = input.required<string>();

  displayValue = defaultEmptyFieldValue;
}
