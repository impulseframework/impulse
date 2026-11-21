import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { Router } from '@angular/router';
import { TenantService } from './tenant.service';

/**
 * The refusal page: where a navigation that cannot open a tenant is sent, under the
 * tenant's own URL. It says one of two things.
 *
 * An address naming a tenant the session cannot reach lands here once the guard has asked
 * the server again, and the page says 404 Not Found and nothing more, as the server's
 * domain guard answers: a tenant that does not exist and one the session holds nothing in
 * look the same, and the address holds the tenant's id, which no page shows. A tenant
 * whose permission digest the server refuses to read (its 403 or 404) lands here the same
 * way. The session is never switched to another tenant; the place switcher is the way
 * into one.
 *
 * A tenant the session reaches whose permission digest could not be read for any other
 * reason lands here too (TenantService.unread holds the address that was asked for), and
 * the page says that the page could not be loaded, with a button that asks for it again.
 * Nothing of the page that was asked for is drawn, since what the person holds there is
 * not known.
 *
 * Both hold for a page that is already open when a read of its tenant's digest fails:
 * TenantService sends the browser here in the open page's stead.
 */
@Component({
  selector: 'impulse-tenant-refused',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [MatButtonModule],
  templateUrl: './tenant-refused.component.html',
  styleUrl: './tenant-refused.component.scss',
})
export class TenantRefusedComponent {
  private tenants = inject(TenantService);
  private router = inject(Router);

  /** The address to ask for again where the tenant could not be read; empty for a refusal. */
  protected unread = this.tenants.unread;

  /** Whether the page is being asked for again: the button waits for the answer. */
  protected trying = signal(false);

  /**
   * tryAgain asks for the page again, through the tenant guard. A read that works opens
   * the page, and this page says what it said until that page is drawn; one that fails
   * again leaves this page as it is.
   */
  protected async tryAgain(): Promise<void> {
    this.trying.set(true);
    try {
      await this.router.navigateByUrl(this.unread());
    } finally {
      this.trying.set(false);
    }
  }
}
