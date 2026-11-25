import { Component, computed, inject, OnInit, PendingTasks, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatSelectModule } from '@angular/material/select';
import { ChangedField, ChangeHistoryPage, ChangeHistoryQuery, ChangeRecord } from '@impulseframework/resource';
import { AppGridComponent } from '@impulseframework/resource-angular/grid';
import { ColumnConfig, FieldName, momentFormat, RecordData } from '@impulseframework/resource-angular/types';
import { format, isValid, parseISO } from 'date-fns';

/** A field the pop-up names by the label its page gives it. */
export interface ChangeHistoryField {
  /** The field's JSON name, as a change record names it (`statusId`). */
  name: string;
  /** The field's label on the record's page ("Status"). */
  label: string;
}

/** The record a history pop-up is opened for. */
export interface ChangeHistoryDialogData {
  /** The pop-up's title, the caller's words ("History of Survey the Kessel run"). */
  title: string;
  /**
   * Reads one page of the record's history: the record's handle's `changes` with the
   * record's key, handed what the pop-up asks for (the older page, one field).
   */
  read: (query: ChangeHistoryQuery) => Promise<ChangeHistoryPage>;
  /**
   * The fields the record's page shows, with their labels: the Field column writes a
   * field by its label, and the pop-up offers them to narrow to. A field not listed is
   * written by its JSON name; with none given there is nothing to narrow to.
   */
  fields?: ChangeHistoryField[];
}

/** The columns of a record's history: when, by whom, what kind of change, which field, from what, to what. */
const changeHistoryColumns: ColumnConfig[] = [
  { id: 'at' as FieldName, header: 'When' },
  { id: 'by' as FieldName, header: 'By' },
  { id: 'change' as FieldName, header: 'Change' },
  { id: 'field' as FieldName, header: 'Field' },
  { id: 'from' as FieldName, header: 'From' },
  { id: 'to' as FieldName, header: 'To' },
];

/** What the pop-up says of a record with no change record. */
const noChanges = 'Nothing has been recorded for this record yet.';

/** What the pop-up says where no change record changed the field chosen. */
const noChangesToField = 'No recorded change to this field.';

/** What a secret field's From and To read: it was changed, and neither value is kept. */
const notKept = 'Not kept';

/** What the Change column reads for each kind of change. */
const changeWords: Record<ChangeRecord['operation'], string> = {
  create: 'Created',
  update: 'Changed',
  delete: 'Deleted',
};

/**
 * A value as a person reads it: a text as it is, a number or a boolean as its text,
 * nothing for null, and an object or a list as its JSON on one line.
 */
function written(value: unknown): string {
  if (value === null || value === undefined) {
    return '';
  }
  if (typeof value === 'string') {
    return value;
  }
  if (typeof value === 'number' || typeof value === 'boolean') {
    return String(value);
  }
  return JSON.stringify(value);
}

/** A moment as every list writes one (momentFormat), or as it came where it cannot be read as one. */
function writtenAt(at: string): string {
  const moment = parseISO(at);
  return isValid(moment) ? format(moment, momentFormat(moment, Date.now())) : at;
}

/**
 * The rows of one change record: one for each field it lists, each with when, by whom and
 * what kind of change, so a row reads alone; and one row with only those three for a
 * record that lists no field the caller may see.
 */
function recordRows(record: ChangeRecord, labels: ReadonlyMap<string, string>): RecordData[] {
  const at = writtenAt(record.at);
  const change = changeWords[record.operation];
  if (record.fields.length === 0) {
    return [{ at, by: record.source, change, field: '', from: '', to: '' }];
  }
  return record.fields.map((changed: ChangedField) => ({
    at,
    by: record.source,
    change,
    field: labels.get(changed.field) ?? changed.field,
    from: changed.secret ? notKept : written(changed.old),
    to: changed.secret ? notKept : written(changed.new),
  }));
}

/**
 * One record's history, read only: each change, newest first, with when it was made, by
 * whom, what kind of change it was, and each field it changed from what to what, in the
 * library's table. It reads the
 * record's history through the caller's `read`, the record's own `changes` route, which
 * the server serves to whoever may read the record, each change with only the fields the
 * person may see on it. The table is drawn once the answer is known; a refusal, by the
 * server or by the network, is shown in its place in the server's words. Where older
 * changes are left out of the page, "Show older" reads them and adds their rows; where
 * the caller names the page's fields, a choice of one narrows the table to its changes.
 * Open it with `openChangeHistory`.
 */
@Component({
  selector: 'impulse-change-history-dialog',
  imports: [MatDialogModule, MatButtonModule, MatFormFieldModule, MatSelectModule, AppGridComponent],
  templateUrl: './change-history-dialog.component.html',
  styleUrls: ['./change-history-dialog.component.scss'],
})
export class ChangeHistoryDialogComponent implements OnInit {
  readonly history = inject<ChangeHistoryDialogData>(MAT_DIALOG_DATA);
  /** Each read is registered as pending work, so the application is stable only once it has answered. */
  private pendingTasks = inject(PendingTasks);
  private labels = new Map((this.history.fields ?? []).map((field) => [field.name, field.label]));
  /** The reads made, so an answer to one that a later read replaced is dropped. */
  private reads = 0;

  readonly columns = changeHistoryColumns;
  /** The fields to narrow to; empty where the caller names none, and no choice is drawn. */
  readonly fields = this.history.fields ?? [];
  /** The field chosen, by its JSON name; empty for all fields. */
  readonly field = signal('');
  /** The change records read so far, newest first; undefined until the first read has answered. */
  readonly records = signal<ChangeRecord[] | undefined>(undefined);
  /** The `after` of the page of older records; undefined where there are none. */
  readonly next = signal<string | undefined>(undefined);
  /** Whether a page of older records is on its way. */
  readonly reading = signal(false);
  /** The refusal, in the server's words; undefined while no read has failed. */
  readonly error = signal<string | undefined>(undefined);
  /** The records as the table writes them, a row for each field each record changed. */
  readonly rows = computed(() => (this.records() ?? []).flatMap((record) => recordRows(record, this.labels)));
  /** What the empty table says: of the record, or of the field chosen. */
  readonly empty = computed(() => (this.field() === '' ? noChanges : noChangesToField));

  ngOnInit(): void {
    void this.load({}, false);
  }

  /** Reads the history again from the newest page, narrowed to the field chosen, or to none. */
  choose(field: string): void {
    this.field.set(field);
    void this.load(field === '' ? {} : { field }, false);
  }

  /** Reads the page of older records and adds its rows below the ones shown. */
  older(): void {
    const after = this.next();
    if (after === undefined || this.reading()) {
      return;
    }
    const field = this.field();
    void this.load(field === '' ? { after } : { after, field }, true);
  }

  private async load(query: ChangeHistoryQuery, adding: boolean): Promise<void> {
    const read = ++this.reads;
    const release = this.pendingTasks.add();
    this.reading.set(true);
    try {
      const page = await this.history.read(query);
      if (read !== this.reads) {
        return;
      }
      this.records.set(adding ? [...(this.records() ?? []), ...page.changes] : page.changes);
      this.next.set(page.next || undefined);
      this.error.set(undefined);
    } catch (error) {
      if (read === this.reads) {
        this.error.set(error instanceof Error ? error.message : String(error));
      }
    } finally {
      if (read === this.reads) {
        this.reading.set(false);
      }
      release();
    }
  }
}
