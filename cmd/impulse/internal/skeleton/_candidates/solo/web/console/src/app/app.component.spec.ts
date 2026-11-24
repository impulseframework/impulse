import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { RouterOutlet } from '@angular/router';
import { FrameComponent } from '@impulseframework/resource-angular/auth-frame';
import { IdleService } from '@impulseframework/resource-angular/auth-idle-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { AppComponent } from './app.component';

// The shell marks the notices' container while the page on the screen sits in the frame, so
// the stylesheet puts the notices under the frame's bar there and at the top of the screen on
// a page with no bar, as the sign-in and sign-out pages are. The frame needs the
// application's own providers, which a spec does not load, so an object of its class stands
// in for it: the shell asks only whether the page shown is one.

describe('AppComponent', () => {
  let fixture: ComponentFixture<AppComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [AppComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();

    fixture = TestBed.createComponent(AppComponent);
    fixture.detectChanges();
  });

  afterEach(() => {
    TestBed.inject(IdleService).stop();
  });

  it("marks the notices' container framed only while the page shown is the frame", () => {
    const container = (fixture.nativeElement as HTMLElement).querySelector('.alert-container');
    const outlet = fixture.debugElement.query(By.directive(RouterOutlet)).injector.get(RouterOutlet);
    expect(container?.classList.contains('framed')).toBe(false);

    outlet.activateEvents.emit(Object.create(FrameComponent.prototype));
    fixture.detectChanges();
    expect(container?.classList.contains('framed')).toBe(true);

    outlet.activateEvents.emit({});
    fixture.detectChanges();
    expect(container?.classList.contains('framed')).toBe(false);
  });
});
