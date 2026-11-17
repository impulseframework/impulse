import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ActionButtonConfig, actionButtonConfig, RecordData } from '@impulseframework/resource-angular/types';

import { TableButtonComponent } from './table-button.component';

/**
 * The control a screen reader announces under the role with the name: a button or a link
 * whose accessible name is the words given. The name is the control's `aria-label`, else
 * the text it holds that is not hidden from assistive technology.
 */
const byRole = (root: HTMLElement, role: 'button' | 'link', name: string): HTMLElement | undefined => {
  const heard = (node: Element): string => {
    return Array.from(node.childNodes)
      .map((child) => {
        if (!(child instanceof Element)) {
          return child.textContent ?? '';
        }
        return child.getAttribute('aria-hidden') === 'true' ? '' : (child.getAttribute('aria-label') ?? heard(child));
      })
      .join('');
  };
  return Array.from(root.querySelectorAll<HTMLElement>(role === 'button' ? 'button' : 'a[href]')).find((control) => {
    return (control.getAttribute('aria-label') ?? heard(control)).trim() === name;
  });
};

// The button is an icon alone, and Material hides an icon from assistive technology, so
// the label is the control's accessible name in both of its forms; the tooltip shows the
// label to the pointer as before.
describe('TableButtonComponent', () => {
  let component: TableButtonComponent<RecordData | null>;
  let fixture: ComponentFixture<TableButtonComponent<RecordData | null>>;
  let element: HTMLElement;

  const create = async (config: ActionButtonConfig, row: RecordData | null, viewRoute = ''): Promise<void> => {
    await TestBed.configureTestingModule({
      imports: [TableButtonComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();

    fixture = TestBed.createComponent(TableButtonComponent<RecordData | null>);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('config', config);
    fixture.componentRef.setInput('rowData', row);
    fixture.componentRef.setInput('viewRoute', viewRoute);
    fixture.detectChanges();
    element = fixture.nativeElement as HTMLElement;
  };
  const settle = async (): Promise<void> => {
    fixture.detectChanges();
    await fixture.whenStable();
    await new Promise((resolve) => setTimeout(resolve, 0));
    fixture.detectChanges();
  };
  /** The words of the tooltip that is open, null while none is. */
  const tooltip = (): string | null => {
    return (
      document.body
        .querySelector('.cdk-overlay-container .mat-mdc-tooltip-show .mat-mdc-tooltip-surface')
        ?.textContent?.trim() ?? null
    );
  };

  it('should create', async () => {
    await create(actionButtonConfig({ label: 'Open', icon: 'open_in_new', actionType: 'link' }), null);
    expect(component).toBeTruthy();
  });

  it('the function form is a button found by its label, and pressing it runs the action with the row', async () => {
    const ran: unknown[] = [];
    await create(
      actionButtonConfig({
        label: 'Delete',
        icon: 'delete',
        actionType: 'function',
        action: (row): void => {
          ran.push(row);
        },
      }),
      { id: 'm-1', name: 'Anvil run' },
    );

    const button = byRole(element, 'button', 'Delete');
    expect(button).toBeDefined();
    button?.click();
    expect(ran).toEqual([{ id: 'm-1', name: 'Anvil run' }]);
  });

  it('the link form is one link found by its label, with no second control inside it', async () => {
    await create(actionButtonConfig({ label: 'Open', icon: 'open_in_new', actionType: 'link' }), null, '/missions');

    const link = byRole(element, 'link', 'Open');
    expect(link?.getAttribute('href')).toBe('/missions');
    expect(element.querySelectorAll('a, button')).toHaveLength(1);
  });

  const forms: { name: string; config: ActionButtonConfig; viewRoute: string }[] = [
    {
      name: 'the function form',
      config: actionButtonConfig({ label: 'Delete', icon: 'delete', actionType: 'function' }),
      viewRoute: '',
    },
    {
      name: 'the link form',
      config: actionButtonConfig({ label: 'Open', icon: 'open_in_new', actionType: 'link' }),
      viewRoute: '/missions',
    },
  ];

  for (const tt of forms) {
    it(`${tt.name} still shows its label as the tooltip`, async () => {
      await create(tt.config, null, tt.viewRoute);
      element.querySelector('.parent')?.dispatchEvent(new MouseEvent('mouseenter'));
      await settle();
      expect(tooltip()).toBe(tt.config.label);
    });
  }
});
