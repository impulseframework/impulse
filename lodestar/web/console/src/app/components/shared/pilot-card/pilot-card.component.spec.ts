// Demonstrates: computed.user, @feature.field, @feature.
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { createApi } from '@app/service/zz_gen_api';
import { PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { provideFrame } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { PilotCardComponent } from './pilot-card.component';

// The pilot card shows the signed-in pilot's standing, read through the caller-scoped
// computed resource. Its citation count, the card's one gated field, shows while the
// commendations flag is on and is absent while it is off, and the card asks for exactly
// the columns the digest grants. A persona with no card sees nothing.

/** One persona as the spec's server plays them: the session, the digest, the enabled flags, and the card. */
interface Persona {
  username: string;
  digest: PermissionDigest;
  enabled: string[];
  cards: Record<string, unknown>[];
}

/** The crew's List grant on the cards, field by field as the digest carries it, with the gated field when the flag is on. */
function cardDigest(gated: boolean): PermissionDigest {
  const digest: PermissionDigest = { PilotCards: { List: 'granted' } };
  for (const field of [
    'displayName',
    'clearance',
    'feeLimit',
    'certifications',
    'squadrons',
    ...(gated ? ['commendations'] : []),
  ]) {
    digest[`PilotCards.${field}`] = { List: 'granted' };
  }
  return digest;
}

const cadet: Persona = {
  username: 'cadet',
  digest: cardDigest(false),
  enabled: [],
  cards: [{ userId: 'cadet', displayName: 'Cadet Cass', clearance: 1, feeLimit: 0, certifications: [], squadrons: [] }],
};

/** Pax, whose two citations the desk holds: his card with the flag on and with it off. */
const paxCited: Persona = {
  username: 'pilot',
  digest: cardDigest(true),
  enabled: ['commendations'],
  cards: [
    {
      userId: 'pilot',
      displayName: 'Pilot Pax',
      clearance: 3,
      feeLimit: 0,
      certifications: ['hazard-3'],
      squadrons: ['Hammer'],
      commendations: 2,
    },
  ],
};

const paxDark: Persona = {
  ...paxCited,
  digest: cardDigest(false),
  enabled: [],
  cards: [
    {
      userId: 'pilot',
      displayName: 'Pilot Pax',
      clearance: 3,
      feeLimit: 0,
      certifications: ['hazard-3'],
      squadrons: ['Hammer'],
    },
  ],
};

function server(persona: Persona, request: TransportRequest): TransportResponse {
  const url = new URL(request.url, 'http://lodestar.test');
  switch (url.pathname) {
    case `${environment.apiUrl}/user/session`:
      return { status: 200, body: { authenticated: true, username: persona.username } };
    case `${environment.apiUrl}/permission-digest`:
      return { status: 200, body: persona.digest };
    case `${environment.apiUrl}/user-domains`:
      return { status: 200, body: [] };
    case `${environment.apiUrl}/features`:
      return { status: 200, body: { enabled: persona.enabled } };
    case `${environment.apiUrl}/pilot-cards`: {
      // As the server does: the named columns alone, and the gated field is unknown while the flag is off.
      const columns = url.searchParams.get('columns')?.split(',') ?? [];
      if (columns.includes('commendations') && !persona.enabled.includes('commendations')) {
        return { status: 400, body: { message: 'unknown column: commendations' } };
      }
      const rows = persona.cards.map((card) =>
        columns.length ? Object.fromEntries(Object.entries(card).filter(([key]) => columns.includes(key))) : card,
      );
      return { status: 200, body: rows, headers: { 'total-count': String(rows.length) } };
    }
    default:
      return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  }
}

describe('PilotCardComponent', () => {
  let fixture: ComponentFixture<PilotCardComponent>;
  let transport: ScriptedTransport;

  /** Runs change detection and lets pending promises land until `done` answers true, for at most `rounds` rounds. */
  const settle = async (done: () => boolean, rounds = 50): Promise<void> => {
    for (let i = 0; i < rounds && !done(); i++) {
      fixture.detectChanges();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const cardText = (): string => element().querySelector('.standing')?.textContent?.replace(/\s+/g, ' ').trim() ?? '';

  /** Signs the persona in over the scripted server (the session, the digest, the domains, the flags) and builds the card. */
  const signIn = async (persona: Persona): Promise<void> => {
    transport = scriptedTransport((request) => server(persona, request));
    await TestBed.configureTestingModule({
      imports: [PilotCardComponent],
      providers: [
        // The pilot card is a typed handle on the console's generated client.
        // SectorService reads the sector off the frame, which the tenant guard writes.
        provideRouter([]),
        provideFrame({ home: 'dashboard', menus: [] }),
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        provideNoopAnimations(),
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    fixture = TestBed.createComponent(PilotCardComponent);
    fixture.detectChanges();
    await settle(() => persona.cards.length === 0 || cardText() !== '');
  };

  it('shows nothing to a persona with no card', async () => {
    await signIn({ ...cadet, cards: [] });
    expect(element().querySelector('.pilot-card')).toBeNull();
  });

  it("shows the cadet's standing", async () => {
    await signIn(cadet);
    expect(cardText()).toContain('Cadet Cass · clearance 1');
  });

  /** The columns each pilot-cards request named, in request order. */
  const cardColumns = (): (string | null)[] =>
    transport.requests
      .map((request) => new URL(request.url, 'http://lodestar.test'))
      .filter((url) => url.pathname === `${environment.apiUrl}/pilot-cards`)
      .map((url) => url.searchParams.get('columns'));

  const cards: { name: string; persona: Persona; want: string; absent?: string; columns: string }[] = [
    {
      name: "with the commendations flag on, the card asks for the gated column the digest grants and counts Pax's citations",
      persona: paxCited,
      want: 'Cited: 2',
      columns: 'userId,certifications,clearance,commendations,displayName,feeLimit,squadrons',
    },
    {
      name: 'with the flag off, the card asks for the columns without it and the line is absent',
      persona: paxDark,
      want: 'Pilot Pax · clearance 3 · Hammer',
      absent: 'Cited',
      columns: 'userId,certifications,clearance,displayName,feeLimit,squadrons',
    },
  ];
  for (const tt of cards) {
    it(tt.name, async () => {
      await signIn(tt.persona);
      expect(cardText()).toContain(tt.want);
      if (tt.absent) {
        expect(cardText()).not.toContain(tt.absent);
      }
      expect(cardColumns()).toEqual([tt.columns]);
    });
  }
});
