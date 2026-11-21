import { Component, computed, DestroyRef, inject, signal } from '@angular/core';
import { takeUntilDestroyed, toSignal } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { ApiError, MethodHandle, MethodWarning, UploadMethodHandle } from '@impulseframework/resource';
import {
  DataType,
  enumeratedConfig,
  EnumeratedConfigOptions,
  field,
  FieldElement,
  FieldName,
  METHOD_META,
  RecordData,
  RPCRecordData,
} from '@impulseframework/resource-angular/types';
import { debounceTime, map } from 'rxjs';
import { formatByteSize } from '../format-fns';
import { ResourceLayoutComponent } from '../resource-layout/resource-layout.component';
import { ResourceStore } from '../resource-store.service';
import { metadataTypeCoercion } from '../resources-helpers';
import { askedFields, holdsValue, nameInWords, requestBody } from '../row-methods/row-methods';

/** What a method dialog is opened with. */
export interface MethodDialogData {
  /** The generated method the dialog runs. */
  method: string;
  /** The dialog's title and the words on its button. */
  label: string;
  /**
   * What the request takes without asking: the fields that carry the key of the row the
   * method runs against, and what the page filled from a row. Empty for a method that
   * names no row on a page that fills nothing.
   */
  preset: Record<string, unknown>;
  /** The question asked before a method that takes nothing more than the row. */
  confirm?: string;
  /** The words above an input, by the request field's name; a field not named is labeled with its name in words. */
  labels?: Record<string, string>;
  /**
   * What a picker among the inputs lists each option by, by the request field's name:
   * the fields of the listed resource, joined in order. A field not named lists its
   * options by the source's own name.
   */
  shows?: Record<string, string[]>;
  /** The request fields picked as a day that mean through the end of that day: each is sent as the next day's first instant. */
  throughDay?: readonly string[];
  /**
   * How a picker among the inputs narrows its options by what the dialog holds, by the
   * request field's name: the filter its list is asked with, from the values of the
   * inputs as they stand over the ones the dialog was opened with (`preset`), so a value
   * the page filled narrows as one a person picked would. The picker asks again as the
   * inputs change, and a choice made before the filter changed is cleared, since it was
   * made among other options.
   */
  narrows?: Record<string, (values: RecordData) => string>;
}

/** What a method dialog closes with once the method has run; a dialog dismissed closes with nothing. */
export interface MethodDialogResult {
  /** The method's answer: its result, or `{ status, result }` where it declares its statuses. */
  answer: unknown;
}

/**
 * The dialog a page runs a generated method through, drawn from the method's metadata
 * alone. The request fields the row does not carry are inputs, each by its display type
 * (a picker where the field declares one), required where the method requires it; a
 * method that takes nothing more is a question with a button. A method that takes files
 * (@upload) has a file input beside its fields, waits for a file, and sends the request
 * and the files together; the most the files may weigh is the method's declared maximum,
 * said under the input, and how many files the method takes is the method's to refuse.
 * The dialog calls the method itself, through the method's handle, so a tenant's method
 * runs in the selected tenant: while the call is open the button waits, a refusal is
 * shown in the dialog in the server's words and the dialog stays open, and once the
 * method has run the dialog closes with its answer.
 *
 * Where the caller's grant on the method carries a condition that may read the request,
 * the dialog rehearses the call as the person fills it. The digest says the grant is
 * conditional and cannot say for which values; the metadata names the request fields a
 * condition may read (`attribute`). Once those inputs and the required ones are filled,
 * and again after each change, the dialog sends a dry run, which the server runs whole
 * and rolls back. A dry run the server refuses as not permitted (403) turns the inputs a
 * condition may read red (the one changed last, where it is one of several), says so
 * under the form, and holds the button until a marked input is changed; any other
 * answer of a dry run leaves the form as it is, for the call itself to say. The values a
 * picker offers are not each tried: the picked one is. A method no condition can judge
 * by its request, one that runs outside a transaction (it refuses a dry run) and one
 * that takes files (they travel with the call alone) are not rehearsed.
 *
 * A method that warns before it runs (`warning` in its metadata, the method's @warning)
 * is drawn as a warning. The dialog asks the server what the call will do, with what the
 * row carries and what the person has filled, as it opens and again after each change,
 * and nothing is saved by asking. The server's words are shown in the warning style
 * under a warning icon, the confirm button carries the words the server gave it, which
 * say what it does, and the button is held until the server has answered and the person
 * has typed the word the method declares. While an input the method requires is empty
 * the server is not asked, since it would refuse the request for it. A refusal of the
 * question is shown as a refusal of the call is, and holds the button too: nobody
 * confirms a warning they were not shown.
 */
