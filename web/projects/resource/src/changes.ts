/**
 * One field a change record says was changed, with its value before and after the
 * change as the record keeps them. The server lists only the fields the caller may see
 * on the record.
 */
export interface ChangedField {
  /** The field's JSON name. */
  field: string;
  /** The value before the change, as JSON decodes it; null where there was none. */
  old: unknown;
  /** The value after the change, as JSON decodes it; null where there is none. */
  new: unknown;
  /**
   * The name of the row the old value pointed at, as it was when the change was made;
   * absent where none was kept or the caller may not read it.
   */
  oldName?: string;
  /**
   * The name of the row the new value pointed at, as it was when the change was made;
   * absent where none was kept or the caller may not read it.
   */
  newName?: string;
  /** True for a field that holds a secret: it was changed, and neither value is kept. */
  secret?: boolean;
}

/**
 * One change record of a record's history: when the change was committed, what kind of
 * change it was, who made it, and each field it changed that the caller may see. A record
 * that changed only fields the caller may not see is listed with no field.
 */
export interface ChangeRecord {
  /** When the change was committed, as the server wrote it. */
  at: string;
  /** The record's place among the change records one commit wrote for the record. */
  sequence: number;
  /** What kind of change it was: the record was created, updated or deleted. */
  operation: 'create' | 'update' | 'delete';
  /** Who made the change, as one sentence. */
  source: string;
  /** The changed fields the caller may see; empty where the caller may see none of them. */
  fields: ChangedField[];
}

/** A page of a record's history: its change records, newest first. */
export interface ChangeHistoryPage {
  /** At most one page of change records, newest first. */
  changes: ChangeRecord[];
  /** What to pass back as `after` for the page of older records; absent where there are none. */
  next?: string;
}

/** What a read of a record's history asks for beyond the record. */
export interface ChangeHistoryQuery {
  /** The `next` of the page before: the page of the records older than it. */
  after?: string;
  /** A field's JSON name: only the change records that changed it. */
  field?: string;
}
