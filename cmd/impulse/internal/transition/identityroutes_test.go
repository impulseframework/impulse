package transition

import "testing"

func TestDropIdentityRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "the base's routes",
			text: `import { Routes } from '@angular/router';
import { apiDescriptor } from '@app/service/zz_gen_api';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { identityRoutes } from '@impulseframework/resource-angular/identity-pages';
import { UiComponent } from '@components/ui/ui.component';

export const routes: Routes = [
  {
    path: '',
    component: UiComponent,
    children: [
      {
        path: 'dashboard',
        loadComponent: () => import('./dashboard.component').then((comp) => comp.DashboardComponent),
      },
      // The staff's accounts: the accounts with a person's row page, the mailed links,
      // the sessions and the failed sign-ins.
      ...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'global' }),
      { path: '**', redirectTo: 'dashboard' },
    ],
  },
];
`,
			want: `import { Routes } from '@angular/router';
import { UiComponent } from '@components/ui/ui.component';

export const routes: Routes = [
  {
    path: '',
    component: UiComponent,
    children: [
      {
        path: 'dashboard',
        loadComponent: () => import('./dashboard.component').then((comp) => comp.DashboardComponent),
      },
      { path: '**', redirectTo: 'dashboard' },
    ],
  },
];
`,
		},
		{
			name: "a tenant's pages, the helper they alone used, and names other routes still use",
			text: `import { Route, Routes } from '@angular/router';
import { apiDescriptor } from '@app/service/zz_gen_api';
import { DomainRoutePath, resourceMeta } from '@app/service/zz_gen_resources';
import { identityRoutes } from '@impulseframework/resource-angular/identity-pages';

const dashboardRoute = (): Route => ({ path: 'dashboard' });

// A page the library builds for a tenant names its whole route.
const underTenant = (page: Route): Route => ({ ...page, path: page.path?.slice(DomainRoutePath.length + 1) });

export const routes: Routes = [
  dashboardRoute(),
  ...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'global' }),
  {
    path: DomainRoutePath,
    children: [
      dashboardRoute(),
      // The tenant's people and its invitations.
      ...identityRoutes(apiDescriptor, resourceMeta, {
        auth: 'Staff',
        scope: 'domain',
      }).map(underTenant),
      { path: '**', redirectTo: 'dashboard' },
    ],
  },
];
`,
			want: `import { Route, Routes } from '@angular/router';
import { DomainRoutePath } from '@app/service/zz_gen_resources';

const dashboardRoute = (): Route => ({ path: 'dashboard' });

export const routes: Routes = [
  dashboardRoute(),
  {
    path: DomainRoutePath,
    children: [
      dashboardRoute(),
      { path: '**', redirectTo: 'dashboard' },
    ],
  },
];
`,
		},
		{
			name: "a name another route still uses stays, in an import over several lines too",
			text: `import { apiDescriptor } from '@app/service/zz_gen_api';
import {
  identityRoutes,
  otherRoutes,
} from '@impulseframework/resource-angular/identity-pages';

export const routes = [
  ...identityRoutes(apiDescriptor, meta, { auth: 'Staff', scope: 'global' }),
  ...otherRoutes(apiDescriptor),
];
`,
			want: `import { apiDescriptor } from '@app/service/zz_gen_api';
import { otherRoutes } from '@impulseframework/resource-angular/identity-pages';

export const routes = [
  ...otherRoutes(apiDescriptor),
];
`,
		},
		{
			name: "the base's menu file: the spread and the names it alone used go, the group and a name another entry uses stay",
			text: `import { apiDescriptor } from '@app/service/zz_gen_api';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { Menu } from '@impulseframework/resource-angular/auth-frame';
import { featureFlagsPage } from '@impulseframework/resource-angular/feature-flags';
import { identityMenuPages } from '@impulseframework/resource-angular/identity-pages';

export function appMenus(): Menu[] {
  return [
    {
      label: 'System',
      groups: [
        {
          entries: [
            // The staff's accounts, their mailed links, sessions and failed sign-ins.
            ...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'global' }),
          ],
        },
        {
          entries: [
            // The feature flags the application declares: the library's page.
            featureFlagsPage(resourceMeta),
          ],
        },
      ],
    },
  ];
}
`,
			// The metadata stays: the Feature flags page reads it too.
			want: `import { resourceMeta } from '@app/service/zz_gen_resources';
import { Menu } from '@impulseframework/resource-angular/auth-frame';
import { featureFlagsPage } from '@impulseframework/resource-angular/feature-flags';

export function appMenus(): Menu[] {
  return [
    {
      label: 'System',
      groups: [
        {
          entries: [
          ],
        },
        {
          entries: [
            // The feature flags the application declares: the library's page.
            featureFlagsPage(resourceMeta),
          ],
        },
      ],
    },
  ];
}
`,
		},
		{
			name: "a spread that shares its line with other code is left for a person",
			text: `import { identityRoutes } from '@impulseframework/resource-angular/identity-pages';

export const routes = [...identityRoutes(api, meta, { auth: 'Staff', scope: 'global' }), other(api)];
`,
			want: `import { identityRoutes } from '@impulseframework/resource-angular/identity-pages';

export const routes = [...identityRoutes(api, meta, { auth: 'Staff', scope: 'global' }), other(api)];
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, _ := dropIdentityRoutes(tt.text)
			if got != tt.want {
				t.Errorf("dropIdentityRoutes() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
	if _, found := dropIdentityRoutes("export const routes = [];\n"); found {
		t.Error("dropIdentityRoutes() found a spread in routes that have none")
	}
}
