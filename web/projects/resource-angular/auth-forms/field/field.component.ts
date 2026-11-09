import { Component, computed, inject, input } from '@angular/core';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { Domain, ReadPermission, Resource, UpdatePermission } from '@impulseframework/resource-angular/types';

export enum InputMode {
  Read = 'read',
  Edit = 'edit',
}

@Component({
  selector: 'impulse-input-field',
  imports: [MatFormFieldModule, MatInputModule],
  template: `
    <mat-form-field [class]="className()">
      <mat-label>{{ name() }}</mat-label>
      <input matInput [disabled]="mode() === inputMode.Edit && !canEdit()" [value]="value()" />
    </mat-form-field>
  `,
  styleUrl: './field.component.scss',
})
export class ImpulseInputFieldComponent {
  auth = inject(AuthService);
  className = input();
  mode = input.required<InputMode>();
  resource = input.required<Resource>();
  domain = input<Domain>();
  value = input.required();
  name = input.required<string>();

  inputMode = InputMode;

  // canRead and canEdit answer from the permission digest — fail closed, and
  // re-evaluated when the digest loads.
  canRead = computed(() => this.allowed(ReadPermission));
  canEdit = computed(() => this.allowed(UpdatePermission));

  private allowed(permission: typeof ReadPermission): boolean {
    const resource = this.resource();
    if (!resource) {
      return false;
    }
    return this.auth.hasPermission({ resource, permission, domain: this.domain() });
  }
}
