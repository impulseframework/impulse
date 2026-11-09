import { Component } from '@angular/core';
import { ReactiveFormsModule } from '@angular/forms';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MatDatepickerModule } from '@angular/material/datepicker';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { BaseInputComponent } from '../../base-field.directive';

@Component({
  selector: 'impulse-boolean-field',
  imports: [MatFormFieldModule, MatInputModule, MatDatepickerModule, ReactiveFormsModule, MatCheckboxModule],
  templateUrl: './boolean-field.component.html',
  styleUrl: './boolean-field.component.scss',
})
export class BooleanFieldComponent extends BaseInputComponent {}
