import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { firstValueFrom } from 'rxjs';
import { FrameConfig } from './frame';
import { FrameComponent } from './frame.component';
import { provideFrameTesting, specTransport } from './frame-testing';

// The frame draws an application's banner above the bar on every signed-in page, and
// nothing there when the application gives none.

@Component({
  selector: 'app-spec-banner',
  template: `<div class="spec-banner">Viewing as Cadet Cass</div>`,
})
class SpecBannerComponent {}

describe('FrameComponent', () => {
  const render = async (config: Partial<FrameConfig>): Promise<HTMLElement> => {
    await TestBed.configureTestingModule({
      imports: [FrameComponent],
      providers: [provideFrameTesting(specTransport({}), config)],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    const fixture = TestBed.createComponent(FrameComponent);
    fixture.detectChanges();
    await fixture.whenStable();
    return fixture.nativeElement as HTMLElement;
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
});
