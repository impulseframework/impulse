import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { API_VERSION } from '@impulseframework/resource-angular/types';
import {
  aboutDialogDefaults,
  AboutDialogComponent,
  AboutFacts,
  aboutLine,
  openAboutDialog,
} from './about-dialog.component';
import { FrameConfig } from './frame';
import { notNamedFor, provideFrameTesting } from './frame-testing';

// About is a pop-up: the application's name, the release the server runs with the build
// this tab runs when the two differ, and the environment wherever the server names one,
// which it does everywhere but production. Copy puts the three on the clipboard as one
// line for a problem report; where the browser offers no clipboard the line is shown to
// copy by hand.

/** What the server's answers carry: its release, and its environment where it names one. */
interface Server {
  release: string;
  environment?: string;
}

describe('AboutDialogComponent', () => {
  let fixture: ComponentFixture<AboutDialogComponent>;

  /** Builds the pop-up over a server that has answered once, as it has for anyone signed in. */
  const open = async (server: Server, build = 'dev', config: Partial<FrameConfig> = {}): Promise<void> => {
    const headers: Record<string, string> = { 'x-api-version': server.release };
    if (server.environment !== undefined) {
      headers['x-environment'] = server.environment;
    }
    const transport = scriptedTransport({ status: 200, body: { methods: [] }, headers });
    await TestBed.configureTestingModule({
      imports: [AboutDialogComponent],
      providers: [
        provideFrameTesting(transport, config),
        { provide: API_VERSION, useValue: build },
        { provide: MatDialogRef, useValue: { close: () => undefined } },
      ],
    }).compileComponents();
    await TestBed.inject(RESOURCE_CLIENT).request('GET', 'user/methods');
    fixture = TestBed.createComponent(AboutDialogComponent);
    fixture.detectChanges();
  };

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const words = (selector: string): string | undefined =>
    element().querySelector(selector)?.textContent?.replace(/\s+/g, ' ').trim();
  const terms = (): string[] =>
    Array.from(element().querySelectorAll('dt')).map((term) => term.textContent?.trim() ?? '');
  const settle = async (): Promise<void> => {
    await new Promise((resolve) => setTimeout(resolve, 0));
    fixture.detectChanges();
  };
  /** Stands a clipboard in for the browser's, or none, for one case. */
  const withClipboard = async (clipboard: unknown, run: () => Promise<void>): Promise<void> => {
    Object.defineProperty(navigator, 'clipboard', { value: clipboard, configurable: true });
    try {
      await run();
    } finally {
      Reflect.deleteProperty(navigator, 'clipboard');
    }
  };

  it('names the application, the release, and the environment a test server says it runs in', async () => {
    await open({ release: 'v0.3.1', environment: 'tst' });

    expect(words('[mat-dialog-title]')).toBe('About Lodestar');
    expect(terms()).toEqual(['Release', 'Environment']);
    expect(words('.impulse-about-dialog-release')).toBe('v0.3.1');
    expect(words('.impulse-about-dialog-environment')).toBe('tst');
    expect(words('.impulse-about-dialog-note')).toBe('When you report a problem, include the release.');
  });

  it('names no environment in production, where the server names none', async () => {
    await open({ release: 'v0.3.1' });

    expect(terms()).toEqual(['Release']);
    expect(element().querySelector('.impulse-about-dialog-environment')).toBeNull();
    expect(element().textContent).not.toContain('Environment');
  });

  it("still says which build the tab runs when that is not the server's", async () => {
    await open({ release: 'v0.3.1', environment: 'tst' }, 'v0.3.0');

    expect(words('.impulse-about-dialog-release')).toBe('v0.3.1 (this tab runs v0.3.0)');
  });

  it('shows an environment whose name the server sent encoded as the name itself', async () => {
    await open({ release: 'v0.3.1', environment: 'Morgan%20Wells%20demo' });

    expect(words('.impulse-about-dialog-environment')).toBe('Morgan Wells demo');
  });

  it("names every element for the pop-up: an application's rule for its own .about or .about-note lands on none", async () => {
    await open({ release: 'v0.3.1', environment: 'tst' });

    expect(notNamedFor('impulse-about-dialog-', element())).toEqual([]);
    expect(element().querySelector('dl.impulse-about-dialog-facts')).not.toBeNull();
  });

  it('is headed About alone where the application gives no name', async () => {
    await open({ release: 'v0.3.1' }, 'dev', { name: undefined });

    expect(words('[mat-dialog-title]')).toBe('About');
  });

  it('Copy puts the name, the release and the environment on the clipboard as one line, and says so', async () => {
    const writeText = vi.fn(async () => undefined);
    await withClipboard({ writeText }, async () => {
      await open({ release: 'v0.3.1', environment: 'tst' }, 'v0.3.0');
      expect(element().querySelector('.impulse-about-dialog-copied')).toBeNull();

      element().querySelector<HTMLButtonElement>('button.impulse-about-dialog-copy')?.click();
      await settle();

      expect(writeText.mock.calls).toEqual([['Lodestar, release v0.3.1 (this tab runs v0.3.0), environment tst']]);
      expect(words('.impulse-about-dialog-copied')).toBe('Copied.');
      expect(element().querySelector('.impulse-about-dialog-by-hand')).toBeNull();
    });
  });

  it('Copy in production puts the name and the release, and no environment', async () => {
    const writeText = vi.fn(async () => undefined);
    await withClipboard({ writeText }, async () => {
      await open({ release: 'v0.3.1' });

      element().querySelector<HTMLButtonElement>('button.impulse-about-dialog-copy')?.click();
      await settle();

      expect(writeText.mock.calls).toEqual([['Lodestar, release v0.3.1']]);
    });
  });

  for (const tt of [
    { name: 'the browser offers no clipboard', clipboard: undefined },
    {
      name: 'the browser refuses',
      clipboard: {
        writeText: async (): Promise<void> => {
          throw new Error('the document is not focused');
        },
      },
    },
  ]) {
    it(`shows the line to copy by hand where ${tt.name}, and does not say it was copied`, async () => {
      await withClipboard(tt.clipboard, async () => {
        await open({ release: 'v0.3.1', environment: 'tst' });

        element().querySelector<HTMLButtonElement>('button.impulse-about-dialog-copy')?.click();
        await settle();
        await settle();

        const field = element().querySelector<HTMLInputElement>('.impulse-about-dialog-by-hand input');
        expect(field?.value).toBe('Lodestar, release v0.3.1, environment tst');
        expect(field?.readOnly).toBe(true);
        expect(element().querySelector('.impulse-about-dialog-copied')).toBeNull();
      });
    });
  }
});

