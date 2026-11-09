import { Component, computed, inject, OnInit, PendingTasks, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule } from '@angular/material/dialog';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { ExecutePermission, FeatureFlag, setFeatureMethod } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';

const dayMs = 86_400_000;

/**
 * How long a flag has been on in this environment, from when it was last flipped: "on
 * for 3 days in this environment", "on for 1 day", "on for less than a day". A timestamp
 * that cannot be read says only that the flag is on.
 */
export function onForSentence(updatedAt: string, now: Date = new Date()): string {
  const since = new Date(updatedAt).getTime();
  if (Number.isNaN(since)) {
    return 'on in this environment';
  }
  const days = Math.floor(Math.max(0, now.getTime() - since) / dayMs);
  if (days === 0) {
    return 'on for less than a day in this environment';
  }
  return `on for ${days} ${days === 1 ? 'day' : 'days'} in this environment`;
}

/**
 * The feature flags dialog: every flag the application declares, with its description,
 * a toggle, and, while it is on, how long it has been on in this environment. It reads the
 * flags resource through the application's client and flips a flag through the generated
 * method, so the permission digest decides what it may do: without Execute on the method
 * the list is read-only and the toggles disabled. After a flip succeeds the client reloads
 * the enabled set and the digest, so the pages of the person flipping follow at once, and
 * the dialog reads the flags again for the new timestamp. A refusal, by the server or by
 * the network, is shown in the dialog in the server's words. Open it with
 * `openFeatureFlagsDialog`; the application decides where the link lives.
 */
@Component({
  selector: 'impulse-feature-flags-dialog',
  imports: [MatDialogModule, MatButtonModule, MatSlideToggleModule],
  templateUrl: './feature-flags-dialog.component.html',
  styleUrls: ['./feature-flags-dialog.component.scss'],
})
export class FeatureFlagsDialogComponent implements OnInit {
  private client = inject(RESOURCE_CLIENT);
  private auth = inject(AuthService);
  /** The read and the flip are registered as pending work, so the application is stable only once they have answered. */
  private pendingTasks = inject(PendingTasks);

  /** Every declared flag, by name, as last read. */
  flags = signal<FeatureFlag[]>([]);
  /** True until the first read answers. */
  loading = signal(true);
  /** The last refusal, in the server's words; undefined while nothing has failed since. */
  error = signal<string | undefined>(undefined);
  /** The flag whose flip is in flight; the toggles are disabled meanwhile. */
  pending = signal<string | undefined>(undefined);
  /** Whether the digest grants Execute on the flip method: the toggles are enabled only then. */
  canSet = computed(() => this.auth.hasPermission({ resource: setFeatureMethod, permission: ExecutePermission }));

  ngOnInit(): void {
    void this.tracked(() => this.load());
  }

  /** How long the flag has been on, for the row of an enabled flag. */
  onFor(flag: FeatureFlag): string {
    return onForSentence(flag.updatedAt);
  }

  /**
   * Flips one flag. The client refreshes the enabled set and the digest after the write;
   * the dialog reads the flags again afterwards whatever happened, so a refused flip
   * leaves the toggle where the server says it is.
   */
  toggle(flag: FeatureFlag, enabled: boolean): Promise<void> {
    return this.tracked(async () => {
      this.pending.set(flag.name);
      this.error.set(undefined);
      try {
        await this.client.features.setFeature(flag.name, enabled);
      } catch (error) {
        this.error.set(messageOf(error));
      } finally {
        await this.load();
        this.pending.set(undefined);
      }
    });
  }

  /** Runs one piece of work as a pending task of the application, released when it settles. */
  private async tracked<T>(work: () => Promise<T>): Promise<T> {
    const release = this.pendingTasks.add();
    try {
      return await work();
    } finally {
      release();
    }
  }

  private async load(): Promise<void> {
    try {
      this.flags.set(await this.client.features.flags());
    } catch (error) {
      this.error.set(messageOf(error));
    } finally {
      this.loading.set(false);
    }
  }
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