@Component({
  selector: 'impulse-method-dialog',
  imports: [
    MatDialogModule,
    MatButtonModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    ReactiveFormsModule,
    ResourceLayoutComponent,
  ],
  templateUrl: './method-dialog.component.html',
  styleUrls: ['./method-dialog.component.scss'],
  providers: [ResourceStore],
})
export class MethodDialogComponent {
  private readonly dialogRef = inject<MatDialogRef<MethodDialogComponent, MethodDialogResult>>(MatDialogRef);
  private readonly store = inject(ResourceStore);
  private readonly destroyRef = inject(DestroyRef);
  readonly data = inject<MethodDialogData>(MAT_DIALOG_DATA);

  /** The method's generated metadata: its request fields and the row it runs against. */
  readonly meta = inject(METHOD_META)(this.data.method);
  /** The request fields a person fills: every field the row does not carry and the page has not filled. */
  readonly asked = askedFields(this.meta, Object.keys(this.data.preset));
  /** One input for each asked field, under the page's words for it or the field's name in words. */
  readonly elements: FieldElement[] = this.asked.map((asked) =>
    field({
      name: asked.fieldName as FieldName,
      label: this.data.labels?.[asked.fieldName] ?? nameInWords(asked.fieldName),
      cols: 12,
      ...this.picker(asked.fieldName),
    }),
  );
  /**
   * What a narrowing is handed: the values the dialog was opened with, under its inputs
   * as they stand.
   */
  private narrowedBy(values: RecordData | undefined): RecordData {
    return { ...(this.data.preset as RecordData), ...(values ?? {}) };
  }

  /**
   * How the picker of an input lists its options, where the page said: the fields it
   * named, joined in order, in the list and once one is chosen, and the filter its list
   * is asked with, from the values of the other inputs (`narrows`), which the dialog
   * hands each input as they stand. Nothing for an input the page said nothing of, whose
   * picker lists every option by the source's own name.
   */
  private picker(fieldName: string): Pick<FieldElement, 'enumeratedConfig'> | Record<string, never> {
    const shown = (this.data.shows?.[fieldName] ?? []) as FieldName[];
    const narrow = this.data.narrows?.[fieldName];
    if (shown.length === 0 && !narrow) {
      return {};
    }
    const options: EnumeratedConfigOptions = { listDisplay: shown, viewDisplay: shown };
    if (narrow) {
      options.filter = (values: RecordData | undefined): string => narrow(this.narrowedBy(values));
      options.filterType = 'parentResource';
    }
    return { enumeratedConfig: enumeratedConfig(options) };
  }

  /** Each input's value before anything is typed: an empty list for a field that holds a list, empty text otherwise. */
  readonly pristine: Record<string, DataType> = Object.fromEntries(
    this.asked.map((asked): [string, DataType] => [asked.fieldName, asked.displayType.endsWith('[]') ? [] : '']),
  );
  readonly form = new FormGroup(
    Object.fromEntries(
      this.asked.map((asked): [string, FormControl] => [
        asked.fieldName,
        new FormControl(this.pristine[asked.fieldName], asked.required ? [Validators.required] : []),
      ]),
    ),
  );
  /**
   * The values of the inputs as they stand, handed to each input: a picker the page
   * narrows (`narrows`) reads its filter from them.
   */
  readonly values = toSignal(this.form.valueChanges.pipe(map(() => this.form.getRawValue() as RecordData)), {
    initialValue: this.form.getRawValue() as RecordData,
  });
  /** The most a method's files may weigh together, in words; empty for a method that takes no files. */
  readonly uploadLimit = this.meta.upload ? formatByteSize(this.meta.upload.maxBytes) : '';
  /** The files chosen for a method that takes files. */
  readonly files = signal<File[]>([]);
  /**
   * The question of a method that takes nothing more than the row; empty where the dialog
   * draws inputs. A method that takes files asks one only where the page gives it: its
   * file input is what the person fills.
   */
  readonly question =
    this.asked.length === 0 && (!this.meta.upload || this.data.confirm) && (!this.meta.warning || this.data.confirm)
      ? (this.data.confirm ?? `${this.data.label}?`)
      : '';

