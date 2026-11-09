import { Component, computed, effect, HostBinding, input } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { ComputedDisplayFieldElement, RecordData } from '@impulseframework/resource-angular/types';

@Component({
  selector: 'impulse-computed-field',
  imports: [MatFormFieldModule, MatButtonModule, MatInputModule],
  templateUrl: './computed-field.component.html',
  styleUrl: './computed-field.component.scss',
})
export class ComputedFieldComponent {
  fieldConfig = input.required<ComputedDisplayFieldElement>();
  fieldClass = input<string>();
  formDataState = input<RecordData>();

  computedValue = computed(() => {
    try {
      return this.fieldConfig().calculatedValue(this.formDataState());
    } catch (e) {
      console.error('Failed to calculate value for computed field: ', this.fieldConfig().label);
      console.error(e);
      return '';
    }
  });

  showField = computed(() => {
    const shouldRender = this.fieldConfig().shouldRender;
    if (typeof shouldRender === 'boolean') {
      return shouldRender;
    }

    try {
      return shouldRender(this.formDataState());
    } catch (e) {
      console.error('Failed to calculate value for should Render function for field: ', this.fieldConfig().label);
      console.error(e);
      return true;
    }
  });

  @HostBinding('class') class = '';

  constructor() {
    effect(() => {
      this.class = this.showField() ? 'col-' + this.fieldConfig()?.cols : 'hidden-field';
    });
  }
}
