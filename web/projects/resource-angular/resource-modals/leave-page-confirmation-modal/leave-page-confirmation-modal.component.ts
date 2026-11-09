import { Component } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogActions, MatDialogModule } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';

@Component({
  selector: 'impulse-leave-page-confirmation-modal',
  imports: [MatDialogModule, MatFormFieldModule, MatDialogActions, MatButtonModule],
  templateUrl: './leave-page-confirmation-modal.component.html',
  styleUrls: ['./leave-page-confirmation-modal.component.scss'],
})
export class LeavePageConfirmationModalComponent {}
