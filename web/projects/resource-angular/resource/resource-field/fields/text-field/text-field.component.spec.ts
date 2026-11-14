import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormControl, FormGroup } from '@angular/forms';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { field, FieldMeta, FieldName, ResourceMeta, RPCFieldMeta } from '@impulseframework/resource-angular/types';

import { ResourceStore } from '../../../resource-store.service';
import { TextFieldComponent } from './text-field.component';

describe('TextFieldComponent', () => {
  let component: TextFieldComponent;
  let fixture: ComponentFixture<TextFieldComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TextFieldComponent],
      providers: [provideResourceTesting(), ResourceStore],
    }).compileComponents();

    const fieldMeta: FieldMeta = { fieldName: 'callsign', displayType: 'string', required: false, isIndex: false };
    const meta: ResourceMeta = { route: 'squadrons', fields: [fieldMeta] };
    fixture = TestBed.createComponent(TextFieldComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('meta', meta);
    fixture.componentRef.setInput('fieldMeta', fieldMeta);
    fixture.componentRef.setInput('fieldConfig', field({ name: 'callsign' as FieldName, label: 'Callsign' }));
    fixture.componentRef.setInput('editMode', 'edit');
    fixture.componentRef.setInput('form', new FormGroup({ callsign: new FormControl('') }));
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('draws a field that is no secret as plain text, with no toggle', () => {
    fixture.componentRef.setInput('showField', true);
    fixture.detectChanges();
    const input = fixture.nativeElement.querySelector('input') as HTMLInputElement;
    expect(input.type).toBe('text');
    expect(input.getAttribute('autocomplete')).toBeNull();
    expect(fixture.nativeElement.querySelector('button.secret-toggle')).toBeNull();
  });
});

// A field the metadata marks secret: what is typed is masked, and the toggle shows the
// input's own text and hides it again. There is no stored value behind it to reveal.
describe('TextFieldComponent with a secret', () => {
  let fixture: ComponentFixture<TextFieldComponent>;
  const input = () => fixture.nativeElement.querySelector('input') as HTMLInputElement;
  const toggle = () => fixture.nativeElement.querySelector('button.secret-toggle') as HTMLButtonElement | null;

  function draw(editMode: 'edit' | 'view', fieldMeta: FieldMeta | RPCFieldMeta): void {
    const meta = { route: 'beacons', fields: [fieldMeta] } as ResourceMeta;
    fixture = TestBed.createComponent(TextFieldComponent);
    fixture.componentRef.setInput('meta', meta);
    fixture.componentRef.setInput('fieldMeta', fieldMeta);
    fixture.componentRef.setInput('fieldConfig', field({ name: 'accessCode' as FieldName, label: 'Access code' }));
    fixture.componentRef.setInput('editMode', editMode);
    fixture.componentRef.setInput('showField', true);
    fixture.componentRef.setInput('form', new FormGroup({ accessCode: new FormControl('') }));
    fixture.detectChanges();
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TextFieldComponent],
      providers: [provideResourceTesting(), ResourceStore],
    }).compileComponents();
  });

  it('masks what is typed, and keeps the browser from filling or correcting it', () => {
    draw('edit', {
      fieldName: 'accessCode',
      displayType: 'string',
      required: false,
      isIndex: false,
      writeOnly: true,
      secret: true,
    });
    expect(input().type).toBe('password');
    expect(input().value).toBe('');
    expect(input().getAttribute('autocomplete')).toBe('new-password');
    expect(input().getAttribute('spellcheck')).toBe('false');
    expect(toggle()?.getAttribute('aria-label')).toBe('Show what you typed');
    expect(toggle()?.getAttribute('aria-pressed')).toBe('false');
  });

  it('shows what is being typed when asked, and hides it again', () => {
    draw('edit', {
      fieldName: 'accessCode',
      displayType: 'string',
      required: false,
      isIndex: false,
      writeOnly: true,
      secret: true,
    });
    input().value = 'open-sesame';
    input().dispatchEvent(new Event('input'));
    toggle()?.click();
    fixture.detectChanges();
    expect(input().type).toBe('text');
    expect(input().value).toBe('open-sesame');
    expect(toggle()?.getAttribute('aria-label')).toBe('Hide what you typed');
    expect(toggle()?.getAttribute('aria-pressed')).toBe('true');
    toggle()?.click();
    fixture.detectChanges();
    expect(input().type).toBe('password');
    expect(input().value).toBe('open-sesame');
  });

  it('masks the box again once the form leaves edit, so the next edit starts masked', () => {
    draw('edit', {
      fieldName: 'accessCode',
      displayType: 'string',
      required: false,
      isIndex: false,
      writeOnly: true,
      secret: true,
    });
    toggle()?.click();
    fixture.detectChanges();
    expect(input().type).toBe('text');
    fixture.componentRef.setInput('editMode', 'view');
    fixture.detectChanges();
    fixture.componentRef.setInput('editMode', 'edit');
    fixture.detectChanges();
    expect(input().type).toBe('password');
    expect(toggle()?.getAttribute('aria-label')).toBe('Show what you typed');
    expect(toggle()?.getAttribute('aria-pressed')).toBe('false');
  });

  it("masks a method's request field that holds a secret", () => {
    draw('edit', { fieldName: 'accessCode', displayType: 'string', required: true, secret: true });
    expect(input().type).toBe('password');
    expect(toggle()).not.toBeNull();
  });

  it('offers no toggle where nothing is typed: a view has no input to show', () => {
    draw('view', {
      fieldName: 'accessCode',
      displayType: 'string',
      required: false,
      isIndex: false,
      writeOnly: true,
      secret: true,
    });
    expect(toggle()).toBeNull();
  });
});
