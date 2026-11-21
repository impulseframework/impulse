import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { AWAITS_ICON_FONT, ICON_FONT_FAMILY, IconFontService } from './icon-font.service';
import { provideTheme } from './provide-theme';

// The service over the real document and a set of fonts that stands in for the browser's
// (jsdom keeps none): each face says where it is (not asked for, on its way, arrived,
// failed), counts how often it was asked for, and arrives or fails when the test says.

/** One face a page declares, as a browser hands it over. */
class Face {
  status = 'unloaded';
  asked = 0;
  private answer!: { arrive: () => void; fail: (reason: Error) => void };
  private readonly answered = new Promise<void>((arrive, fail) => {
    this.answer = { arrive, fail };
  });

  constructor(
    readonly family: string,
    status = 'unloaded',
  ) {
    this.status = status;
  }

  /** What a browser answers with once the face has arrived, whether or not anything asked for it. */
  get loaded(): Promise<void> {
    return this.answered;
  }

  /** Asks for the face, as the service does and as a page does by drawing in it. */
  load(): Promise<void> {
    this.asked++;
    if (this.status === 'unloaded') {
      this.status = 'loading';
    }
    return this.answered;
  }

  async arrive(): Promise<void> {
    this.status = 'loaded';
    this.answer.arrive();
    await Promise.resolve();
  }

  async fail(): Promise<void> {
    this.status = 'error';
    this.answer.fail(new Error('the font could not be fetched'));
    await Promise.resolve();
  }
}

const root = (): HTMLElement => document.documentElement;
const marked = (): boolean => root().classList.contains(AWAITS_ICON_FONT);

describe('IconFontService', () => {
  let faces: Face[];
  let told: Map<() => void, string>;

  /** A font of the page starts on its way: the browser tells whoever listens. */
  const starts = (): void => {
    for (const [listener, event] of told) {
      if (event === 'loading') {
        listener();
      }
    }
  };
  /** Starts the application with provideTheme, as an application does. */
  const start = (): IconFontService => {
    TestBed.configureTestingModule({ providers: [provideTheme()] });
    return TestBed.inject(IconFontService);
  };

  beforeEach(() => {
    faces = [];
    told = new Map();
    Object.defineProperty(document, 'fonts', {
      configurable: true,
      value: {
        forEach: (each: (face: Face) => void): void => {
          faces.forEach((face) => each(face));
        },
        addEventListener: (event: string, listener: () => void): void => {
          told.set(listener, event);
        },
        removeEventListener: (_: string, listener: () => void): void => {
          told.delete(listener);
        },
      },
    });
  });

  afterEach(() => {
    TestBed.resetTestingModule();
    delete (document as { fonts?: unknown }).fonts;
    root().classList.remove(AWAITS_ICON_FONT);
  });

  it("asks for the icons' font as the application starts, and marks the root for as long as it is on its way", async () => {
    const icons = new Face(ICON_FONT_FAMILY);
    faces.push(icons);
    start();
    expect(icons.asked).toBe(1);
    expect(marked()).toBe(true);
    // However long that is: nothing here counts seconds.
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(marked()).toBe(true);
    await icons.arrive();
    expect(marked()).toBe(false);
  });

  it('reads the family as a browser hands it back, in the quotes the stylesheet wrote', async () => {
    for (const family of [`"${ICON_FONT_FAMILY}"`, `'${ICON_FONT_FAMILY}'`]) {
      const icons = new Face(family);
      faces = [icons];
      start();
      expect(marked()).toBe(true);
      await icons.arrive();
      expect(marked()).toBe(false);
      TestBed.resetTestingModule();
    }
  });

  it("asks for no other font, and marks nothing for one that is on its way: a page's letters are not its icons", () => {
    const letters = [new Face('Roboto'), new Face('Roboto', 'loading'), new Face('Material Icons Outlined')];
    faces.push(...letters);
    start();
    expect(letters.map((face) => face.asked)).toEqual([0, 0, 0]);
    expect(marked()).toBe(false);
  });

  it('marks nothing where the font has arrived already, and asks for nothing', () => {
    const icons = new Face(ICON_FONT_FAMILY, 'loaded');
    faces.push(icons);
    start();
    expect(icons.asked).toBe(0);
    expect(marked()).toBe(false);
  });

  it('marks the root for a font the page asked for itself, by drawing an icon, before the application started', async () => {
    const icons = new Face(ICON_FONT_FAMILY, 'loading');
    faces.push(icons);
    start();
    expect(marked()).toBe(true);
    await icons.arrive();
    expect(marked()).toBe(false);
  });

  it('looks again each time a font of the page starts on its way, so a stylesheet that arrives after the application is not missed', async () => {
    start();
    expect(marked()).toBe(false);
    // The stylesheet arrives and the page draws its first words: Roboto starts on its way.
    const icons = new Face(ICON_FONT_FAMILY);
    faces.push(new Face('Roboto', 'loading'), icons);
    starts();
    expect(icons.asked).toBe(1);
    expect(marked()).toBe(true);
    // Another font starts while the icons' is on its way: it is not asked for again.
    starts();
    expect(icons.asked).toBe(1);
    await icons.arrive();
    expect(marked()).toBe(false);
    // And one that starts after it has arrived marks nothing.
    starts();
    expect(icons.asked).toBe(1);
    expect(marked()).toBe(false);
  });

  it('keeps the mark until every face of the font has arrived', async () => {
    const first = new Face(ICON_FONT_FAMILY);
    const second = new Face(ICON_FONT_FAMILY);
    faces.push(first, second);
    start();
    await first.arrive();
    expect(marked()).toBe(true);
    await second.arrive();
    expect(marked()).toBe(false);
  });

  it('takes the mark off for a font that fails: it is not on its way', async () => {
    const icons = new Face(ICON_FONT_FAMILY);
    faces.push(icons);
    start();
    expect(marked()).toBe(true);
    await icons.fail();
    expect(marked()).toBe(false);
    // A font that has failed is not asked for again when another starts.
    starts();
    expect(icons.asked).toBe(1);
    expect(marked()).toBe(false);
  });

  it('starts once, however often it is told to', async () => {
    const icons = new Face(ICON_FONT_FAMILY);
    faces.push(icons);
    const service = start();
    service.start();
    expect(told.size).toBe(1);
    expect(icons.asked).toBe(1);
    await icons.arrive();
    expect(marked()).toBe(false);
  });

  it('listens no more once the application has left', () => {
    start();
    expect(told.size).toBe(1);
    TestBed.resetTestingModule();
    expect(told.size).toBe(0);
  });

  it('leaves a browser that keeps no account of its fonts alone', () => {
    delete (document as { fonts?: unknown }).fonts;
    expect(() => start()).not.toThrow();
    expect(marked()).toBe(false);
  });
});
