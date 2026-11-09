import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';

import { SEARCH_DEBOUNCE_MS, SearchBoxComponent } from './search-box.component';

// The search box commits the typed text after a pause, at Enter at once, and the clear
// button commits nothing; the hint reads the host's total as the matches.

describe('SearchBoxComponent', () => {
  let fixture: ComponentFixture<SearchBoxComponent>;
  let committed: string[];

  const input = (): HTMLInputElement =>
    (fixture.nativeElement as HTMLElement).querySelector('input') as HTMLInputElement;
  const typeText = (text: string): void => {
    input().value = text;
    input().dispatchEvent(new Event('input'));
    fixture.detectChanges();
  };
  const pause = async (ms = SEARCH_DEBOUNCE_MS + 50): Promise<void> => {
    await new Promise((resolve) => setTimeout(resolve, ms));
    fixture.detectChanges();
  };

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [SearchBoxComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(SearchBoxComponent);
    committed = [];
    fixture.componentInstance.textChange.subscribe((text) => committed.push(text));
    fixture.componentRef.setInput('placeholder', 'Search Name, Contact Email');
    fixture.detectChanges();
  });

  it('names the fields it searches in the placeholder', () => {
    expect(input().placeholder).toBe('Search Name, Contact Email');
    expect(input().getAttribute('maxlength')).toBe('200');
  });

  it('commits the text once after the pause, not per keystroke', async () => {
    typeText('n');
    typeText('ng');
    typeText('nguyen');
    expect(committed).toEqual([]);
    await pause();
    expect(committed).toEqual(['nguyen']);
  });

  it('commits at Enter without waiting', () => {
    typeText('merid');
    input().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
    expect(committed).toEqual(['merid']);
  });

  it('commits a clear, and the draft empties', async () => {
    fixture.componentRef.setInput('value', 'nguyen');
    fixture.detectChanges();
    expect(input().value).toBe('nguyen');
    (
      (fixture.nativeElement as HTMLElement).querySelector('button[aria-label="Clear the search"]') as HTMLButtonElement
    ).click();
    fixture.detectChanges();
    expect(committed).toEqual(['']);
    expect(input().value).toBe('');
  });

  it('blank text is a clear, and the same text again is nothing', async () => {
    fixture.componentRef.setInput('value', 'nguyen');
    fixture.detectChanges();
    typeText('   ');
    await pause();
    typeText('nguyen');
    input().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
    expect(committed).toEqual(['']);
  });

  it("reads the total as the matches while text is committed, then the host's hint", () => {
    fixture.componentRef.setInput('hint', 'Not searched for your role: Contact Email');
    fixture.componentRef.setInput('total', 12);
    fixture.detectChanges();
    expect(fixture.componentInstance.hintText()).toBe('Not searched for your role: Contact Email');
    fixture.componentRef.setInput('value', 'nguyen');
    fixture.detectChanges();
    expect(fixture.componentInstance.hintText()).toBe('12 rows match. Not searched for your role: Contact Email');
    fixture.componentRef.setInput('total', 1);
    fixture.componentRef.setInput('hint', '');
    fixture.detectChanges();
    expect(fixture.componentInstance.hintText()).toBe('1 row matches');
  });

  it('keeps a keystroke in the box, and lets Escape through', () => {
    const letter = new KeyboardEvent('keydown', { key: 'a', bubbles: true });
    const escape = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true });
    let reached = 0;
    (fixture.nativeElement as HTMLElement).addEventListener('keydown', () => reached++);
    input().dispatchEvent(letter);
    input().dispatchEvent(escape);
    expect(reached).toBe(1);
  });
});
