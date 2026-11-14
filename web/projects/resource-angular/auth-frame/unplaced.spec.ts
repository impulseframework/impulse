import { Routes } from '@angular/router';
import { here, Menu, page, pageRoute } from './menu';
import { unplacedPages } from './unplaced';

// The check an application ships: a page the routes declare with a label for the menus
// and no place in the menu file is reported, by itself; one placed twice is not.

const users = page({ label: 'Users', icon: 'group', route: ['tenants/:tenantID', 'users'] });
const signIn = page({ label: 'Sign-in settings', icon: 'login', route: ['tenants/:tenantID', 'sign-in'] });
const guides = page({ label: 'Guides', icon: 'menu_book', route: [here, 'guides'] });
const flags = page({ label: 'Feature flags', icon: 'flag', route: ['flags'] });

const routes: Routes = [
  { path: '', children: [pageRoute(flags, { path: 'flags' }), pageRoute(guides, { path: 'guides' })] },
  {
    path: 'tenants/:tenantID',
    children: [
      pageRoute(users, { path: 'users' }),
      pageRoute(signIn, { path: 'sign-in' }),
      // A route that carries no page is no menu's: the refusal page, an approval page.
      { path: 'refused' },
      pageRoute(guides, { path: 'guides' }),
    ],
  },
];

describe('unplacedPages', () => {
  it('reports each page the routes declare and the menu file never places, once, in route order', () => {
    const menus: Menu[] = [{ label: 'People', groups: [{ entries: [users] }] }];
    expect(unplacedPages(routes, menus).map((p) => p.label)).toEqual(['Feature flags', 'Guides', 'Sign-in settings']);
  });

  it('finds a page placed in a submenu', () => {
    const menus: Menu[] = [
      {
        label: 'People',
        groups: [{ entries: [users, { label: 'More', groups: [{ entries: [signIn, guides, flags] }] }] }],
      },
    ];
    expect(unplacedPages(routes, menus)).toEqual([]);
  });

  it('passes a page placed in two menus', () => {
    const menus: Menu[] = [
      { label: 'People', groups: [{ entries: [users, signIn] }] },
      { label: 'System', groups: [{ entries: [flags, users] }] },
      { label: 'Help', groups: [{ entries: [guides] }] },
    ];
    expect(unplacedPages(routes, menus)).toEqual([]);
  });

  it('keeps the route data a page route already had', () => {
    expect(pageRoute(users, { path: 'users', data: { title: 'Users' } }).data).toEqual({
      title: 'Users',
      impulsePage: users,
    });
  });
});
