import { ChangeDetectionStrategy, Component } from '@angular/core';

/**
 * The refusal page: where an address naming a tenant the session cannot reach lands, under
 * the tenant's own URL, once the tenant guard has asked the server again. It says 404 Not
 * Found and nothing more, as the server's domain guard answers: a tenant that does not
 * exist and one the session holds nothing in look the same, and the address holds the
 * tenant's id, which no page shows. The session is never switched to another tenant; the
 * place switcher is the way into one.
 */
@Component({
  selector: 'impulse-tenant-refused',
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './tenant-refused.component.html',
  styleUrl: './tenant-refused.component.scss',
})
export class TenantRefusedComponent {}
