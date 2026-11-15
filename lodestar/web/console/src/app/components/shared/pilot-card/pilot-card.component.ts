/** Demonstrates: computed.user, @feature.field, @feature. */
import { ChangeDetectionStrategy, Component, computed, inject } from '@angular/core';
import { MatCardModule } from '@angular/material/card';
import { Feature } from '@app/service/zz_gen_api';
import { FeatureDirective } from '@impulseframework/resource-angular/auth-feature';
import { grantedColumns, SectorService } from '@components/sector/sector.service';

/**
 * The signed-in pilot's standing, on the home page: the caller-scoped read, a computed
 * resource whose List yields the one row that is "mine", chosen server-side by the
 * identity the permission check ran as, so under a view-as session it shows the viewed
 * person's standing, not the actor's. The card asks for the columns the digest grants
 * (grantedColumns): its citation count is a field behind the commendations flag
 * (@feature.field), which the server serves only when it is named and the digest names
 * exactly while the flag is on, so the read follows the flag, and the person who flips it
 * sees their own card read again. A person with no card (no grant on the cards, or no
 * pilot row) sees nothing.
 */
@Component({
  selector: 'app-pilot-card',
  imports: [FeatureDirective, MatCardModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './pilot-card.component.html',
  styleUrl: './pilot-card.component.scss',
})
export class PilotCardComponent {
  private sectors = inject(SectorService);

  private cards = this.sectors.globalList((api) => api.pilotCards, grantedColumns);
  readonly card = computed(() => this.cards.value()[0]);

  /**
   * The flag the citation count sits behind (@feature.field on PilotCards): the generated
   * member, never the string, so a misspelled flag fails to compile. The enabled set and
   * the digest the card's columns follow are loaded together at sign-in and refreshed
   * together by a flip, so the count is in the row whenever the flag says to show it.
   */
  readonly commendations = Feature.Commendations;
}
