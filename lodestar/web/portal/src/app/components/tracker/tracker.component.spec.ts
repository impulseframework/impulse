import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { provideFrame } from '@impulseframework/resource-angular/auth-frame';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
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
