import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Component, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialog, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { Router } from '@angular/router';
import { ApiDescriptor, TenantCreationDescriptor } from '@impulseframework/resource';
import { inSentence, PlaceService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import {
  articleFor,
  holdsValue,
  metadataTypeCoercion,
  ResourceLayoutComponent,
  ResourceStore,
} from '@impulseframework/resource-angular/resource';
import {
  API_URL,
  DataType,
  field,
  FieldElement,
  FieldName,
  nameInWords,
  RecordData,
  Resource,
  RESOURCE_META,
} from '@impulseframework/resource-angular/types';
import { firstValueFrom, map } from 'rxjs';

/**
 * Opens the dialog a signed-in person who holds no tenant creates their own through, and
 * answers its reference: it closes with the new tenant's id, or with nothing when
 * dismissed. Refused where the description carries no create-your-tenant route
 * (`tenantCreation`).
 */
export function openCreateTenantDialog(
  dialog: MatDialog,
  descriptor: ApiDescriptor,
): MatDialogRef<CreateTenantDialogComponent, string | undefined> {
  if (!descriptor.tenantCreation) {
    throw new Error(
      "openCreateTenantDialog: the API description carries no create-your-tenant route (tenantCreation): this outlet does not serve it, or the application's tenants are not made by the people who sign up",
    );
  }
  return dialog.open<CreateTenantDialogComponent, ApiDescriptor, string | undefined>(CreateTenantDialogComponent, {
    width: '480px',
    maxWidth: 'calc(100vw - 32px)',
    data: descriptor,
  });
}

/**
 * The dialog a person who signed up and holds no tenant creates their own through. It asks
 * for the fields the generated create-your-tenant route takes (the description's
 * `tenantCreation`), each drawn from the tenant record's metadata as a method's dialog
 * draws a method's fields: the tenant's key is an id the server generates and answers,
 * which nobody types. The route writes the row and seats the person as its administrator,
 * so the dialog reads the person's permissions again, closes with the new tenant's id and
 * opens the tenant's first page. The route is no resource or method of the client, so the
 * dialog posts with the application's HttpClient. A refusal is said in place and the
 * dialog stays open: a name another tenant carries in the form's words, anything else in
 * the server's.
 */
@Component({
  selector: 'impulse-create-tenant-dialog',
  imports: [MatButtonModule, MatDialogModule, ReactiveFormsModule, ResourceLayoutComponent],
  templateUrl: './create-tenant-dialog.component.html',
  styleUrl: './create-tenant-dialog.component.scss',
  providers: [ResourceStore],
})
export class CreateTenantDialogComponent {
  private readonly dialogRef = inject<MatDialogRef<CreateTenantDialogComponent, string | undefined>>(MatDialogRef);
  private readonly http = inject(HttpClient);
  private readonly auth = inject(AuthService);
  private readonly places = inject(PlaceService);
  private readonly router = inject(Router);
  private readonly apiUrl = inject(API_URL);
  private readonly descriptor = inject<ApiDescriptor>(MAT_DIALOG_DATA);
  private readonly creation = this.descriptor.tenantCreation as TenantCreationDescriptor;

  /** The tenant record's generated metadata: how each field the route takes is drawn. */
  readonly meta = inject(RESOURCE_META)(this.creation.resource as Resource);
  /** What the application calls a tenant, inside a sentence. */
  readonly word = inSentence(this.descriptor.domainRoute?.word?.one ?? 'tenant');
  /** The field a tenant is called by: the record's name field, or the first the route takes. */
  readonly nameField: string = this.meta?.nameField
    ? String(this.meta.nameField)
    : (this.creation.fields[0]?.name ?? '');
  /** What the name's input says under it. */
  readonly hint = `What people call the ${this.word}, wherever it is listed. It can be changed later.`;

  /** One input for each field the route takes, in its order: the name as Name, every other by its name in words. */
  readonly elements: FieldElement[] = this.creation.fields.map((taken) =>
    field({
      name: taken.name as FieldName,
      label: taken.name === this.nameField ? 'Name' : nameInWords(taken.name),
      cols: 12,
    }),
  );
  /** Each input's value before anything is typed: an empty list for a field that holds a list, empty text otherwise. */
  readonly pristine: Record<string, DataType> = Object.fromEntries(
    this.creation.fields.map((taken): [string, DataType] => [taken.name, this.holdsList(taken.name) ? [] : '']),
  );
  readonly form = new FormGroup(
    Object.fromEntries(
      this.creation.fields.map((taken): [string, FormControl] => [
        taken.name,
        new FormControl(this.pristine[taken.name], taken.required ? [Validators.required] : []),
      ]),
    ),
  );
  /** The values of the inputs as they stand, handed to each input. */
  readonly values = toSignal(this.form.valueChanges.pipe(map(() => this.form.getRawValue() as RecordData)), {
    initialValue: this.form.getRawValue() as RecordData,
  });

  /** True while the request is open. */
  readonly busy = signal(false);
  /** The refusal of the last request, in a sentence, or empty. */
  readonly refusal = signal('');

  /** Creates the tenant from the inputs, then opens it. */
  async create(): Promise<void> {
    if (this.form.invalid || this.busy()) {
      return;
    }
    this.busy.set(true);
    this.refusal.set('');
    const values = metadataTypeCoercion(this.form.getRawValue() as RecordData, this.meta);
    // An input left empty sends nothing: a field that is not required is then absent.
    const body = Object.fromEntries(Object.entries(values).filter(([, value]) => holdsValue(value)));
    const name = String(values[this.nameField] ?? '');
    try {
      const answer = await firstValueFrom(
        this.http.post<Record<string, unknown>>(`${this.apiUrl}/${this.creation.route}`, body),
      );
      const id = String(answer[this.creation.key] ?? '');
      await firstValueFrom(this.auth.refreshPermissions());
      this.dialogRef.close(id);
      const place = this.places.places().find((candidate) => candidate.id === id) ?? { id, label: name };
      await this.router.navigateByUrl(this.places.landing(place));
    } catch (failure) {
      this.refusal.set(this.refusalOf(failure, name));
    } finally {
      this.busy.set(false);
    }
  }

  /** Closes the dialog with nothing. */
  cancel(): void {
    this.dialogRef.close();
  }

  /** Whether the record's field holds a list, by its metadata. */
  private holdsList(name: string): boolean {
    const displayType = this.meta?.fields.find((candidate) => candidate.fieldName === name)?.displayType ?? '';
    return displayType.endsWith('[]');
  }

  /**
   * The sentence a refused create is said with. A name another tenant carries is the one
   * conflict the form can cause (the unique index on the name answers 409), so it is said
   * in the form's words; anything else is the server's message where it gave one.
   */
  private refusalOf(failure: unknown, name: string): string {
    if (failure instanceof HttpErrorResponse && failure.status === 409) {
      const article = articleFor(this.word) === 'an' ? 'An' : 'A';
      return `${article} ${this.word} named ${name} already exists. Choose another name.`;
    }
    if (failure instanceof HttpErrorResponse) {
      const message: unknown = (failure.error as { message?: unknown } | null)?.message;
      return typeof message === 'string' && message ? message : failure.message;
    }
    return failure instanceof Error ? failure.message : String(failure);
  }
}