  /** The word a person types before a method that warns is confirmed; empty for a method that does not warn. */
  readonly typedWord = this.meta.warning?.typed ?? '';
  /** What the server says the call will do, for the request as filled; undefined until it has answered. */
  readonly warned = signal<MethodWarning | undefined>(undefined);
  /** The input the word is typed into. It is no part of the request. */
  readonly typed = new FormControl('', { nonNullable: true });
  private readonly typedValue = toSignal(this.typed.valueChanges, { initialValue: '' });
  /**
   * Whether the call may be confirmed: always for a method that does not warn; for one
   * that does, once the server's warning is on the screen and the word is typed, in any
   * case and with spaces around it or not.
   */
  readonly confirmed = computed(
    () => !this.typedWord || (this.warned() !== undefined && this.typedValue().trim().toLowerCase() === this.typedWord),
  );
  /** The words on the button that runs the method: the server's for a method that warns, the page's label otherwise. */
  readonly runLabel = computed(() => this.warned()?.confirm ?? this.data.label);
  /** Counts the warnings asked for, so an answer that a later change overtook is dropped. */
  private warningTurn = 0;

  /** Whether the call is open: the button waits and a second click does nothing. */
  readonly busy = signal(false);
  /** The server's refusal of the last call, in its words; empty while there is none. */
  readonly refusal = signal('');

  /**
   * The inputs a grant's condition may read: the request fields that declare a name for
   * conditions (`attribute`). A role file may hold the method to their values, and no
   * value never passes such a condition.
   */
  readonly judged = this.asked.filter((asked) => asked.attribute !== undefined).map((asked) => asked.fieldName);
  /**
   * Whether the dialog rehearses the call with a dry run: the caller's grant on the
   * method is conditional by the digest, a condition may read one of the inputs, the
   * method runs in a transaction (so a dry run previews it), and it takes no files.
   */
  readonly rehearses =
    this.meta.dryRun === true &&
    !this.meta.upload &&
    this.judged.length > 0 &&
    this.store.methodHandle(this.data.method).state() === 'conditional';
  /**
   * What the dialog says when a rehearsal is refused as not permitted. The server's own
   * words name the method and the row and, by design, not the condition; the person
   * needs to know which input to change, and the red input shows it.
   */
  static readonly notPermitted = 'You are not permitted to do this with the value marked in red. Change it to go on.';
  /** How long the inputs rest before a change is rehearsed, in milliseconds. */
  static readonly rehearsalDelay = 300;
  /** Counts the rehearsals asked for, so an answer that a later change overtook is dropped. */
  private rehearsal = 0;

  constructor() {
    this.clearWhenNarrowed();
    if (this.meta.warning) {
      void this.askWarning();
      this.form.valueChanges
        .pipe(debounceTime(MethodDialogComponent.rehearsalDelay), takeUntilDestroyed(this.destroyRef))
        .subscribe(() => void this.askWarning());
    }
    if (!this.rehearses) {
      return;
    }
    for (const asked of this.asked) {
      this.form.controls[asked.fieldName].valueChanges
        .pipe(debounceTime(MethodDialogComponent.rehearsalDelay), takeUntilDestroyed(this.destroyRef))
        .subscribe(() => void this.rehearse(asked.fieldName));
    }
  }

  /**
   * Clears a picker's choice when the filter the page narrows it by changes: the choice
   * was made among options the picker no longer lists (a role of another tenant), and a
   * request that sent it would be refused for it.
   */
  private clearWhenNarrowed(): void {
    for (const [name, narrow] of Object.entries(this.data.narrows ?? {})) {
      const control = this.form.controls[name];
      if (!control) {
        continue;
      }
      const held = (): string => narrow(this.narrowedBy(this.form.getRawValue() as RecordData));
      let narrowed = held();
      this.form.valueChanges.pipe(takeUntilDestroyed(this.destroyRef)).subscribe(() => {
        const now = held();
        if (now !== narrowed) {
          narrowed = now;
          control.setValue(this.pristine[name]);
        }
      });
    }
  }

  /**
   * Rehearses the call after an input changed: a dry run with what the row carries and
   * what the person filled, once every input the method requires and every input a
   * condition may read is filled, and no input stands refused. A refusal as not
   * permitted is the judged inputs': the changed one where it is judged, every judged
   * one otherwise, since the server does not say which. A marked input is red (its error
   * holds the server's words), the dialog says so, and the form is invalid until a
   * marked input changes, which clears its mark. A dry run that passes clears what a
   * refused one left.
   */
  async rehearse(changed: string): Promise<void> {
    if (this.busy()) {
      return;
    }
    const raw = this.form.getRawValue() as Record<string, unknown>;
    if (this.form.invalid || this.judged.some((name) => !holdsValue(raw[name]))) {
      // An input is missing or stands refused. A refusal no input carries any more is
      // of values that are gone: its words go with them.
      if (!Object.values(this.form.controls).some((control) => control.hasError('refused'))) {
        this.refusal.set('');
      }
      return;
    }
    const turn = ++this.rehearsal;
    const values = metadataTypeCoercion(raw as RPCRecordData, this.meta);
    try {
      await this.store
        .methodHandle(this.data.method)
        .dryRun(requestBody(this.data.preset, this.asked, values, this.data.throughDay));
      if (turn === this.rehearsal && !this.busy()) {
        this.refusal.set('');
      }
    } catch (error) {
      if (!(error instanceof ApiError)) {
        throw error;
      }
      if (turn !== this.rehearsal || this.busy() || error.status !== 403) {
        return;
      }
      for (const name of this.judged.includes(changed) ? [changed] : this.judged) {
        const control = this.form.controls[name];
        control.setErrors({ refused: error.message });
        control.markAsTouched();
      }
      this.refusal.set(MethodDialogComponent.notPermitted);
    }
  }

