import { FieldName } from '@impulseframework/resource-angular/types';
import { Resource } from '@impulseframework/resource';

import { listViewConfig, singleColumnConfig, viewConfig } from './configs';

// A column is a field column, a button column or an icon column, and the three are
// exclusive by type: a config naming both a button and an icon does not compile, which
// the directive below proves at build time.

describe('singleColumnConfig', () => {
  it('builds a field column, a button column and an icon column', () => {
    expect(singleColumnConfig({ id: 'name' as FieldName }).buttonConfig).toBeUndefined();
    expect(singleColumnConfig({ id: 'view' as FieldName, buttonConfig: { label: 'View', icon: 'arrow_forward' } }).buttonConfig?.icon).toBe(
      'arrow_forward',
    );
    expect(singleColumnConfig({ id: 'status' as FieldName, iconConfig: { icons: { over: 'warning' } } }).iconConfig?.icons['over']).toBe(
      'warning',
    );
  });

  it('refuses a column that is both a button and an icon, at compile time', () => {
    const both = {
      id: 'status' as FieldName,
      buttonConfig: { label: 'View', icon: 'arrow_forward' },
      iconConfig: { icons: { over: 'warning' } },
    };
    // @ts-expect-error a column is a button or an icon, never both
    expect(() => singleColumnConfig(both)).not.toThrow();
  });
});

// A page's live option is its own word, resolved against the resource's metadata by the
// store: the builders leave it unset when the options say nothing, so the metadata's
// default applies, and carry it as written otherwise (`live: false` is the opt-out).
describe('the live option on a page config', () => {
  const ships = 'Ships' as Resource;
  const list = { title: 'Ships', primaryResource: ships, listColumns: [], elements: [] };
  const view = { title: 'Ship', primaryResource: ships, elements: [] };

  it('is left unset by listViewConfig and viewConfig when the options say nothing', () => {
    expect(listViewConfig({ ...list }).live).toBeUndefined();
    expect(viewConfig({ ...view }).live).toBeUndefined();
  });

  it('is carried as written', () => {
    expect(listViewConfig({ ...list, live: false }).live).toBe(false);
    expect(listViewConfig({ ...list, live: true }).live).toBe(true);
    expect(viewConfig({ ...view, live: false }).live).toBe(false);
    expect(viewConfig({ ...view, live: true }).live).toBe(true);
  });
});