describe('aboutLine', () => {
  const cases: { name: string; facts: AboutFacts; want: string }[] = [
    {
      name: 'a test environment',
      facts: { name: 'Lodestar', release: 'v0.3.1', build: 'v0.3.1', environment: 'tst' },
      want: 'Lodestar, release v0.3.1, environment tst',
    },
    {
      name: 'production names no environment',
      facts: { name: 'Lodestar', release: 'v0.3.1', build: 'v0.3.1' },
      want: 'Lodestar, release v0.3.1',
    },
    {
      name: 'a tab kept open across a release says which build it runs',
      facts: { name: 'Lodestar', release: 'v0.3.1', build: 'v0.3.0', environment: 'tst' },
      want: 'Lodestar, release v0.3.1 (this tab runs v0.3.0), environment tst',
    },
    {
      name: 'a leading v aside, one release is one release',
      facts: { name: 'Lodestar', release: 'v0.3.1', build: '0.3.1' },
      want: 'Lodestar, release v0.3.1',
    },
    {
      name: 'a local build names no release of its own',
      facts: { name: 'Lodestar', release: 'dev', environment: 'development' },
      want: 'Lodestar, release dev, environment development',
    },
    {
      name: 'an application with no name opens with the release',
      facts: { release: 'v0.3.1', environment: 'tst' },
      want: 'Release v0.3.1, environment tst',
    },
    { name: 'a server that has not answered names no release', facts: { name: 'Lodestar' }, want: 'Lodestar' },
    {
      name: "an application's name is copied as it is written, a small first letter kept",
      facts: { name: 'eDock', release: 'v0.3.1', environment: 'tst' },
      want: 'eDock, release v0.3.1, environment tst',
    },
    {
      name: 'an application with an empty name opens with the release too',
      facts: { name: '', release: 'v0.3.1' },
      want: 'Release v0.3.1',
    },
  ];
  for (const tt of cases) {
    it(tt.name, () => {
      expect(aboutLine(tt.facts)).toBe(tt.want);
    });
  }
});

describe('openAboutDialog', () => {
  it('opens the pop-up at its default size, which a config of the caller overrides', () => {
    const opened: { component: unknown; config: unknown }[] = [];
    const dialog = {
      open: (component: unknown, config: unknown) => {
        opened.push({ component, config });
        return { close: () => undefined } as unknown as MatDialogRef<AboutDialogComponent>;
      },
    } as unknown as MatDialog;

    openAboutDialog(dialog);
    openAboutDialog(dialog, { width: '320px' });

    expect(opened).toEqual([
      { component: AboutDialogComponent, config: aboutDialogDefaults },
      { component: AboutDialogComponent, config: { ...aboutDialogDefaults, width: '320px' } },
    ]);
  });
});
