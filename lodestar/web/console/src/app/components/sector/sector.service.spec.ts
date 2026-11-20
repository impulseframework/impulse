import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { ResourceRef } from '@angular/core';
import { provideRouter } from '@angular/router';
import { TestBed } from '@angular/core/testing';
import { Permissions, Resources } from '@app/service/zz_gen_constants';
import { createApi } from '@app/service/zz_gen_api';
import {
  ApiError,
  ClientBase,
  Domain,
  PermissionDigest,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { provideFrame, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { grantedColumns, SectorService, serverWords } from './sector.service';

// A global reader (globalList, globalPage) asks for its list when the digest grants it and
// again only when the answer to that global question changes. The open sector's digest
// landing after sign-in, a reload of the same digests, and a change of sector all leave a
// global grant as it was, so none of them reads the list again; a digest that withholds the
// grant leaves the reader idle at its default, and one that later grants it reads once.

/** The global digest grants the pilot cards when `granted`; every sector's digest grants missions. */
let granted = true;
/** The card fields the global digest enumerates under the grant, when it enumerates any. */
let cardFields: string[] = [];
const sectorDigest: PermissionDigest = { Missions: { List: 'granted', Read: 'granted' } };
/** The answer to a read of a sector's missions, when it is not the one mission. */
let missionsAnswer: TransportResponse | undefined;

/** The global digest: the grant on the cards, with a field-level entry per enumerated field. */
function globalDigest(): PermissionDigest {
  if (!granted) {
    return {};
  }
  const digest: PermissionDigest = { PilotCards: { List: 'granted' } };
  for (const field of cardFields) {
    digest[`PilotCards.${field}`] = { List: 'granted' };
  }
  return digest;
}

/** The two sectors' ids: a sector's key is an id its create generates, and the name is a field. */
const anvilID = 'a1111111-1111-4111-8111-111111111111';
const bastionID = 'b2222222-2222-4222-8222-222222222222';

/** The server as the spec plays it: the session, the digests, two lit sectors, and the pilot cards. */
function server(request: TransportRequest): TransportResponse {
  const url = new URL(request.url, 'http://lodestar.test');
  switch (url.pathname) {
    case `${environment.apiUrl}/user/session`:
      return { status: 200, body: { authenticated: true, username: 'pilot' } };
    case `${environment.apiUrl}/permission-digest`:
      if (url.searchParams.get('domain')) {
        return { status: 200, body: sectorDigest };
      }
      return { status: 200, body: globalDigest() };
    case `${environment.apiUrl}/user-domains`:
      return {
        status: 200,
        body: [
          { domain: anvilID, name: 'Anvil' },
          { domain: bastionID, name: 'Bastion' },
        ],
      };
    case `${environment.apiUrl}/pilot-cards`:
      return { status: 200, body: [{ id: 'p1', callsign: 'Kestrel' }], headers: { 'total-count': '1' } };
    case `${environment.apiUrl}/sectors/${anvilID}/missions`:
    case `${environment.apiUrl}/sectors/${bastionID}/missions`:
      return missionsAnswer ?? { status: 200, body: [{ id: 'm1', title: 'Tow' }], headers: { 'total-count': '1' } };
    default:
      return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  }
}

describe('SectorService global readers', () => {
  let transport: ScriptedTransport;

  /** The pilot-cards requests as URLs, in order. */
  const pilotCardRequests = (): URL[] =>
    transport.requests
      .map((r) => new URL(r.url, 'http://lodestar.test'))
      .filter((url) => url.pathname === `${environment.apiUrl}/pilot-cards`);

  /** How many times the pilot cards were asked for, whatever the query. */
  const pilotCardReads = (): number => pilotCardRequests().length;

  /** The columns each pilot-cards request named. */
  const pilotCardColumns = (): (string | null)[] => pilotCardRequests().map((url) => url.searchParams.get('columns'));

  beforeEach(() => {
    cardFields = [];
  });

  /** Runs the root effects and lets pending promises land until `done` answers true, for at most `rounds` rounds. */
  const settle = async (done: () => boolean, rounds = 50): Promise<void> => {
    for (let i = 0; i < rounds && !done(); i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    TestBed.tick();
  };

  /** Signs in over the scripted server (the session, the global digest, the domains) and hands over the service. */
  const signIn = async (): Promise<SectorService> => {
    transport = scriptedTransport(server);
    TestBed.configureTestingModule({
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        provideHttpClient(),
        provideHttpClientTesting(),
        // The sector is the frame's: the library's tenant service, which the tenant guard writes.
        provideRouter([]),
        provideFrame({ home: 'dashboard', menus: [] }),
      ],
    });
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    return TestBed.inject(SectorService);
  };

  // The readers are built in an injection context, as a component's field initializer builds them.
  const readers: { name: string; build: (sectors: SectorService) => ResourceRef<unknown> }[] = [
    { name: 'globalList', build: (sectors) => sectors.globalList((api) => api.pilotCards) },
    { name: 'globalPage', build: (sectors) => sectors.globalPage((api) => api.pilotCards) },
  ];

  for (const tt of readers) {
    describe(tt.name, () => {
      it('reads a granted list once: the sector digests landing, a digest reload, and a sector change read nothing more', async () => {
        granted = true;
        const sectors = await signIn();
        const cards = TestBed.runInInjectionContext(() => tt.build(sectors));
        await settle(() => cards.status() === 'resolved');
        expect(pilotCardReads()).toBe(1);

        // Entering a sector loads its digest, as the tenant guard's does; it has
        // landed once the sector's own grant answers.
        await settle(() => sectors.can(Permissions.List, Resources.Missions));
        expect(pilotCardReads()).toBe(1);

        // The same digests again, and another sector entered and its digest landed.
        await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.refresh();
        await settle(() => false, 5);
        TestBed.inject(TenantService).enter(bastionID);
        await settle(() => sectors.current() === bastionID && sectors.can(Permissions.List, Resources.Missions));
        await settle(() => false, 5);
        expect(pilotCardReads()).toBe(1);
        expect(cards.status()).toBe('resolved');
      });

      it('idles at its default without the grant and reads once when a reloaded digest grants it', async () => {
        granted = false;
        const sectors = await signIn();
        const cards = TestBed.runInInjectionContext(() => tt.build(sectors));
        await settle(() => sectors.can(Permissions.List, Resources.Missions));
        expect(cards.status()).toBe('idle');
        expect(pilotCardReads()).toBe(0);

        granted = true;
        await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
        await settle(() => cards.status() === 'resolved');
        expect(pilotCardReads()).toBe(1);
      });
    });
  }

  it('answers an empty list from a globalList without the grant', async () => {
    granted = false;
    const sectors = await signIn();
    const cards = TestBed.runInInjectionContext(() => sectors.globalList((api) => api.pilotCards));
    await settle(() => sectors.can(Permissions.List, Resources.Missions));
    expect(cards.value()).toEqual([]);
    expect(pilotCardReads()).toBe(0);
  });

  // A query built from the digest (grantedColumns) names the key and the granted columns;
  // a reload that enumerates the same fields reads nothing more, and one that adds a field
  // (the commendations flag turned on, the digest refreshed by the flip) reads again with it.
  it('a reader with grantedColumns follows the digest: the same fields hold it still, a new field reads again', async () => {
    granted = true;
    cardFields = ['displayName', 'clearance'];
    const sectors = await signIn();
    const cards = TestBed.runInInjectionContext(() => sectors.globalList((api) => api.pilotCards, grantedColumns));
    await settle(() => cards.status() === 'resolved');
    expect(pilotCardColumns()).toEqual(['userId,clearance,displayName']);

    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.refresh();
    await settle(() => false, 5);
    expect(pilotCardColumns()).toEqual(['userId,clearance,displayName']);

    cardFields = ['displayName', 'clearance', 'commendations'];
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.refresh();
    await settle(() => pilotCardReads() === 2);
    expect(pilotCardColumns()).toEqual(['userId,clearance,displayName', 'userId,clearance,commendations,displayName']);
    expect(cards.status()).toBe('resolved');
  });
});

// A sector reader (sectorList, sectorPage) asks for its list when the open sector's digest
// grants it, and again only when the sector or the answer to that question changes. The
// sector's digest is loaded again each time the sector is entered; a reader that read again
// for it would empty and refill a page already drawn. A page asks the service whether it
// knows what it will draw (known, settled) and holds itself back until it does.
describe('SectorService sector readers', () => {
  let transport: ScriptedTransport;

  beforeEach(() => {
    missionsAnswer = undefined;
  });

  /** How many times a sector's missions were asked for. */
  const missionReads = (sector: string): number =>
    transport.requests.filter(
      (r) => new URL(r.url, 'http://lodestar.test').pathname === `${environment.apiUrl}/sectors/${sector}/missions`,
    ).length;

  /** Runs the root effects and lets pending promises land until `done` answers true, for at most `rounds` rounds. */
  const settle = async (done: () => boolean, rounds = 50): Promise<void> => {
    for (let i = 0; i < rounds && !done(); i++) {
      TestBed.tick();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    TestBed.tick();
  };

  /** Signs in over the scripted server and enters Anvil, as the tenant guard does. */
  const enterAnvil = async (): Promise<SectorService> => {
    transport = scriptedTransport(server);
    TestBed.configureTestingModule({
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        provideHttpClient(),
        provideHttpClientTesting(),
        provideRouter([]),
        provideFrame({ home: 'dashboard', menus: [] }),
      ],
    });
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    TestBed.inject(TenantService).enter(anvilID);
    return TestBed.inject(SectorService);
  };

  const readers: { name: string; build: (sectors: SectorService) => ResourceRef<unknown> }[] = [
    { name: 'sectorList', build: (sectors) => sectors.sectorList((sector) => sector.missions) },
    { name: 'sectorPage', build: (sectors) => sectors.sectorPage((sector) => sector.missions) },
  ];

  for (const tt of readers) {
    describe(tt.name, () => {
      it("reads a granted list once: the sector's digest landing again and a digest reload read nothing more", async () => {
        const sectors = await enterAnvil();
        const missions = TestBed.runInInjectionContext(() => tt.build(sectors));
        await settle(() => missions.status() === 'resolved' && missionReads(anvilID) === 1);
        expect(missionReads(anvilID)).toBe(1);

        // The same digest again, as entering the sector again loads it, and every digest reloaded.
        const permissions = TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions;
        await permissions.loadDigest(anvilID as Domain);
        TestBed.tick();
        expect(missions.status()).toBe('resolved');
        await permissions.refresh();
        await settle(() => false, 5);
        expect(missions.status()).toBe('resolved');
        expect(missionReads(anvilID)).toBe(1);
      });

      it('reads again in another sector', async () => {
        const sectors = await enterAnvil();
        const missions = TestBed.runInInjectionContext(() => tt.build(sectors));
        await settle(() => missions.status() === 'resolved' && missionReads(anvilID) === 1);

        TestBed.inject(TenantService).enter(bastionID);
        await settle(() => missions.status() === 'resolved' && missionReads(bastionID) === 1);
        expect(missionReads(anvilID)).toBe(1);
        expect(missionReads(bastionID)).toBe(1);
      });
    });
  }

  it('known answers no until the digest of the open sector is in, and settled until the lists have answered', async () => {
    const sectors = await enterAnvil();
    const missions = TestBed.runInInjectionContext(() => sectors.sectorList((sector) => sector.missions));

    // Signed in, the sector entered, its digest still on its way.
    expect(sectors.known()).toBe(false);
    expect(sectors.settled(missions)).toBe(false);

    // The digest is in and grants the list, so the list is on its way: not settled yet.
    await settle(() => sectors.known());
    expect(sectors.known()).toBe(true);
    TestBed.tick();
    expect(missions.status()).toBe('loading');
    expect(sectors.settled(missions)).toBe(false);

    await settle(() => missions.status() === 'resolved');
    expect(sectors.settled(missions)).toBe(true);

    // Read again after a change, the list keeps what it showed and the page stays as drawn.
    missions.reload();
    expect(missions.status()).toBe('reloading');
    expect(sectors.settled(missions)).toBe(true);
  });

  it("settled holds for a list the server refused, and saidBy gives the server's words and no status code", async () => {
    missionsAnswer = { status: 403, body: { message: 'user cadet does not have List on Missions' } };
    const sectors = await enterAnvil();
    const missions = TestBed.runInInjectionContext(() => sectors.sectorList((sector) => sector.missions));
    await settle(() => missions.status() === 'error');

    expect(sectors.settled(missions)).toBe(true);
    expect(sectors.saidBy(missions)).toBe('user cadet does not have List on Missions');
  });
});

describe('serverWords', () => {
  const refused = (status: number, body: unknown): ApiError => new ApiError('POST', '/api/x', status, body);

  it("says the server's own message", () => {
    expect(serverWords(refused(409, { message: 'the squadron is grounded' }))).toBe('the squadron is grounded');
    expect(serverWords(refused(413, 'the file is too large'))).toBe('the file is too large');
  });

  it('says the server gave no reason where it gave none, never a status code', () => {
    expect(serverWords(refused(409, undefined))).toBe('The server gave no reason.');
    expect(serverWords(refused(409, { expenses: '3', fee: '2' }))).toBe('The server gave no reason.');
    expect(serverWords(refused(500, ''))).toBe('The server gave no reason.');
  });

  it('says what another error says', () => {
    expect(serverWords(new Error('offline'))).toBe('offline');
  });
});
