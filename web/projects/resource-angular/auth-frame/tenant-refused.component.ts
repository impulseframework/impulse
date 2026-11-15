import { ChangeDetectionStrategy, Component, computed, inject, resource } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { ApiError, Domain, ResourceDescriptor } from '@impulseframework/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { map } from 'rxjs';
import { Frame, inSentence } from './frame';

/** The server's answer about a tenant: whether it serves it to the session, in its words. */
export interface TenantAnswer {
  served: boolean;
  message: string;
}

/**
 * The refusal page: where a URL naming a tenant the session cannot reach lands, under the
 * tenant's own URL. It asks the server what a page there would have asked, one row of the
 * first tenant's resource the API lists, and shows the answer in the server's words: with
 * concealed tenants, the 404 every route under the tenant answers, the same for a tenant
 * that does not exist and for one the session holds nothing in. The session is never
 * switched to another tenant; the place switcher is the way into one.
 */
@Component({
  selector: 'impulse-tenant-refused',
  imports: [RouterLink],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './tenant-refused.component.html',
  styleUrl: './tenant-refused.component.scss',
})
export class TenantRefusedComponent {
  private client = inject(RESOURCE_CLIENT);
  readonly frame = inject(Frame);
  readonly word = this.frame.tenantWord;
  readonly lower = inSentence;

  /** The tenant the URL names. */
  readonly tenant = toSignal(
    inject(ActivatedRoute).paramMap.pipe(map((params) => params.get(this.frame.param) ?? '')),
    {
      initialValue: '',
    },
  );

  /** Where the tenant opens, should the server serve it after all. */
  readonly home = computed(() => ['/', this.frame.segment, this.tenant(), this.frame.config.home]);

  // The question: one row of the first tenant's resource the API lists.
  private probe = Object.values(this.client.descriptor.resources).find(
    (descriptor): descriptor is ResourceDescriptor =>
      descriptor.scope === 'domain' && descriptor.operations.includes('list'),
  );

  /** The server's answer for the tenant. */
  readonly answer = resource({
    params: () => ({ tenant: this.tenant() }),
    loader: ({ params }): Promise<TenantAnswer> => this.ask(params.tenant),
  });

  private async ask(tenant: string): Promise<TenantAnswer> {
    const probe = this.probe;
    if (!probe) {
      return { served: false, message: `the API lists nothing under a ${inSentence(this.word.one)}` };
    }
    try {
      await this.client.define<Record<string, unknown>, string[], 'list'>(probe, tenant as Domain).list({ limit: 1 });
      return { served: true, message: '' };
    } catch (err) {
      if (err instanceof ApiError) {
        // 404 is the domain guard's answer; anything else means the tenant is served.
        return { served: err.status !== 404, message: `${err.status}: ${err.message}` };
      }
      throw err;
    }
  }
}
