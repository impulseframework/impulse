import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Component, inject, signal } from '@angular/core';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { Router } from '@angular/router';
import { apiDescriptor, CreateTenant, CreateTenantAnswer, createTenantRoute } from '@app/service/zz_gen_api';
import { inSentence } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';

/** Opens the dialog; it closes with the new tenant's id, or with nothing when cancelled. */
export function openNewTenantDialog(dialog: MatDialog): MatDialogRef<NewTenantDialogComponent, string | undefined> {
  return dialog.open<NewTenantDialogComponent, undefined, string | undefined>(NewTenantDialogComponent, {
    width: '480px',
    maxWidth: 'calc(100vw - 32px)',
  });
}

/**
 * The new-tenant dialog is how a person who signed up creates a tenant of their own. It
 * asks for the tenant's name alone: the tenant's key is an id the server generates and
 * answers, which nobody types, and the name is a field its administrators change later.
 * The home page's card opens it for a person who signed in and holds no tenant: the
 * generated create-your-tenant route (`POST create-tenant`, the `createTenantRoute`
 * constant the generated client exports) writes the row, seats them as its administrator
 * and hands the tenant to the roster, so their next request stands in it, and the dialog
 * opens the dashboard under the new tenant's URL, where the guard on the tenant segment
 * enters it. The route serves while an administrator has a sign-in method open to sign-up
 * (the Sign-in page); while every method is invite only, and for a person who already
 * holds a tenant, the server refuses, which the dialog reports in place. The route is no
 * resource or method of the client, so the dialog posts with the application's
 * HttpClient, and the session's tenant list is read again before the dashboard opens.
 *
 * Nobody else creates a tenant here. An administrator brings an organization in by
 * inviting one person, on the Invited tenants page the library builds among the staff's
 * pages (System), and that person's first sign-in makes the tenant.
 */
@Component({
  selector: 'app-new-tenant-dialog',
  imports: [MatButtonModule, MatDialogModule, MatFormFieldModule, MatInputModule, ReactiveFormsModule],
  templateUrl: './new-tenant-dialog.component.html',
  styleUrl: './new-tenant-dialog.component.scss',
})
export class NewTenantDialogComponent {
  private dialogRef = inject<MatDialogRef<NewTenantDialogComponent, string | undefined>>(MatDialogRef);
  private http = inject(HttpClient);
  private auth = inject(AuthService);
  private router = inject(Router);

  /** What a person gives a new tenant: its name. */
  readonly form = new FormGroup({
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
      const id = await this.createOwn(body);
      await firstValueFrom(this.auth.refreshPermissions());
      this.dialogRef.close(id);
      await this.router.navigate(['/', apiDescriptor.domainRoute.segment, id, 'dashboard']);
    } catch (failure) {
      this.error.set(refusal(failure, body.name));
    } finally {
      this.busy.set(false);
    }
  }

  /** The generated route writes the tenant and seats the person in it as its administrator. */
  private async createOwn(body: CreateTenant): Promise<string> {
    const created = await firstValueFrom(
      this.http.post<CreateTenantAnswer>(`${environment.apiUrl}/${createTenantRoute}`, body),
    );
    return created.id;
  }
}

/** The word with its article, as a sentence starts with it: "A tenant", "An outlet". */
function withArticle(word: string): string {
  return `${/^[aeiou]/i.test(word) ? 'An' : 'A'} ${word}`;
}

/**
 * The sentence a failed create is reported with. A name another tenant carries is the one
 * conflict the form can cause (the unique index on the name answers 409), so it is said
 * in the form's words; anything else is the server's message where it gave one.
 */
function refusal(failure: unknown, name: string): string {
  if (failure instanceof HttpErrorResponse && failure.status === 409) {
    return `${withArticle(inSentence(apiDescriptor.domainRoute?.word?.one ?? 'Tenant'))} named ${name} already exists. Choose another name.`;
  }
  if (failure instanceof HttpErrorResponse) {
    const message: unknown = failure.error?.message;
    return typeof message === 'string' && message ? message : failure.message;
  }
  return failure instanceof Error ? failure.message : String(failure);
}
