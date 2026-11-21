import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { TokensComponent } from '@impulseframework/resource-angular/tokens';
import { ApiKeysPageComponent } from './api-keys-page.component';
import { notNamedFor } from './frame-testing';

/** Stands in for the tokens component, which has specs of its own and asks the server. */
@Component({ selector: 'impulse-tokens', template: '' })
class TokensStandInComponent {}

describe('ApiKeysPageComponent', () => {
  let fixture: ComponentFixture<ApiKeysPageComponent>;

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;

  beforeEach(() => {
    TestBed.configureTestingModule({ imports: [ApiKeysPageComponent] });
    TestBed.overrideComponent(ApiKeysPageComponent, {
      remove: { imports: [TokensComponent] },
      add: { imports: [TokensStandInComponent] },
    });
    fixture = TestBed.createComponent(ApiKeysPageComponent);
    fixture.detectChanges();
  });

  it('heads the tokens with the words of the menu entry that leads here', () => {
    expect(element().querySelector('h1')?.textContent?.trim()).toBe('Your API keys');
    expect(element().querySelector('impulse-tokens')).not.toBeNull();
  });

  it("names its one element for the page: an application's rule for its own .api-keys lands on none", () => {
    expect(notNamedFor('impulse-api-keys-page-', element())).toEqual([]);
    expect(element().querySelector('section.impulse-api-keys-page-content')).not.toBeNull();
  });
});
