import { ApplicationRef, Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { firstValueFrom } from 'rxjs';
import { FrameConfig } from './frame';
import { FrameComponent } from './frame.component';
import { provideFrameTesting, specTransport } from './frame-testing';
import { TenantService } from './tenant.service';

// The frame draws an application's banner above the bar on every signed-in page, and
// nothing there when the application gives none. While it is on the screen the place the
// page is in is remembered as where the person works, and no longer once it is gone.

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
