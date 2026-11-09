import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ClientBase } from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { FooterComponent } from './footer.component';

describe('FooterComponent', () => {
  let fixture: ComponentFixture<FooterComponent>;

  beforeEach(async () => {
    // The footer's release line reads the client, whose every answer names the server's release.
    const transport = scriptedTransport({ status: 200, body: undefined, headers: { 'x-api-version': 'v0.3.1' } });
    await TestBed.configureTestingModule({
      imports: [FooterComponent],
      providers: [provideResourceTesting({ transport })],
    }).compileComponents();

    fixture = TestBed.createComponent(FooterComponent);
    fixture.detectChanges();
  });

  it('creates with the footer line', () => {
    expect(fixture.nativeElement.querySelector('footer.footer')?.textContent).toContain(
      'built on the Impulse Framework',
    );
  });
  it('shows the release the server names once an answer has named it', async () => {
    expect(fixture.nativeElement.querySelector('impulse-release')?.textContent?.trim()).toBe('');
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).request('GET', 'user/session');
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('impulse-release')?.textContent?.trim()).toBe('v0.3.1');
  });
});