  /**
   * Asks the server what the call will do, for what the row carries and what the person
   * has filled, and shows its answer: nothing is saved by asking. It waits while an
   * input the method requires is empty, since the server refuses such a request, and
   * leaves the warning shown before as it is. A refusal is shown in the server's words
   * and takes the warning off the screen, which holds the button. An answer a later
   * change overtook is dropped.
   */
  async askWarning(): Promise<void> {
    if (!this.meta.warning || this.busy() || this.form.invalid) {
      return;
    }
    const turn = ++this.warningTurn;
    const values = metadataTypeCoercion(this.form.getRawValue() as RPCRecordData, this.meta);
    try {
      const said = await this.store
        .methodHandle(this.data.method)
        .warning(requestBody(this.data.preset, this.asked, values, this.data.throughDay));
      if (turn === this.warningTurn) {
        this.warned.set(said);
        this.refusal.set('');
      }
    } catch (error) {
      if (!(error instanceof ApiError)) {
        throw error;
      }
      if (turn === this.warningTurn) {
        this.warned.set(undefined);
        this.refusal.set(error.message);
      }
    }
  }

  /** Whether a method that takes files has one to send; a method that takes none always has what it needs. */
  readonly hasFiles = computed(() => !this.meta.upload || this.files().length > 0);

  /** Takes the files chosen in the file input, in place of any chosen before. */
  pick(event: Event): void {
    this.files.set(Array.from((event.target as HTMLInputElement).files ?? []));
    this.refusal.set('');
  }

  /** Runs the method with what the row carries and what the person filled, and its files where it takes them. */
  async run(): Promise<void> {
    if (this.busy() || this.form.invalid || !this.hasFiles() || !this.confirmed()) {
      return;
    }
    this.busy.set(true);
    this.refusal.set('');
    try {
      const values = metadataTypeCoercion(this.form.getRawValue() as RPCRecordData, this.meta);
      const body = requestBody(this.data.preset, this.asked, values, this.data.throughDay);
      const handle = this.store.methodHandle(this.data.method);
      const answer = this.meta.upload
        ? await uploadHandle(handle, this.data.method).upload(body, this.files())
        : await handle.execute(body);
      const refused = declaredRefusal(answer);
      if (refused !== undefined) {
        this.refusal.set(refused);
        return;
      }
      this.dialogRef.close({ answer });
    } catch (error) {
      if (!(error instanceof ApiError)) {
        throw error;
      }
      this.refusal.set(error.message);
    } finally {
      this.busy.set(false);
    }
  }
}

/**
 * The handle of a method whose metadata declares an upload, with its `upload` call. The
 * API descriptor declares the upload too, and the handle is made from the descriptor, so
 * a handle with no such call is metadata and a descriptor generated apart: refused,
 * naming the method and the way out.
 */
function uploadHandle<Body, Result>(
  handle: MethodHandle<Body, Result>,
  method: string,
): UploadMethodHandle<Body, Result> {
  if (!('upload' in handle)) {
    throw new Error(
      `${method} takes files by its metadata and not by the API descriptor: the two were generated apart; regenerate both`,
    );
  }
  return handle as UploadMethodHandle<Body, Result>;
}

/**
 * The refusal a method answered with one of its own declared statuses: a 4xx the method
 * declares resolves with `{ status, result }`, so the dialog reads it here. Its words
 * are the result's `message` where it carries one, and the status otherwise. Undefined
 * for an answer that is no refusal.
 */
export function declaredRefusal(answer: unknown): string | undefined {
  if (!answer || typeof answer !== 'object' || !('status' in answer)) {
    return undefined;
  }
  const status = (answer as { status: unknown }).status;
  if (typeof status !== 'number' || status < 400) {
    return undefined;
  }
  const result = (answer as { result?: unknown }).result;
  if (result && typeof result === 'object' && 'message' in result && typeof result.message === 'string') {
    return result.message;
  }
  return `The request was refused (${status}).`;
}
