import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { iconCellKey, RecordData } from '@impulseframework/resource-angular/types';

import { AppGridComponent } from './grid.component';
import { ColumnFilter, ColumnFilterInputs, RowKey } from './grid-types';

// The grid identifies a row by `rowKey`, the caller's function from a row to its identity,
// or by the row's position in the page when the caller passes none. The track expression,
// the selection, and the expansion are keyed by it, so two rows alike in every field, or
// alike in a field called `id`, are still two rows.

describe('AppGridComponent', () => {
  let fixture: ComponentFixture<AppGridComponent>;
  let component: AppGridComponent;

  const create = async (rows: RecordData[], rowKey: RowKey | undefined): Promise<void> => {
    await TestBed.configureTestingModule({
      imports: [AppGridComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(AppGridComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('rowData', rows);
    fixture.componentRef.setInput('columnDefs', Object.keys(rows[0]).map((id) => ({ id })));
    fixture.componentRef.setInput('selectionType', 'multiple');
    fixture.componentRef.setInput('enableRowExpansion', true);
    fixture.componentRef.setInput('rowKey', rowKey);
    fixture.detectChanges();
  };

  const selectedInDom = (): number => (fixture.nativeElement as HTMLElement).querySelectorAll('tr.impulse-row.selected').length;

  const cases: { name: string; rows: RecordData[]; rowKey: RowKey | undefined; pick: number; wantKey: unknown }[] = [
    {
      name: 'a compound key the caller joins',
      rows: [
        { squadronId: 'sq-1', pilotId: 'p-1' },
        { squadronId: 'sq-1', pilotId: 'p-2' },
      ],
      rowKey: (row): string => {
        return `${row['squadronId']}/${row['pilotId']}`;
      },
      pick: 0,
      wantKey: 'sq-1/p-1',
    },
    {
      name: 'a key not named id, with id alike on every row',
      rows: [
        { code: 'A', id: 'same' },
        { code: 'B', id: 'same' },
      ],
      rowKey: (row): unknown => {
        return row['code'];
      },
      pick: 1,
      wantKey: 'B',
    },
    {
      name: 'no key: the position, with every field alike',
      rows: [{ section: 'General' }, { section: 'General' }],
      rowKey: undefined,
      pick: 1,
      wantKey: 1,
    },
  ];

  for (const tt of cases) {
    describe(tt.name, () => {
      const other = 1 - tt.pick;

      beforeEach(async () => {
        await create(tt.rows, tt.rowKey);
      });

      it('identifies the row by it', () => {
        expect(component.keyOf(tt.rows[tt.pick])).toEqual(tt.wantKey);
        expect(component.keyOf(tt.rows[other])).not.toEqual(tt.wantKey);
      });

      it('selects the one row, and selecting all selects both', () => {
        const emitted: RecordData[][] = [];
        component.selectedRows.subscribe((rows) => emitted.push(rows));

        component.toggleRow(tt.rows[tt.pick]);
        fixture.detectChanges();
        expect(component.isSelected(tt.rows[tt.pick])).toBe(true);
        expect(component.isSelected(tt.rows[other])).toBe(false);
        expect(component.allSelected()).toBe(false);
        expect(component.someSelected()).toBe(true);
        expect(selectedInDom()).toBe(1);
        expect(emitted.at(-1)).toEqual([tt.rows[tt.pick]]);

        component.toggleSelectAll();
        fixture.detectChanges();
        expect(component.allSelected()).toBe(true);
        expect(selectedInDom()).toBe(2);
        expect(emitted.at(-1)?.length).toBe(2);
      });

      it('expands the one row', () => {
        component.toggleExpand(tt.rows[tt.pick]);
        expect(component.isExpanded(tt.rows[tt.pick])).toBe(true);
        expect(component.isExpanded(tt.rows[other])).toBe(false);

        component.toggleExpand(tt.rows[tt.pick]);
        expect(component.isExpanded(tt.rows[tt.pick])).toBe(false);
      });

      it('draws both rows', () => {
        expect((fixture.nativeElement as HTMLElement).querySelectorAll('tr.impulse-row').length).toBe(2);
      });
    });
  }

  // jsdom, where an application's specs run, has no ResizeObserver: the grid keeps the
  // one measurement of its container and draws the page instead of failing in its render hook.
  it('draws an icon column from the display row: the icon as an image named by the words, the words alone where the row has no icon', async () => {
    const rows: RecordData[] = [
      { id: 'p-1', limitStatus: 'Grandfathered: Jane Doe has 4 active learners. The partner limit is 2.', [iconCellKey('limitStatus')]: 'warning' },
      { id: 'p-2', limitStatus: 'ok', [iconCellKey('limitStatus')]: null },
    ];
    await TestBed.configureTestingModule({ imports: [AppGridComponent], providers: [provideResourceTesting()] }).compileComponents();
    fixture = TestBed.createComponent(AppGridComponent);
    fixture.componentRef.setInput('rowData', rows);
    fixture.componentRef.setInput('columnDefs', [
      { id: 'id' },
      { id: 'limitStatus', header: 'Limit', iconConfig: { icons: { grandfathered: 'warning' } }, tooltipPosition: 'right' },
    ]);
    fixture.detectChanges();
    const element = fixture.nativeElement as HTMLElement;
    const cells = Array.from(element.querySelectorAll<HTMLElement>('td.icon-col'));
    expect(cells).toHaveLength(2);
    const icon = cells[0].querySelector('mat-icon');
    expect(icon?.textContent?.trim()).toBe('warning');
    expect(icon?.getAttribute('aria-label')).toBe('Grandfathered: Jane Doe has 4 active learners. The partner limit is 2.');
    expect(icon?.getAttribute('role')).toBe('img');
    expect(icon?.getAttribute('tabindex')).toBe('0');
    expect(cells[1].querySelector('mat-icon')).toBeNull();
    expect(cells[1].textContent?.trim()).toBe('ok');
    expect(Array.from(element.querySelectorAll('th .col-header')).map((h) => h.textContent?.trim())).toEqual(['Id', 'Limit']);
  });

  it('draws the page under virtual scroll where the environment has no ResizeObserver', async () => {
    expect(typeof ResizeObserver).toBe('undefined');
    await create([{ section: 'General' }, { section: 'Flight' }], undefined);
    fixture.componentRef.setInput('enableVirtualScroll', true);
    fixture.detectChanges();
    expect((fixture.nativeElement as HTMLElement).querySelectorAll('tr.impulse-row').length).toBe(2);
  });
});

// The filter menu's value control follows the column's field: a date control on a date
// column, a date and a time of day on a timestamp column, typed text elsewhere and for a
// list, and the value travels as the server reads it, the whole day or minute.
describe('the filter menu\'s value control', () => {
  let fixture: ComponentFixture<AppGridComponent>;
  let emitted: ColumnFilter[] | undefined;

  const create = async (filterInputs: ColumnFilterInputs): Promise<void> => {
    await TestBed.configureTestingModule({
      imports: [AppGridComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(AppGridComponent);
    fixture.componentRef.setInput('rowData', [{ deadline: '2026-12-15T09:30:00Z', expiresOn: '2026-12-20', title: 'Survey' }]);
    fixture.componentRef.setInput('columnDefs', [{ id: 'deadline' }, { id: 'expiresOn' }, { id: 'title' }]);
    fixture.componentRef.setInput('filterability', { deadline: 'always', expiresOn: 'always', title: 'always' });
    fixture.componentRef.setInput('filterInputs', filterInputs);
    emitted = undefined;
    fixture.componentInstance.filterChange.subscribe((filters) => {
      emitted = filters;
    });
    fixture.detectChanges();
  };
  const settle = async (): Promise<void> => {
    fixture.detectChanges();
    await fixture.whenStable();
    await new Promise((resolve) => setTimeout(resolve, 0));
    fixture.detectChanges();
  };
  /** Opens the column's filter menu by its header button and hands back the menu's panel in the overlay. */
  const openMenu = async (column: number): Promise<HTMLElement> => {
    const triggers = (fixture.nativeElement as HTMLElement).querySelectorAll<HTMLButtonElement>('button.filter-trigger');
    triggers[column].click();
    await settle();
    const menu = document.body.querySelector<HTMLElement>('.cdk-overlay-container .impulse-grid-filter-menu');
    if (!menu) {
      throw new Error('no menu open');
    }
    return menu;
  };
  const controls = (menu: HTMLElement): string[] => Array.from(menu.querySelectorAll('input')).map((input) => input.type);
  const give = (input: HTMLInputElement | null, value: string): void => {
    if (!input) {
      throw new Error('no input');
    }
    input.value = value;
    input.dispatchEvent(new Event('input'));
    fixture.detectChanges();
  };
  const choose = (menu: HTMLElement, operator: string): void => {
    const select = menu.querySelector<HTMLSelectElement>('select.filter-operator');
    if (!select) {
      throw new Error('no operator select');
    }
    select.value = operator;
    select.dispatchEvent(new Event('change'));
    fixture.detectChanges();
  };
  const apply = async (menu: HTMLElement): Promise<void> => {
    const button = Array.from(menu.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Apply');
    if (!button) {
      throw new Error('no Apply button');
    }
    button.click();
    await settle();
  };
  /** Closes the open menu as a click beside it does. */
  const closeMenu = async (): Promise<void> => {
    const backdrop = document.body.querySelector<HTMLElement>('.cdk-overlay-backdrop');
    if (!backdrop) {
      throw new Error('no menu backdrop');
    }
    backdrop.click();
    await settle();
  };

  it('a timestamp column takes a day and a time of day, sent as the minute', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(0);
    expect(controls(menu)).toEqual(['date', 'time']);
    give(menu.querySelector('input[type=date]'), '2026-12-15');
    give(menu.querySelector('input[type=time]'), '09:30');
    await apply(menu);
    expect(emitted).toEqual([{ field: 'deadline', operator: 'eq', value: '2026-12-15T09:30' }]);
  });

  it('a timestamp column with the day alone sends the day', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(0);
    give(menu.querySelector('input[type=date]'), '2026-12-15');
    await apply(menu);
    expect(emitted).toEqual([{ field: 'deadline', operator: 'eq', value: '2026-12-15' }]);
  });

  it('a new operator waits for Apply, as a value does, and keeps the day the control shows', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(0);
    give(menu.querySelector('input[type=date]'), '2026-12-15');
    await apply(menu);
    fixture.componentRef.setInput('filters', emitted);
    emitted = undefined;
    choose(menu, 'gte');
    expect(menu.querySelector<HTMLInputElement>('input[type=date]')?.value).toBe('2026-12-15');
    expect(emitted).toBeUndefined();
    give(menu.querySelector('input[type=time]'), '12:00');
    await apply(menu);
    expect(emitted).toEqual([{ field: 'deadline', operator: 'gte', value: '2026-12-15T12:00' }]);
  });

  it('a null test chosen alone commits as the menu closes', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(2);
    choose(menu, 'isnull');
    expect(emitted).toBeUndefined();
    await closeMenu();
    expect(emitted).toEqual([{ field: 'title', operator: 'isnull', value: '' }]);
  });

  it('closing the menu on an unfinished draft keeps the committed filter', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(1);
    choose(menu, 'in');
    give(menu.querySelector('input'), '2026-12-01, 2027');
    await apply(menu);
    expect(emitted).toEqual([{ field: 'expiresOn', operator: 'in', value: '2026-12-01, 2027' }]);
    fixture.componentRef.setInput('filters', emitted);
    emitted = undefined;
    choose(menu, 'eq');
    expect(menu.querySelector<HTMLInputElement>('input[type=date]')?.value).toBe('');
    await closeMenu();
    expect(emitted).toBeUndefined();
  });

  it('a time of day without a day is nothing yet', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(0);
    give(menu.querySelector('input[type=time]'), '09:30');
    await apply(menu);
    expect(emitted).toBeUndefined();
  });

  it('a date column takes a day alone', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(1);
    expect(controls(menu)).toEqual(['date']);
    give(menu.querySelector('input[type=date]'), '2026-12-20');
    await apply(menu);
    expect(emitted).toEqual([{ field: 'expiresOn', operator: 'eq', value: '2026-12-20' }]);
  });

  it('a list on a date column is typed in the forms the server reads, and a return to one value starts over', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(1);
    choose(menu, 'in');
    expect(controls(menu)).toEqual(['text']);
    const text = menu.querySelector<HTMLInputElement>('input');
    expect(text?.placeholder).toBe('2026-12-15, 12/2026');
    give(text, '2026-12-01, 2027');
    choose(menu, 'eq');
    expect(controls(menu)).toEqual(['date']);
    expect(menu.querySelector<HTMLInputElement>('input[type=date]')?.value).toBe('');
    expect(emitted).toBeUndefined();
  });

  it('a column with no control keeps its text box', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(2);
    expect(controls(menu)).toEqual(['text']);
    expect(menu.querySelector<HTMLInputElement>('input')?.placeholder).toBe('Value');
  });
});
