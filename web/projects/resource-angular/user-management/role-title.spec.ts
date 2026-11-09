import { roleTitle, RoleTitlePipe } from './role-title';

// A role's title is its name split at the capitals: what a person reads where the server
// knows the role by its name.

describe('roleTitle', () => {
  const cases: { name: string; want: string }[] = [
    { name: 'FeatureAdministrator', want: 'Feature Administrator' },
    { name: 'SectorCrew', want: 'Sector Crew' },
    { name: 'Headquarters', want: 'Headquarters' },
    { name: 'HTTPAdmin', want: 'HTTP Admin' },
    { name: 'Level2Admin', want: 'Level2 Admin' },
    { name: 'harbormaster', want: 'Harbormaster' },
    { name: 'sector_crew', want: 'Sector crew' },
    { name: 'sector-crew-lead', want: 'Sector crew lead' },
    { name: '', want: '' },
  ];
  for (const tt of cases) {
    it(`${JSON.stringify(tt.name)} reads ${JSON.stringify(tt.want)}`, () => {
      expect(roleTitle(tt.name)).toBe(tt.want);
    });
  }

  it('the pipe gives the same words, and nothing for no name', () => {
    const pipe = new RoleTitlePipe();
    expect(pipe.transform('FeatureAdministrator')).toBe('Feature Administrator');
    expect(pipe.transform(null)).toBe('');
    expect(pipe.transform(undefined)).toBe('');
  });
});
