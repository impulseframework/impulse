import { ApplicationRef } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideTheme, THEME_STORAGE_KEY, ThemeService } from '@impulseframework/resource-angular/ui-theme';
import { ScriptedTransport } from '@impulseframework/resource/testing';
import { firstValueFrom } from 'rxjs';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { provideFrameTesting, SpecServer, specTransport } from './frame-testing';

// The look a person chose is kept with their account: when a person's session is first
// checked the account's look is applied and remembered in the browser, once for each
// sign-in; the person's own change is written to the account, one write at a time; and
// neither happens in an impersonated session. An account with no look takes none from
// the browser, and a server that keeps no look is written nothing.

const lookPath = '/api/user/look';
const palettes = [
  { id: 'blue', label: 'Blue' },
  { id: 'workspace', label: 'Workspace' },
];

interface Setup {
  /** More of the session read's answer: the look the account holds, an impersonation record. */
  session?: Record<string, unknown>;
  /** The server keeps a look: its methods list names the path. */
  keeps?: boolean;
  /** What the server answers a look posted to it with. */
  status?: number;
}

describe('AccountLook', () => {
  let transport: ScriptedTransport;
  /** The spec's server: a spec changes what its session read answers from here. */
  let server: SpecServer;
  /** While set, a look posted to the server is answered only once it resolves. */
  let held: Promise<void> | undefined;

  const root = (): HTMLElement => document.documentElement;
  const remembered = (): unknown => JSON.parse(localStorage.getItem(THEME_STORAGE_KEY) ?? 'null');
  const posted = (): unknown[] =>
    transport.requests.filter((r) => r.method === 'POST' && r.url.endsWith(lookPath)).map((r) => r.body);
  const settle = async (): Promise<void> => {
    await TestBed.inject(ApplicationRef).whenStable();
    await new Promise((resolve) => setTimeout(resolve));
    await TestBed.inject(ApplicationRef).whenStable();
  };

  /** Reads the session again, as the keepalive and a sign-in do. */
  const check = async (): Promise<void> => {
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    await settle();
  };

  /** Starts the application over the spec's server and checks the session, as its first guard does. */
  const signIn = async ({ session = {}, keeps = true, status = 200 }: Setup = {}): Promise<ThemeService> => {
    server = {
      methods: { methods: [], ...(keeps ? { changeLook: lookPath } : {}) },
      session: { authenticated: true, username: 'gale', ...session },
      other: (url) =>
        url.pathname === lookPath ? { status, body: status === 200 ? {} : { message: 'refused' } } : undefined,
    };
    const answers = specTransport(server);
    transport = Object.assign(
      async (request: Parameters<ScriptedTransport>[0]) => {
        const response = await answers(request);
        if (held && request.method === 'POST' && request.url.endsWith(lookPath)) {
          await held;
        }
        return response;
      },
      { requests: answers.requests },
    );
    TestBed.configureTestingModule({ providers: [provideTheme({ palettes }), provideFrameTesting(transport)] });
    const theme = TestBed.inject(ThemeService);
    await check();
    return theme;
  };

  beforeEach(() => {
    localStorage.clear();
    held = undefined;
  });

  afterEach(() => {
    localStorage.clear();
    root().removeAttribute('data-palette');
    root().style.removeProperty('color-scheme');
  });

  it("applies the account's look once the session is checked, and remembers it in the browser", async () => {
    const theme = await signIn({ session: { look: { scheme: 'dark', palette: 'workspace' } } });
    expect(theme.scheme()).toBe('dark');
    expect(theme.palette()).toBe('workspace');
    expect(root().style.getPropertyValue('color-scheme')).toBe('dark');
    expect(root().getAttribute('data-palette')).toBe('workspace');
    expect(remembered()).toEqual({ scheme: 'dark', palette: 'workspace' });
    expect(posted()).toEqual([]);
  });

  it("takes the account's look over the one the browser remembered", async () => {
    localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify({ scheme: 'dark', palette: 'workspace' }));
    const theme = await signIn({ session: { look: { scheme: 'light', palette: 'blue' } } });
    expect(theme.scheme()).toBe('light');
    expect(theme.palette()).toBe('blue');
    expect(remembered()).toEqual({ scheme: 'light', palette: 'blue' });
  });

  it('keeps what the browser remembered for an account that holds no look, and writes the account nothing', async () => {
    localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify({ scheme: 'dark', palette: 'workspace' }));
    const theme = await signIn();
    expect(theme.scheme()).toBe('dark');
    expect(theme.palette()).toBe('workspace');
    expect(posted()).toEqual([]);
  });

  it('keeps a palette the application does not offer out, and takes the scheme', async () => {
    const theme = await signIn({ session: { look: { scheme: 'dark', palette: 'violet' } } });
    expect(theme.scheme()).toBe('dark');
    expect(theme.palette()).toBe('blue');
  });

  it("writes the person's change to the account, the whole look each time", async () => {
    const theme = await signIn();
    theme.setScheme('dark');
    await settle();
    theme.setPalette('workspace');
    await settle();
    expect(posted()).toEqual([
      { scheme: 'dark', palette: 'blue' },
      { scheme: 'dark', palette: 'workspace' },
    ]);
  });

  it('writes nothing to a server that keeps no look', async () => {
    const theme = await signIn({ keeps: false });
    theme.setScheme('dark');
    await settle();
    expect(posted()).toEqual([]);
    expect(theme.scheme()).toBe('dark');
  });

  it("leaves the change in force when the server refuses it: the browser's copy holds", async () => {
    const theme = await signIn({ status: 403 });
    theme.setScheme('dark');
    await settle();
    expect(posted()).toEqual([{ scheme: 'dark', palette: 'blue' }]);
    expect(theme.scheme()).toBe('dark');
    expect(remembered()).toEqual({ scheme: 'dark', palette: 'blue' });
  });

  it("takes the account's look once for a sign-in: a later read of the session takes nothing back", async () => {
    // The account holds light, the person picks dark, and the write is refused: the
    // keepalive's read still answers light, and the page stays as the person left it.
    const theme = await signIn({ session: { look: { scheme: 'light', palette: 'workspace' } }, status: 403 });
    theme.setScheme('dark');
    await settle();
    await check();
    expect(theme.scheme()).toBe('dark');
    expect(theme.palette()).toBe('workspace');
    expect(root().style.getPropertyValue('color-scheme')).toBe('dark');
    expect(remembered()).toEqual({ scheme: 'dark', palette: 'workspace' });
  });

  it("takes the next person's look when another person signs in on the same page", async () => {
    const theme = await signIn({ session: { look: { scheme: 'dark', palette: 'workspace' } } });
    server.session = { authenticated: false };
    await check();
    // Signed out, the browser's copy stays: it is the login page's.
    expect(theme.scheme()).toBe('dark');
    server.session = { authenticated: true, username: 'ada', look: { scheme: 'light', palette: 'blue' } };
    await check();
    expect(theme.scheme()).toBe('light');
    expect(theme.palette()).toBe('blue');
    expect(posted()).toEqual([]);
  });

  it("takes the same person's look again at their next sign-in on the page", async () => {
    const theme = await signIn({ session: { look: { scheme: 'dark', palette: 'workspace' } } });
    server.session = { authenticated: false };
    await check();
    theme.setScheme('light');
    await settle();
    server.session = { authenticated: true, username: 'gale', look: { scheme: 'dark', palette: 'workspace' } };
    await check();
    expect(theme.scheme()).toBe('dark');
    // What was picked while signed out was nobody's to keep.
    expect(posted()).toEqual([]);
  });

  it('writes one change at a time, in order, and only the latest of those made meanwhile', async () => {
    const theme = await signIn();
    let release = (): void => undefined;
    held = new Promise<void>((resolve) => (release = resolve));
    theme.setScheme('dark');
    await settle();
    theme.setPalette('workspace');
    await settle();
    theme.setScheme('light');
    await settle();
    // The first write is on its way; the two changes made since wait as one.
    expect(posted()).toEqual([{ scheme: 'dark', palette: 'blue' }]);
    release();
    await settle();
    expect(posted()).toEqual([
      { scheme: 'dark', palette: 'blue' },
      { scheme: 'light', palette: 'workspace' },
    ]);
  });

  const impersonation = { actor: 'ada', principalKind: 'user', principal: 'gale', expiresAt: '2099-01-01T00:00:00Z' };

  it('takes no look from the person an impersonated session acts as', async () => {
    const theme = await signIn({ session: { impersonation, look: { scheme: 'dark', palette: 'workspace' } } });
    expect(theme.scheme()).toBe('light');
    expect(theme.palette()).toBe('blue');
    expect(remembered()).toBeNull();
  });

  it('writes no look for the person an impersonated session acts as', async () => {
    const theme = await signIn({ session: { impersonation } });
    theme.setScheme('dark');
    await settle();
    expect(posted()).toEqual([]);
    expect(theme.scheme()).toBe('dark');
  });

  it("takes the person's own look once the session stops acting for someone else", async () => {
    const theme = await signIn({ session: { impersonation, look: { scheme: 'dark', palette: 'workspace' } } });
    server.session = { authenticated: true, username: 'ada', look: { scheme: 'dark', palette: 'blue' } };
    await check();
    expect(theme.scheme()).toBe('dark');
    expect(theme.palette()).toBe('blue');
    expect(remembered()).toEqual({ scheme: 'dark', palette: 'blue' });
  });
});
