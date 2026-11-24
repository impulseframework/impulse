import { Component, input } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormGroup } from '@angular/forms';
import { ConfigElement, field, FieldName, section } from '@impulseframework/resource-angular/types';

import { ResourceFieldComponent } from '../resource-field/resource-field.component';
import { ResourceLayoutComponent } from './resource-layout.component';

describe('ResourceLayoutComponent', () => {
  let component: ResourceLayoutComponent;
  let fixture: ComponentFixture<ResourceLayoutComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ResourceLayoutComponent],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceLayoutComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('element', { type: 'padding', cols: 6 });
    fixture.componentRef.setInput('meta', {});
    fixture.componentRef.setInput('editMode', 'view');
    fixture.componentRef.setInput('form', new FormGroup({}));
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});

/** Stands in for a field's own component, which draws the field's frame: the layout's box is what is under test. */
@Component({ selector: 'impulse-resource-field', template: '' })
class FieldStandInComponent {
  fieldConfig = input<unknown>();
  meta = input<unknown>();
  fieldClass = input<unknown>();
  editMode = input<unknown>();
  form = input<unknown>();
  formDataState = input<unknown>();
  pristineValue = input<unknown>();
  data = input<unknown>();
}

// A section's box is drawn around a field that stands in no section on a record's page and
// its create form; in an action's dialog the field is drawn in its own frame alone.
describe('ResourceLayoutComponent, the box around a field', () => {
  const username = field({ name: 'username' as FieldName });

  async function drawn(element: ConfigElement, framed?: boolean): Promise<HTMLElement> {
    TestBed.overrideComponent(ResourceLayoutComponent, {
      remove: { imports: [ResourceFieldComponent] },
      add: { imports: [FieldStandInComponent] },
    });
    await TestBed.configureTestingModule({ imports: [ResourceLayoutComponent] }).compileComponents();
    const drawing = TestBed.createComponent(ResourceLayoutComponent);
    drawing.componentRef.setInput('element', element);
    drawing.componentRef.setInput('meta', {});
    drawing.componentRef.setInput('editMode', 'edit');
    drawing.componentRef.setInput('form', new FormGroup({}));
    if (framed !== undefined) {
      drawing.componentRef.setInput('framed', framed);
    }
    drawing.detectChanges();
    return drawing.nativeElement as HTMLElement;
  }

  it("draws a field that stands in no section in a section's box where nothing is said, as a page's form does", async () => {
    const host = await drawn(username);
    const box = host.querySelector('impulse-resource-field')?.closest('.resource-layout-container');
    expect(box).toBeTruthy();
    expect(box?.classList.contains('resource-layout-container-odd')).toBe(true);
  });

  it('draws a field that stands in no section in its own frame alone where it is not framed', async () => {
    const host = await drawn(username, false);
    expect(host.querySelector('.section-elements.dense-1 > impulse-resource-field')).toBeTruthy();
    expect(host.querySelector('[class*="resource-layout-container"]')).toBeNull();
  });

  it('keeps the labeled box of a section an author declares where it is not framed', async () => {
    const host = await drawn(section({ label: 'Account', children: [username] }), false);
    const box = host.querySelector('impulse-resource-field')?.closest('.resource-layout-container');
    expect(box).toBeTruthy();
    expect(box?.querySelector(':scope > .label')?.textContent?.trim()).toBe('Account');
  });
});
