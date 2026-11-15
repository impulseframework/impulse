import { Component, inject, signal } from '@angular/core';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { Router } from '@angular/router';
import { injectApi } from '@app/api/api';
import { TenantsCreate } from '@app/service/zz_gen_api';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { resourcePage } from '@impulseframework/resource-angular/resource-route-generator';
import { firstValueFrom } from 'rxjs';
import { tenantsConfig } from '../../../configs/tenants.config';

/** Opens the dialog; it closes with the new tenant's key, or with nothing when cancelled. */
export function openNewTenantDialog(dialog: MatDialog): MatDialogRef<NewTenantDialogComponent, string | undefined> {
  return dialog.open<NewTenantDialogComponent, undefined, string | undefined>(NewTenantDialogComponent, {
    width: '480px',
    maxWidth: 'calc(100vw - 32px)',
  });
}

/**
 * The new-tenant dialog, Tenants › New tenant…: an administrator holding Create on Tenants
 * names a tenant, the client's create on the record writes the row, the roster serves it
 * at once, and the dialog opens the record's page. Nobody is seated in it: a person enters
 * once someone gives them a role there. The Tenant record here is created by an
 * administrator only (`@tenant`); a record its own people create
 * (`@tenant(creation: self-service)`) has the dashboard offer the same dialog to a person
 * holding no tenant, as the tenanted skeleton does. The session's tenant list is read
 * again first, so an administrator who reaches every tenant finds it in the switcher.
 */
@Component({
  selector: 'app-new-tenant-dialog',
  imports: [MatButtonModule, MatDialogModule, MatFormFieldModule, MatInputModule, ReactiveFormsModule],
  templateUrl: './new-tenant-dialog.component.html',
  styleUrl: './new-tenant-dialog.component.scss',
})
export class NewTenantDialogComponent {
  private dialogRef = inject<MatDialogRef<NewTenantDialogComponent, string | undefined>>(MatDialogRef);
  private api = injectApi();
  private auth = inject(AuthService);
  private router = inject(Router);

  /** The record's fields: the key, a slug the tenant-scoped URLs carry, and the name. */
  readonly form = new FormGroup({
    id: new FormControl('', {
      nonNullable: true,
      validators: [Validators.required, Validators.pattern(/^[a-z0-9]+(-[a-z0-9]+)*$/)],
    }),
    name: new FormControl('', { nonNullable: true, validators: [Validators.required] }),
  });

  /** True while the request is in flight. */
  readonly busy = signal(false);
  /** The server's refusal, in its words, or empty. */
  readonly error = signal('');

  async create(): Promise<void> {
    if (this.form.invalid || this.busy()) {
      return;
    }
    this.busy.set(true);
    this.error.set('');
    const body: TenantsCreate = this.form.getRawValue();
    try {
      const id = (await this.api.tenants.create(body)) ?? body.id;
      await firstValueFrom(this.auth.refreshPermissions());
      this.dialogRef.close(id);
      await this.router.navigate(['/', ...resourcePage(tenantsConfig, resourceMeta).route, id]);
    } catch (failure) {
      this.error.set(failure instanceof Error ? failure.message : String(failure));
    } finally {
      this.busy.set(false);
    }
  }
}
