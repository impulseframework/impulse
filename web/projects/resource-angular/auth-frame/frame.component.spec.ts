import { ScrollDispatcher } from '@angular/cdk/scrolling';
import { ApplicationRef, Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { firstValueFrom } from 'rxjs';
import { FrameConfig } from './frame';
import { FrameComponent } from './frame.component';
import { provideFrameTesting, specTransport } from './frame-testing';
import { TenantService } from './tenant.service';

// The frame draws an application's banner above the bar on every signed-in page, and
// nothing there when the application gives none. It is as high as the window, and the
// page scrolls inside it, under the banner and the bar. While it is on the screen the
// place the page is in is remembered as where the person works, and no longer once it is
// gone.

@Component({
  selector: 'app-spec-banner',
  template: `<div class="spec-banner">Viewing as Cadet Cass</div>`,
})
class SpecBannerComponent {}

describe('FrameComponent', () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  const draw = async (config: Partial<FrameConfig>): Promise<ComponentFixture<FrameComponent>> => {
    await TestBed.configureTestingModule({
      imports: [FrameComponent],
      providers: [provideFrameTesting(specTransport({}), config)],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    const fixture = TestBed.createComponent(FrameComponent);
    fixture.detectChanges();
    await fixture.whenStable();
    return fixture;
  };
  const render = async (config: Partial<FrameConfig>): Promise<HTMLElement> => {
    return (await draw(config)).nativeElement as HTMLElement;
  };

  it("draws the application's banner above the bar", async () => {
    const element = await render({ banner: SpecBannerComponent });
    const main = element.querySelector('.impulse-frame-main');
    const banner = main?.querySelector('app-spec-banner');
    const bar = main?.querySelector('impulse-frame-bar');
    expect(banner?.textContent).toContain('Viewing as Cadet Cass');
    expect(bar).not.toBeNull();
    expect(banner && bar && banner.compareDocumentPosition(bar) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('draws no banner when the application gives none', async () => {
    const element = await render({});
    expect(element.querySelector('app-spec-banner')).toBeNull();
    expect(element.querySelector('.impulse-frame-main impulse-frame-bar')).not.toBeNull();
  });

  describe('what scrolls', () => {
    /** The frame's stylesheet, as the page holds it. */
    const sheet = (): string => {
      return Array.from(document.querySelectorAll('style'))
        .map((style) => style.textContent ?? '')
        .filter((text) => text.includes('.impulse-frame-scroll'))
        .join('\n');
    };
    const onPaper = /@media print\s*\{((?:[^{}]*\{[^{}]*\})*)\s*\}/g;
    /** What the frame's stylesheet declares for `selector`, on the screen or on paper, however the selector is spaced. */
    const declares = (selector: string, medium: 'screen' | 'print' = 'screen'): string => {
      const rules =
        medium === 'screen'
          ? sheet().replace(onPaper, '')
          : Array.from(sheet().matchAll(onPaper))
              .map(([, held]) => held)
              .join('\n');
      return Array.from(rules.matchAll(/([^{}]+)\{([^{}]*)\}/g))
        .filter(
          ([, named]) =>
            named
              .replace(/\[_ng[^\]]*\]/g, '')
              .replace(/\s*>\s*/g, ' > ')
              .trim() === selector,
        )
        .map(([, , declarations]) => declarations)
        .join('\n');
    };

    it('is the page, under the banner and the bar, which are never among what scrolls', async () => {
      const element = await render({ banner: SpecBannerComponent });
      const scrolls = element.querySelector('.impulse-frame-main > .impulse-frame-scroll') as HTMLElement;

      expect(scrolls).not.toBeNull();
      expect(scrolls.querySelector(':scope > main.impulse-frame-page router-outlet')).not.toBeNull();
      expect(scrolls.querySelector('impulse-frame-bar, app-spec-banner')).toBeNull();
      const bar = element.querySelector('.impulse-frame-main > impulse-frame-bar') as HTMLElement;
      expect(bar.compareDocumentPosition(scrolls) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      expect(declares('.impulse-frame-scroll')).toMatch(/overflow:\s*auto/);
    });

    it('is not the document: the frame is as high as the window, however long the page is', async () => {
      await render({});

      expect(declares('.impulse-frame-shell')).toMatch(/(^|[;\s])height:\s*100dvh/);
      expect(declares('.impulse-frame-shell')).not.toContain('min-height');
      expect(declares('.impulse-frame-main')).not.toContain('min-height');
    });

    it('takes the height the banner and the bar leave, and the page fills it or is longer', async () => {
      await render({});

      expect(declares('.impulse-frame-main')).toMatch(/flex-direction:\s*column/);
      expect(declares('.impulse-frame-scroll')).toMatch(/(^|[;\s])flex:\s*1\s*(;|$)/);
      expect(declares('.impulse-frame-scroll > .impulse-frame-page')).toMatch(/(^|[;\s])flex:\s*1 0 auto/);
    });

    it('carries what a page places by hand, so that it scrolls with the page', async () => {
      await render({});

      expect(declares('.impulse-frame-scroll')).toMatch(/position:\s*relative/);
    });

    it('is known to the overlays, so a pop-up opened from the page follows the page as it scrolls', async () => {
      const element = await render({});
      const known = Array.from(TestBed.inject(ScrollDispatcher).scrollContainers.keys()).map(
        (scrollable) => scrollable.getElementRef().nativeElement as HTMLElement,
      );

      expect(known).toContain(element.querySelector('.impulse-frame-scroll'));
    });

    it('is nothing on paper: the frame is as long as the page there', async () => {
      await render({});

      expect(declares('.impulse-frame-shell', 'print')).toMatch(/(^|[;\s])height:\s*auto/);
      expect(declares('.impulse-frame-scroll', 'print')).toMatch(/overflow:\s*visible/);
    });

    it('is named for the frame, as every element of the frame is: a rule an application writes for its own content lands on none', async () => {
      const element = await render({ banner: SpecBannerComponent });
      // What scrolls is the frame's own element, straight under Material's: Material keeps a
      // scrolling element of its own inside the side panel, which is not the frame's to name.
      const own = [
        'mat-sidenav-container',
        'mat-sidenav',
        'mat-sidenav-content',
        'mat-sidenav-content > [cdkScrollable]',
        'main',
      ].map((selector) => element.querySelector(selector) as HTMLElement);

      for (const part of own) {
        const named = Array.from(part.classList).filter((name) => !/^(mat|mdc|cdk|ng)-/.test(name));
        expect(named.length).toBeGreaterThan(0);
        expect(named.filter((name) => !name.startsWith('impulse-frame-'))).toEqual([]);
      }
      // The names the frame's own elements had, which an application's stylesheet is likely
      // to hold for its own content.
      const likely = '.page-content, .page-scroll, .main, .panel, .shell, .content, .page';
      expect(own.filter((part) => part.matches(likely))).toEqual([]);
    });

    it("keeps the room around the page itself: the application's stylesheet names it, or it is the library's own", async () => {
      await render({});

      expect(declares('.impulse-frame-scroll > .impulse-frame-page')).toMatch(
        /padding:\s*var\(--impulse-page-padding,\s*16px 24px\)/,
      );
    });

    describe('takes the keys that scroll a page while nothing has the focus', () => {
      /** A key pressed on `on`, as the browser sends it: the document hears it on its way up. */
      const press = (key: string, on: Element = document.body): KeyboardEvent => {
        const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true });
        on.dispatchEvent(event);
        return event;
      };
      const scrollerOf = async (): Promise<HTMLElement> => {
        const element = await render({});
        (document.activeElement as HTMLElement | null)?.blur();
        expect(document.activeElement).toBe(document.body);
        return element.querySelector('.impulse-frame-scroll') as HTMLElement;
      };

      for (const key of ['PageDown', 'PageUp', ' ', 'ArrowDown', 'ArrowUp', 'Home', 'End']) {
        it(`gives itself the focus at ${JSON.stringify(key)}, and leaves the key to the browser, which scrolls what has the focus`, async () => {
          const scrolls = await scrollerOf();

          const event = press(key);

          expect(document.activeElement).toBe(scrolls);
          expect(event.defaultPrevented).toBe(false);
        });
      }

      it('is no stop of the Tab key, and shows no line around the page while it has the focus', async () => {
        const scrolls = await scrollerOf();

        expect(scrolls.getAttribute('tabindex')).toBe('-1');
        expect(declares('.impulse-frame-scroll:focus')).toMatch(/outline:\s*none/);
      });

      it('leaves every other key where it is', async () => {
        await scrollerOf();

        press('Tab');
        press('a');
        press('Enter');
        press('Escape');

        expect(document.activeElement).toBe(document.body);
      });

      it('leaves a key to what has the focus', async () => {
        const scrolls = await scrollerOf();
        const field = document.createElement('input');
        scrolls.querySelector('main')?.append(field);
        field.focus();
        expect(document.activeElement).toBe(field);

        press(' ', field);
        press('Home', field);
        press('ArrowDown', field);

        expect(document.activeElement).toBe(field);
      });

      it('leaves a key that something on the page has answered', async () => {
        await scrollerOf();
        const answer = (event: Event): void => event.preventDefault();
        document.body.addEventListener('keydown', answer);

        press('PageDown');
        document.body.removeEventListener('keydown', answer);

        expect(document.activeElement).toBe(document.body);
      });
    });
  });

  it('has the place its page is in remembered as where the person works, and no longer once it is gone', async () => {
    const remembered = (): string | null => localStorage.getItem('impulse-place:/:gale');
    const fixture = await draw({});
    const tenants = TestBed.inject(TenantService);
    tenants.enter('a-id');
    await fixture.whenStable();
    expect(remembered()).toBe('a-id');
    fixture.destroy();
    tenants.enter('b-id');
    await TestBed.inject(ApplicationRef).whenStable();
    expect(remembered()).toBe('a-id');
  });
});
