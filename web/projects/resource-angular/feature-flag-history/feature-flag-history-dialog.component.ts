import { Component, computed, inject, OnInit, PendingTasks, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule } from '@angular/material/dialog';
import { FeatureFlagChange, FeatureFlagHistory } from '@impulseframework/resource';
import { AppGridComponent } from '@impulseframework/resource-angular/grid';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { ColumnConfig, dayAndMinuteFormat, FieldName, RecordData } from '@impulseframework/resource-angular/types';
import { format, isValid, parseISO } from 'date-fns';

/** The flag a history pop-up is opened for. */
export interface FeatureFlagHistoryData {
  /** The flag's name as the server holds it (`cargo_manifest`). */
  name: string;
  /** The flag's name in words, as the page lists it ("Cargo manifest"); the pop-up's title. */
  words: string;
}

/** What the pop-up says of a flag nobody has flipped. */
export const noFeatureFlagChanges = 'Nobody has turned this flag on or off.';

/**
 * The columns of a flag's history: when, what the flag was turned to, and by whom. What
 * it was turned to is one of two short phrases, so its column is as wide as the longer one
 * and the moment and the name share the rest, which holds a whole moment at a desk's width.
 */
export const featureFlagHistoryColumns: ColumnConfig[] = [
  { id: 'changedAt' as FieldName, header: 'When' },
  { id: 'change' as FieldName, header: 'Change', width: 104 },
  { id: 'changedBy' as FieldName, header: 'By' },
];

/** The fact of a flip each column writes. */
const columnFact: Record<string, keyof FeatureFlagChange> = {
  changedAt: 'changedAt',
  change: 'enabled',
  changedBy: 'changedBy',
};

/**
 * The columns a history is drawn with: of when, what and who, the ones some flip carries.
 * The server leaves a fact out of every flip where the person's role leaves the same
 * fact of the flag out, and a column that would be empty all the way down is not drawn.
 */
export function historyColumns(changes: readonly FeatureFlagChange[]): ColumnConfig[] {
  return featureFlagHistoryColumns.filter((column) =>
    changes.some((change) => change[columnFact[column.id]] !== undefined),
  );
}

/**
 * One flip as the table writes it: the moment to the minute, "Turned on" or "Turned off",
 * and who did it. A fact the server left out is written as nothing.
 */
export function historyRow(change: FeatureFlagChange): RecordData {
  const at = change.changedAt === undefined ? undefined : parseISO(change.changedAt);
  return {
    changedAt: at && isValid(at) ? format(at, dayAndMinuteFormat) : (change.changedAt ?? ''),
    change: change.enabled === undefined ? '' : change.enabled ? 'Turned on' : 'Turned off',
    changedBy: change.changedBy ?? '',
  };
}

/**
 * One flag's history, read only: each time the flag was turned on or off, newest first,
 * with who did it, in the library's table. It reads the flag's flips through the
 * application's client, from the flag's own route, which the server serves to whoever
 * may list the flags, each flip with the facts the person may see of the flag: a column
 * the person's role shows nothing of is not drawn, and a role that shows none of them is
 * refused by the server, in its words. The table is drawn once the answer is known; a
 * refusal, by the server or by the network, is shown in its place in the server's words.
 * Where the flag was flipped more often than one read answers, the pop-up says the older
 * flips are left out. Open it with `openFeatureFlagHistory`.
 */
@Component({
  selector: 'impulse-feature-flag-history-dialog',
  imports: [MatDialogModule, MatButtonModule, AppGridComponent],
  templateUrl: './feature-flag-history-dialog.component.html',
  styleUrls: ['./feature-flag-history-dialog.component.scss'],
})
export class FeatureFlagHistoryDialogComponent implements OnInit {
  readonly flag = inject<FeatureFlagHistoryData>(MAT_DIALOG_DATA);
  private client = inject(RESOURCE_CLIENT);
  /** The read is registered as pending work, so the application is stable only once it has answered. */
  private pendingTasks = inject(PendingTasks);

  /** The columns drawn: every one for a flag nobody has flipped, else the ones some flip carries. */
  readonly columns = computed(() => {
    const changes = this.history()?.changes ?? [];
    return changes.length === 0 ? featureFlagHistoryColumns : historyColumns(changes);
  });
  readonly empty = noFeatureFlagChanges;
  /** The flag's history as the server answered it; undefined until it has. */
  readonly history = signal<FeatureFlagHistory | undefined>(undefined);
  /** The refusal, in the server's words; undefined while the read has not failed. */
  readonly error = signal<string | undefined>(undefined);
  /** The flips as the table writes them. */
  readonly rows = computed(() => (this.history()?.changes ?? []).map(historyRow));
  /** What the pop-up says where older flips are left out; nothing where the answer holds them all. */
  readonly leftOut = computed(() => {
    const history = this.history();
    return history?.more ? `Only the newest ${history.changes.length} changes are shown.` : undefined;
  });

  ngOnInit(): void {
    void this.load();
  }

  private async load(): Promise<void> {
    const release = this.pendingTasks.add();
    try {
      this.history.set(await this.client.features.changes(this.flag.name));
    } catch (error) {
      this.error.set(error instanceof Error ? error.message : String(error));
    } finally {
      release();
    }
  }
}
