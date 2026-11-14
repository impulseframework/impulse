import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Component, inject, signal } from '@angular/core';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialog, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { Router } from '@angular/router';
import { injectApi } from '@app/api/api';
import { apiDescriptor, CreateTenant, createTenantRoute } from '@app/service/zz_gen_api';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { resourcePage } from '@impulseframework/resource-angular/resource-route-generator';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { tenantsConfig } from '../../../configs/tenants.config';

/** Which of the record's two creation paths the dialog takes. */
export interface NewTenantData {
  /**
   * The person names their own tenant and becomes its administrator: the self-service half
   * of the Tenant record's creation shape, for a session holding no tenant. Otherwise an
   * administrator holding Create on Tenants creates the record, and nobody is seated in it.
   */
  readonly selfService: boolean;
}

/** What the create-your-tenant route answers: the new tenant's key. */
interface Created {
  id: string;
}

/** Opens the dialog; it closes with the new tenant's key, or with nothing when cancelled. */
export function openNewTenantDialog(
  dialog: MatDialog,
  data: NewTenantData,
): MatDialogRef<NewTenantDialogComponent, string | undefined> {
  return dialog.open<NewTenantDialogComponent, NewTenantData, string | undefined>(NewTenantDialogComponent, {
    data,
    width: '480px',
    maxWidth: 'calc(100vw - 32px)',
  });
}

/**
 * The new-tenant dialog creates a Tenant record by either of the two paths its creation
 * shape (`@tenant(creation: both)`) keeps. The home page's card opens it for a person who
 * signed in and holds no tenant: the generated create-your-tenant route (`POST
 * create-tenant`, the `createTenantRoute` constant the generated client exports) writes the
 * row, seats them as its administrator and hands the tenant to the roster, so their next
 * request stands in it, and the dialog opens the dashboard under the new tenant's URL,
 * where the guard on the tenant segment enters it. A person who already holds a tenant is
 * refused by the server, which the dialog reports in place. Tenants › New tenant… opens it
 * for an administrator holding Create on Tenants: the client's create on the record writes
 * the row, the roster serves it at once, and the dialog opens the record's page. The route
 * is no resource or method of the client, so the self-service path posts with the
 * application's HttpClient. Either way the session's tenant list is read again first.
 */
@Component({
  selector: 'app-new-tenant-dialog',
  imports: [MatButtonModule, MatDialogModule, MatFormFieldModule, MatInputModule, ReactiveFormsModule],
  templateUrl: './new-tenant-dialog.component.html',
  styleUrl: './new-tenant-dialog.component.scss',
})
export class NewTenantDialogComponent {
  readonly data = inject<NewTenantData>(MAT_DIALOG_DATA);
  private dialogRef = inject<MatDialogRef<NewTenantDialogComponent, string | undefined>>(MatDialogRef);
  private http = inject(HttpClient);
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
    const body: CreateTenant = this.form.getRawValue();
    try {
      const id = this.data.selfService ? await this.createOwn(body) : await this.createRecord(body);
      await firstValueFrom(this.auth.refreshPermissions());
      this.dialogRef.close(id);
      await this.router.navigate(
        this.data.selfService
          ? ['/', apiDescriptor.domainRoute.segment, id, 'dashboard']
          : ['/', ...resourcePage(tenantsConfig, resourceMeta).route, id],
      );
    } catch (failure) {
      this.error.set(refusal(failure));
    } finally {
      this.busy.set(false);
    }
  }

  /** The self-service path: the generated route seats the person in their new tenant. */
  private async createOwn(body: CreateTenant): Promise<string> {
    const created = await firstValueFrom(this.http.post<Created>(`${environment.apiUrl}/${createTenantRoute}`, body));
    return created.id;
  }

  /** The administrator's path: the client's create on the record, under Create on Tenants. */
  private async createRecord(body: CreateTenant): Promise<string> {
    return (await this.api.tenants.create(body)) ?? body.id;
  }
}

/** The sentence a failed request is reported with: the server's message where it gave one. */
function refusal(failure: unknown): string {
  if (failure instanceof HttpErrorResponse) {
    const message: unknown = failure.error?.message;
    return typeof message === 'string' && message ? message : failure.message;
  }
  return failure instanceof Error ? failure.message : String(failure);
}
