import { Component, computed, inject, OnInit, PendingTasks, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatSelectModule } from '@angular/material/select';
import {
  ChangedField,
  ChangeHistoryPage,
  ChangeHistoryQuery,
  ChangeRecord,
  FieldMeta,
  Resource,
} from '@impulseframework/resource';
import { AppGridComponent } from '@impulseframework/resource-angular/grid';
import { columnHeading, drawnAsMark, formatColumnValue } from '@impulseframework/resource-angular/resource';
import {
  ColumnConfig,
  DataType,
  FieldName,
  momentFormat,
  RecordData,
  RESOURCE_META,
} from '@impulseframework/resource-angular/types';
import { format, isValid, parseISO } from 'date-fns';

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
   * The resource the record is a row of. Its generated metadata says how each field's
   * value is written (a moment, a day, a yes or no, one of a fixed set of values, a role)
   * and what a field no column is on is called.
   */
  resource: Resource;
  /**
   * The page's columns, as its list has them (`listColumns`), and a column for any other
   * field the page gives a heading of its own. A changed field one of them is on reads
   * under that column's heading and is written by its `formatType` where it sets one; the
   * first column on a field that is drawn (not `hidden`, no button column) is the one
   * taken. The fields to narrow to are the ones the columns are on. Nothing else of a
   * column is read: its `emptyDataValue`, `valueGetter`, `iconConfig`, `said`, `fallback`,
   * `tenantKey` and `lines` shape a row of the list, and a change record holds one value
   * and not the row as it was.
   */
  columns?: readonly ColumnConfig[];
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

/** A field the Field choice offers: its JSON name, which a choice sends, and its heading. */
interface FieldOption {
  name: string;
  label: string;
}

/**
 * How the pop-up names and writes one changed field: the heading the Field column writes,
 * the page's column that speaks for the field, where one does, and the field's metadata,
 * where the resource's metadata lists it.
 */
interface FieldWriting {
  heading: string;
  column: ColumnConfig | undefined;
  meta: FieldMeta | undefined;
}

/**
 * Whether a column may speak for its field: it is drawn and is no button column. A button
 * column's id is no field, and a hidden column is not one the page lists.
 */
function speaks(column: ColumnConfig): boolean {
  return column.hidden !== true && column.buttonConfig === undefined;
}

/** The column that speaks for a field: the first handed on it that may (speaks). */
function speakingColumn(columns: readonly ColumnConfig[], field: string): ColumnConfig | undefined {
  return columns.find((column) => column.id === field && speaks(column));
}

/**
 * One side of a changed field as From or To writes it: "Not kept" for a secret, the name
 * the record kept of the row the value pointed at where it kept one, a yes or no as the
 * value itself, which the grid draws as the list's check or dash, and every other value
 * as a list's cell on the field writes it (formatColumnValue), nothing for no value.
 */
function sideOf(changed: ChangedField, value: unknown, name: string | undefined, writing: FieldWriting): DataType {
  if (changed.secret) {
    return notKept;
  }
  if (name !== undefined) {
    return name;
  }
  if (drawnAsMark(writing.column, value)) {
    return value;
  }
  return formatColumnValue(writing.column, writing.meta, value);
}

/** A moment as every list writes one (momentFormat), or as it came where it cannot be read as one. */
function writtenAt(at: string): string {
  const moment = parseISO(at);
  return isValid(moment) ? format(moment, momentFormat(moment, Date.now())) : at;
}

/**
 * The rows of one change record: one for each field it lists, each with when, by whom and
 * what kind of change, so a row reads alone; and one row with only those three for a
 * record that lists no field the caller may see. Each field is named and each value
 * written as `writingOf` says for the field (sideOf).
 */
function recordRows(record: ChangeRecord, writingOf: (field: string) => FieldWriting): RecordData[] {
  const at = writtenAt(record.at);
  const change = changeWords[record.operation];
  if (record.fields.length === 0) {
    return [{ at, by: record.source, change, field: '', from: '', to: '' }];
  }
  return record.fields.map((changed: ChangedField) => {
    const writing = writingOf(changed.field);
    return {
      at,
      by: record.source,
      change,
      field: writing.heading,
      from: sideOf(changed, changed.old, changed.oldName, writing),
      to: sideOf(changed, changed.new, changed.newName, writing),
    };
  });
}

/**
 * One record's history, read only: each change, newest first, with when it was made, by
 * whom, what kind of change it was, and each field it changed from what to what, in the
 * library's table. It reads the record's history through the caller's `read`, the
 * record's own `changes` route, which the server serves to whoever may read the record,
 * each change with only the fields the person may see on it. The caller hands the
 * resource and the page's columns, by which each field is named and each value written as
 * the page's list does. The table is drawn once the answer is known; a refusal, by the
 * server or by the network, is shown in its place in the server's words. Where older
 * changes are left out of the page, "Show older" reads them and adds their rows; where
 * the page's columns are on the resource's fields, a choice of one narrows the table to
 * its changes. Open it with `openChangeHistory`.
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
  /** The resource's generated metadata: each field's, by JSON name, and the fields a name stands beside. */
  private readonly meta = inject(RESOURCE_META)(this.history.resource);
  private readonly fieldMetas = new Map((this.meta?.fields ?? []).map((field) => [field.fieldName, field]));
  private readonly named = new Set((this.meta?.nameFields ?? []).map((name) => name.of));
  private readonly handed = this.history.columns ?? [];
  /** The reads made, so an answer to one that a later read replaced is dropped. */
  private reads = 0;

  readonly columns = changeHistoryColumns;
  /**
   * The fields to narrow to: each a handed column speaks for and the metadata lists, by
   * its heading, in the order handed; empty where there is none, and no choice is drawn.
   */
  readonly fields: FieldOption[] = this.optionsOf();
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
  readonly rows = computed(() =>
    (this.records() ?? []).flatMap((record) => recordRows(record, (field) => this.writingOf(field))),
  );
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

  /**
   * How a changed field is named and written: by the column that speaks for it and its
   * metadata. With no column's heading it is named as a list heads a column that sets
   * none, without its trailing `Id` where the resource carries a name beside it and the
   * field takes no fixed set of values.
   */
  private writingOf(field: string): FieldWriting {
    const column = speakingColumn(this.handed, field);
    const meta = this.fieldMetas.get(field);
    const namedWithoutId = this.named.has(field) && meta?.enumeration === undefined;
    return { heading: columnHeading(field, column?.header, namedWithoutId), column, meta };
  }

  /**
   * The Field choice's options: each field a handed column speaks for and the metadata
   * lists, once, where the column that speaks for it stands, by its heading.
   */
  private optionsOf(): FieldOption[] {
    const options: FieldOption[] = [];
    for (const column of this.handed) {
      const name: string = column.id;
      if (!speaks(column) || !this.fieldMetas.has(name)) {
        continue;
      }
      if (!options.some((option) => option.name === name)) {
        options.push({ name, label: this.writingOf(name).heading });
      }
    }
    return options;
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
