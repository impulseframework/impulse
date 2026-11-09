import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ClientBase } from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { API_VERSION } from '@impulseframework/resource-angular/types';
import { ReleaseComponent } from './release.component';

// The line shows the release the server named on its last answer and nothing before one,
// and when this build names another release it says which build the tab runs.

describe('ReleaseComponent', () => {
  let fixture: ComponentFixture<ReleaseComponent>;
  let client: ClientBase;

  async function render(build: string): Promise<void> {
    const transport = scriptedTransport({ status: 200, body: { methods: [] }, headers: { 'x-api-version': 'v0.3.1' } });
    await TestBed.configureTestingModule({
      imports: [ReleaseComponent],
      providers: [provideResourceTesting({ transport }), { provide: API_VERSION, useValue: build }],
    }).compileComponents();
    fixture = TestBed.createComponent(ReleaseComponent);
    client = TestBed.inject(RESOURCE_CLIENT);
    fixture.detectChanges();
  }

  function text(): string {
    return (fixture.nativeElement.textContent as string).replace(/\s+/g, ' ').trim();
  }

  it('renders nothing before the server has answered', async () => {
    await render('v0.3.1');
    expect(text()).toBe('');
  });

  it('shows the release the server named on its answer', async () => {
    await render('v0.3.1');
    await client.request('GET', 'user/methods');
    fixture.detectChanges();
    expect(text()).toBe('v0.3.1');
    expect(fixture.nativeElement.querySelector('.impulse-release-build')).toBeNull();
  });

  it("says which build the tab runs when it is not the server's", async () => {
    await render('v0.3.0');
    await client.request('GET', 'user/methods');
    fixture.detectChanges();
    expect(text()).toBe('v0.3.1 (this tab runs v0.3.0)');
  });

  it('shows the release alone for a local build of the application', async () => {
    await render('dev');
    await client.request('GET', 'user/methods');
    fixture.detectChanges();
    expect(text()).toBe('v0.3.1');
  });

  it('reads a release named before it was created', async () => {
    await render('0.3.1');
    await client.request('GET', 'user/methods');
    const later = TestBed.createComponent(ReleaseComponent);
    later.detectChanges();
    expect((later.nativeElement.textContent as string).trim()).toBe('v0.3.1');
  });
});
