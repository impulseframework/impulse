import { Component } from '@angular/core';
import { ReactiveFormsModule } from '@angular/forms';
import { MatDatepickerModule } from '@angular/material/datepicker';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { BaseInputComponent } from '../../base-field.directive';

@Component({
  selector: 'impulse-number-field',
  imports: [MatFormFieldModule, MatInputModule, MatDatepickerModule, ReactiveFormsModule],
  templateUrl: './number-field.component.html',
  styleUrl: './number-field.component.scss',
})
export class NumberFieldComponent extends BaseInputComponent {}
