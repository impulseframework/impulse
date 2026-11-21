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
    const main = element.querySelector('.main');
    const banner = main?.querySelector('app-spec-banner');
    const bar = main?.querySelector('impulse-frame-bar');
    expect(banner?.textContent).toContain('Viewing as Cadet Cass');
    expect(bar).not.toBeNull();
    expect(banner && bar && banner.compareDocumentPosition(bar) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('draws no banner when the application gives none', async () => {
    const element = await render({});
    expect(element.querySelector('app-spec-banner')).toBeNull();
    expect(element.querySelector('.main impulse-frame-bar')).not.toBeNull();
  });

  describe('what scrolls', () => {
    /** The frame's stylesheet, as the page holds it. */
    const sheet = (): string => {
      return Array.from(document.querySelectorAll('style'))
        .map((style) => style.textContent ?? '')
        .filter((text) => text.includes('.page-scroll'))
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
      const scrolls = element.querySelector('.main > .page-scroll') as HTMLElement;

      expect(scrolls).not.toBeNull();
      expect(scrolls.querySelector(':scope > main.page-content router-outlet')).not.toBeNull();
      expect(scrolls.querySelector('impulse-frame-bar, app-spec-banner')).toBeNull();
      const bar = element.querySelector('.main > impulse-frame-bar') as HTMLElement;
      expect(bar.compareDocumentPosition(scrolls) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      expect(declares('.page-scroll')).toMatch(/overflow:\s*auto/);
    });

    it('is not the document: the frame is as high as the window, however long the page is', async () => {
      await render({});

      expect(declares('.shell')).toMatch(/(^|[;\s])height:\s*100dvh/);
      expect(declares('.shell')).not.toContain('min-height');
      expect(declares('.main')).not.toContain('min-height');
    });

    it('takes the height the banner and the bar leave, and the page fills it or is longer', async () => {
      await render({});

      expect(declares('.main')).toMatch(/flex-direction:\s*column/);
      expect(declares('.page-scroll')).toMatch(/(^|[;\s])flex:\s*1\s*(;|$)/);
      expect(declares('.page-scroll > .page-content')).toMatch(/(^|[;\s])flex:\s*1 0 auto/);
    });

    it('carries what a page places by hand, so that it scrolls with the page', async () => {
      await render({});

      expect(declares('.page-scroll')).toMatch(/position:\s*relative/);
    });

    it('is known to the overlays, so a pop-up opened from the page follows the page as it scrolls', async () => {
      const element = await render({});
      const known = Array.from(TestBed.inject(ScrollDispatcher).scrollContainers.keys()).map(
        (scrollable) => scrollable.getElementRef().nativeElement as HTMLElement,
      );

      expect(known).toContain(element.querySelector('.page-scroll'));
    });

    it('is nothing on paper: the frame is as long as the page there', async () => {
      await render({});

      expect(declares('.shell', 'print')).toMatch(/(^|[;\s])height:\s*auto/);
      expect(declares('.page-scroll', 'print')).toMatch(/overflow:\s*visible/);
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
