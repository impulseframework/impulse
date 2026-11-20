import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { provideFrame, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { TrackerComponent } from './tracker.component';

describe('TrackerComponent', () => {
  let fixture: ComponentFixture<TrackerComponent>;
  let transport: ScriptedTransport;

  beforeEach(async () => {
    transport = scriptedTransport();
    await TestBed.configureTestingModule({
      imports: [TrackerComponent],
      // The tracker reads its handles and the page size off the portal's generated client,
      // and its sector off the frame (the tenant guard writes it from the address).
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        provideRouter([]),
        provideFrame({ home: 'tracker', menus: [] }),
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(TrackerComponent);
    fixture.detectChanges();
    // The resources' loaders run once the microtasks drain; whenStable would wait on the idle
    // service's keepalive interval instead, which never settles.
    await new Promise((resolve) => setTimeout(resolve));
    fixture.detectChanges();
  });

  it('creates the tracker with the page size the descriptor declares for missions', () => {
    expect(fixture.componentInstance.pageSize).toBeGreaterThan(0);
  });

  it('says so outside every sector, and asks the server for nothing before a sector is open and a grant is held', () => {
    const element: HTMLElement = fixture.nativeElement;
    expect(element.querySelector('h1')?.textContent).toContain('Your missions');
    expect(element.querySelector('.empty')?.textContent).toContain('You hold no sector yet.');
    expect(fixture.componentInstance.current()).toBe('');
    expect(fixture.componentInstance.canFile()).toBe(false);
    expect(transport.requests).toEqual([]);
  });
});

// Filing a distress call is a button over the calls the client filed, and the form it
// opens is a dialog over the page, as the library's lists open their create: the page
// itself draws no form. The dialog is titled with the button's words and carries Cancel
// and those words as its buttons; Cancel and Escape close it with nothing sent; a call
// the server takes closes it and the list is read again; a call the server refuses is
// shown in the dialog in the server's words, and the form stays as it was filled. The
// digest's field-level Create entries still decide which inputs the form draws.
//
// Demonstrates: create-form-narrowing.

/** Anvil's id: a sector's key is an id its create generates, and the name is a field. */
const anvilID = 'a1111111-1111-4111-8111-111111111111';

/** What the scripted server holds: the client's grants in Anvil, and its answer to a filed call. */
interface World {
  grants: PermissionDigest;
  write?: TransportResponse;
}

/** A client who lists the calls they filed and files one, every field theirs to write. */
const files: PermissionDigest = { DistressCalls: { List: 'granted', Create: 'granted' } };
/** The same client with the digest naming the fields they may write: the contact is not one. */
const filesTwoFields: PermissionDigest = {
  ...files,
  'DistressCalls.summary': { Create: 'granted' },
  'DistressCalls.severity': { Create: 'granted' },
};

function serverFor(world: World): (request: TransportRequest) => TransportResponse {
  return (request) => {
    const url = new URL(request.url, 'http://lodestar.test');
    switch (url.pathname) {
      case `${environment.apiUrl}/user/session`:
        return { status: 200, body: { authenticated: true, username: 'client' } };
      case `${environment.apiUrl}/permission-digest`:
        return { status: 200, body: url.searchParams.get('domain') === anvilID ? world.grants : {} };
      case `${environment.apiUrl}/user-domains`:
        return { status: 200, body: [{ domain: anvilID, name: 'Anvil' }] };
      case `${environment.apiUrl}/sectors/${anvilID}/distress-calls`:
        return { status: 200, body: [] };
      // A create rides the consolidated route as one operation.
      case `${environment.apiUrl}/resources`:
        return world.write ?? { status: 200, body: { distressCalls: ['call-1'] } };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };
}

describe('TrackerComponent files a distress call from a dialog', () => {
  let fixture: ComponentFixture<TrackerComponent>;
  let transport: ScriptedTransport;

  /** Draws the page and lets pending answers land until `done` answers true, for at most `rounds` rounds. */
  const settle = async (done: () => boolean = () => false, rounds = 10): Promise<void> => {
    for (let i = 0; i < rounds && !done(); i++) {
      fixture.detectChanges();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  /** Signs in, opens the tracker in Anvil as the tenant guard does, and waits for Anvil's grants. */
  const pageFor = async (world: World): Promise<void> => {
    transport = scriptedTransport(serverFor(world));
    await TestBed.configureTestingModule({
      imports: [TrackerComponent, NoopAnimationsModule],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        provideRouter([]),
        provideFrame({ home: 'tracker', menus: [] }),
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    TestBed.inject(TenantService).enter(anvilID);
    fixture = TestBed.createComponent(TrackerComponent);
    await settle(() => fixture.componentInstance.canFile(), 50);
  };

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const text = (e: Element | null): string => e?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
  const inDialog = <E extends HTMLElement>(selector: string): E | null =>
    document.querySelector<E>(`.cdk-overlay-container mat-dialog-container ${selector}`);
  const dialogTexts = (selector: string): string[] =>
    Array.from(document.querySelectorAll(`.cdk-overlay-container mat-dialog-container ${selector}`)).map(text);
  const dialogOpen = (): boolean => document.querySelector('.cdk-overlay-container mat-dialog-container') !== null;
  /** The operations the page sent, in order: a create is one operation on the consolidated route. */
  const writes = (): unknown[] =>
    transport.requests
      .filter((r) => r.method === 'PATCH' && r.url.replace(/\?.*$/, '') === `${environment.apiUrl}/resources`)
      .flatMap((r) => r.body as unknown[]);
  /** How many times the calls were listed. */
  const callReads = (): number =>
    transport.requests.filter(
      (r) =>
        r.method === 'GET' && r.url.replace(/\?.*$/, '') === `${environment.apiUrl}/sectors/${anvilID}/distress-calls`,
    ).length;
  /** Opens the dialog by its button, as a person does. */
  const open = async (): Promise<void> => {
    element().querySelector<HTMLButtonElement>('button.file-call')?.click();
    await settle();
  };
  /** Types into one of the dialog's inputs, as a person does. */
  const type = (name: string, value: string): void => {
    const input = inDialog<HTMLInputElement>(`input[name="${name}"]`)!;
    input.value = value;
    input.dispatchEvent(new Event('input'));
  };
  /** Fills the three fields and sends the form by its button. */
  const file = async (): Promise<void> => {
    type('summary', 'Hull breach on the lower ring');
    type('severity', '3');
    type('callerContact', 'dock 4, channel 9');
    await settle();
    inDialog<HTMLButtonElement>('button.send')?.click();
    await settle(() => false, 5);
  };

  it('draws no form on the page: filing is a button over the calls', async () => {
    await pageFor({ grants: files });

    expect(element().querySelectorAll('form, input').length).toBe(0);
    expect(text(element().querySelector('button.file-call'))).toBe('File a distress call');
    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);
  });

  it('offers no button without the Create grant', async () => {
    await pageFor({ grants: { DistressCalls: { List: 'granted' } } });
    await settle(() => callReads() > 0, 50);

    expect(element().querySelector('button.file-call')).toBeNull();
  });

  it("opens the form in a dialog under the button's words, and Cancel sends nothing", async () => {
    await pageFor({ grants: files });
    await open();

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['File a distress call']);
    expect(dialogTexts('mat-label')).toEqual(['Summary', 'Severity (1–5)', 'Your contact']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'File a distress call']);

    type('summary', 'Hull breach on the lower ring');
    inDialog<HTMLButtonElement>('button.cancel')?.click();
    await settle(() => !dialogOpen());

    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);
  });

  it('Escape closes the dialog with nothing sent, and the form opens empty the next time', async () => {
    await pageFor({ grants: files });
    await open();
    type('summary', 'Hull breach on the lower ring');

    document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle(() => !dialogOpen());
    expect(dialogOpen()).toBe(false);
    expect(writes()).toEqual([]);

    await open();
    expect(inDialog<HTMLInputElement>('input[name="summary"]')?.value).toBe('');
  });

  it('a filed call closes the dialog and the calls are read again', async () => {
    await pageFor({ grants: files });
    await settle(() => callReads() > 0, 50);
    const before = callReads();
    await open();
    await file();
    await settle(() => !dialogOpen() && callReads() > before, 50);

    expect(writes()).toEqual([
      {
        op: 'add',
        path: `/sectors/${anvilID}/distress-calls`,
        value: { summary: 'Hull breach on the lower ring', severity: 3, callerContact: 'dock 4, channel 9' },
      },
    ]);
    expect(dialogOpen()).toBe(false);
    expect(callReads()).toBe(before + 1);
  });

  it("a call the server refuses is shown in the dialog in the server's words, and the form stays as it was filled", async () => {
    const sentence = 'DistressCalls: the severity is a number from 1 to 5.';
    await pageFor({ grants: files, write: { status: 400, body: { message: sentence } } });
    await open();
    await file();
    await settle(() => dialogTexts('.refusal').length > 0, 50);

    expect(dialogOpen()).toBe(true);
    expect(dialogTexts('.refusal')).toEqual([sentence]);
    expect(inDialog<HTMLInputElement>('input[name="summary"]')?.value).toBe('Hull breach on the lower ring');
    // The page under the dialog shows nothing of it.
    expect(element().querySelector('.refusal')).toBeNull();
  });

  it('draws the inputs the digest grants: a client who may not write the contact is not asked for it', async () => {
    await pageFor({ grants: filesTwoFields });
    await open();

    expect(dialogTexts('mat-label')).toEqual(['Summary', 'Severity (1–5)']);
  });
});
