import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormControl, FormGroup } from '@angular/forms';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { field, FieldMeta, FieldName, ResourceMeta } from '@impulseframework/resource-angular/types';

import { ResourceStore } from '../../../resource-store.service';
import { BytesFieldComponent } from './bytes-field.component';

// A bytes field presents the value's size and a download of the decoded bytes, never the
// base64 and never an input. jsdom has no object URLs, so the spec supplies a pair that
// records what the field creates and revokes.

/** The base64 of 32 zero bytes: a SHA-256 digest. */
const digest32 = btoa(String.fromCharCode(...new Array<number>(32).fill(0)));
const digestMeta: FieldMeta = { fieldName: 'digest', displayType: 'bytes', required: true, isIndex: false };
const meta: ResourceMeta = { route: 'mission-documents', fields: [digestMeta] };

describe('BytesFieldComponent', () => {
  let fixture: ComponentFixture<BytesFieldComponent>;
  const created: { url: string; size: number }[] = [];
  const revoked: string[] = [];
  const originalCreate = URL.createObjectURL;
  const originalRevoke = URL.revokeObjectURL;

  const create = async (value: string | null): Promise<void> => {
    await TestBed.configureTestingModule({
      imports: [BytesFieldComponent],
      providers: [provideResourceTesting(), ResourceStore],
    }).compileComponents();
    fixture = TestBed.createComponent(BytesFieldComponent);
    fixture.componentRef.setInput('meta', meta);
    fixture.componentRef.setInput('fieldMeta', digestMeta);
    fixture.componentRef.setInput('fieldConfig', field({ name: 'digest' as FieldName, label: 'Digest' }));
    fixture.componentRef.setInput('editMode', 'view');
    fixture.componentRef.setInput('showField', true);
    fixture.componentRef.setInput('form', new FormGroup({ digest: new FormControl<string | null>(value) }));
    fixture.detectChanges();
  };

  beforeEach(() => {
    created.length = 0;
    revoked.length = 0;
    URL.createObjectURL = (blob: Blob | MediaSource): string => {
      const url = `blob:test/${created.length}`;
      created.push({ url, size: blob instanceof Blob ? blob.size : -1 });
      return url;
    };
    URL.revokeObjectURL = (url: string): void => {
      revoked.push(url);
    };
  });

  afterEach(() => {
    URL.createObjectURL = originalCreate;
    URL.revokeObjectURL = originalRevoke;
  });

  it('shows the size and a download of the decoded bytes named after the field', async () => {
    await create(digest32);
    const element: HTMLElement = fixture.nativeElement;
    expect(fixture.componentInstance.byteCount()).toBe(32);
    expect(element.querySelector<HTMLInputElement>('input[matinput]')?.value).toBe('32 B');
    expect(element.querySelector('mat-label')?.textContent?.trim()).toBe('Digest');
    const anchor = element.querySelector<HTMLAnchorElement>('a.download');
    expect(anchor?.getAttribute('href')).toBe('blob:test/0');
    expect(anchor?.getAttribute('download')).toBe('digest');
    expect(created).toEqual([{ url: 'blob:test/0', size: 32 }]);
    expect(element.textContent).not.toContain(digest32);
  });

  it('revokes the download URL when destroyed', async () => {
    await create(digest32);
    fixture.destroy();
    expect(revoked).toEqual(['blob:test/0']);
  });

  it('shows nothing for a null value', async () => {
    await create(null);
    const element: HTMLElement = fixture.nativeElement;
    expect(element.querySelector<HTMLInputElement>('input[matinput]')?.value).toBe('');
    expect(element.querySelector('a.download')).toBeNull();
    expect(created).toEqual([]);
  });
});
