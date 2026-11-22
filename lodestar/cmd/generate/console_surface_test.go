package generate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestConsoleCarriesLibrarySurface pins the browser-library surface the console carries
// beyond its generated pages, which no other application exercises since the
// browser packages' showcase retired: the idle session provided from the build's environment through the
// library's tokens, its warning the library's notice that any activity ends, with no
// button of the console's (the retired confirmation flag is not provided); the notices'
// place, under the frame's bar and at the foot of the screen on a phone; the Squadrons
// page's componentConfig (the channel card) and arrayConfig (the sector's other
// squadrons, filtered by the indexed key); the Hangars page, whose passes
// list offers the three holder pickers, the sector's groups among them, and whose two
// lists write no words for their create's button and dialog, which the library takes from
// each resource's row word (every list's create opens in a dialog, with nothing for its
// config to ask); and the leave-page
// confirmation, which the library's resourceRoutes attaches to every config-driven list
// and row route under the FRONTEND_LOGIN_PATH the console provides, so the console adds
// no guard of its own. Lodestar has no browser specs and the walkthrough is curl, so the
// committed sources are the proof the wiring is present; the dialogs are driven by hand
// in the browser (README, "Running it"). Needs no emulator.
//
// Demonstrates: idle.configured, config.component, config.array, form.leave-page, list.create-dialog.
func TestConsoleCarriesLibrarySurface(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	console := filepath.Join(filepath.Dir(thisFile), "..", "..", "web", "console", "src")

	tests := []struct {
		name   string
		file   string
		want   []string
		absent []string
	}{
		{
			name: "the idle tokens are provided, the durations from the environment, and the retired confirmation flag is not",
			file: "app/app.config.ts",
			want: []string{
				"{ provide: IDLE_SESSION_DURATION, useValue: environment.idle.sessionSeconds }",
				"{ provide: IDLE_WARNING_DURATION, useValue: environment.idle.warningSeconds }",
				"{ provide: IDLE_KEEPALIVE_DURATION, useValue: environment.idle.keepAliveSeconds }",
				"provide: IDLE_LOGOUT_ACTION,",
				"{ provide: FRONTEND_LOGIN_PATH, useValue: '/login' }",
			},
			absent: []string{"IDLE_TIMEOUT_REQUIRE_CONFIRMATION"},
		},
		{
			name: "a development build shortens the idle session",
			file: "environments/environment.ts",
			want: []string{"idle: { sessionSeconds: 180, warningSeconds: 60, keepAliveSeconds: 30 }"},
		},
		{
			name: "the served build's idle session matches the server's default session timeout",
			file: "environments/environment.prod.ts",
			want: []string{"idle: { sessionSeconds: 600, warningSeconds: 60, keepAliveSeconds: 30 }"},
		},
		{
			name: "the console draws no bar of its own and so no stay-signed-in button: every signed-in page sits in the library's frame",
			file: "app/app.routes.ts",
			want: []string{"component: FrameComponent,", "canActivate: [LoginAuthenticationGuard],"},
		},
		{
			name: "the notices stack under the frame's bar, and at the foot of the screen on a phone, as the skeleton places them",
			file: "app/app.component.scss",
			want: []string{
				".alert-container {", "top: 56px;", "transform: translateX(-50%);",
				"@media (max-width: 599.98px) {", "top: auto;", "bottom: 16px;",
			},
			absent: []string{"top: 12px;"},
		},
		{
			name: "the Squadrons page carries the channel card and the sector's other squadrons",
			file: "app/configs/squadrons.config.ts",
			want: []string{
				"componentConfig({ primaryResource: Resources.Squadrons, component: SquadronChannelComponent })",
				"arrayConfig({",
				"title: 'Other squadrons in this sector'",
				"`${Squadrons.fieldName.id}:ne:${squadron.id}`",
			},
		},
		{
			name: "the channel card is the application's component over the library's base class",
			file: "app/components/sector/squadron-channel/squadron-channel.component.ts",
			want: []string{"export class SquadronChannelComponent extends CustomConfigComponent"},
		},
		{
			name: "the Hangars page lists a hangar's passes, offers a person, a squadron and a group as the holder, and names each create's dialog",
			file: "app/configs/hangars.config.ts",
			want: []string{
				"primaryResource: Resources.HangarPasses,",
				"parentRelation: { parentKey: Hangars.fieldName.id, childKey: HangarPasses.fieldName.hangarId },",
				"name: HangarPasses.fieldName.userId,",
				"listDisplay: [CrewSectorUsers.fieldName.name, CrewSectorUsers.fieldName.username],",
				"name: HangarPasses.fieldName.squadronId,",
				"listDisplay: [Squadrons.fieldName.name],",
				"name: HangarPasses.fieldName.groupId,",
				"listDisplay: [CrewSectorGroups.fieldName.name],",
			},
			// Neither list writes a label: each add button takes its words from the
			// resource's row word (TestConsoleNamesItsRowsInWords).
			absent: []string{"createButtonLabel"},
		},
		{
			name: "the config-driven routes come from resourceRoutes, which carries the leave-page guard, and the console adds none",
			file: "app/app.routes.ts",
			want: []string{
				"resourceRoutes(squadronsConfig, resourceMeta)",
				"resourceRoutes(missionsConfig, resourceMeta)",
				"resourceRoutes(hangarsConfig, resourceMeta)",
			},
			absent: []string{"canDeactivate: ["},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(filepath.Join(console, tt.file))
			if err != nil {
				t.Fatalf("reading %s: %v", tt.file, err)
			}
			source := string(data)
			for _, want := range tt.want {
				if !strings.Contains(source, want) {
					t.Errorf("%s: missing %q", tt.file, want)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(source, absent) {
					t.Errorf("%s: carries %q, want absent", tt.file, absent)
				}
			}
		})
	}
}

// TestConsoleNamesItsRowsInWords pins the word a list calls its row by, as the generated
// metadata carries it for the library's lists, which say it on the button that adds a
// row ("Add a hangar pass"), in the question before one is deleted ("Delete this hangar
// pass?") and in the message after ("Hangar pass deleted."). A resource that states no
// word goes by its name split into words; a view that declares its table goes by the
// table's word (the mission board's rows are missions, the squadron's roster's are
// squadron memberships); and a view that states its own keeps it (@rowWord): a pilot's
// assignments are a second view over the table of memberships, and its rows are
// assignments. Two lists keep the label they write, since adding is called something
// else there: a distress call is filed, and the roster gains a pilot while what a
// delete takes away is the membership, never the pilot. Needs no emulator.
//
// Demonstrates: @rowWord.
func TestConsoleNamesItsRowsInWords(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	console := filepath.Join(filepath.Dir(thisFile), "..", "..", "web", "console", "src", "app")

	data, err := os.ReadFile(filepath.Join(console, "core", "service", "zz_gen_resources.ts"))
	if err != nil {
		t.Fatalf("reading the generated metadata: %v", err)
	}
	metadata := string(data)

	words := []struct {
		name     string
		resource string
		want     string
	}{
		{name: "a resource that states no word: its name in words", resource: "HangarPasses", want: "rowWords: { one: 'hangar pass', many: 'hangar passes' },"},
		{name: "a name of one word", resource: "Hangars", want: "rowWords: { one: 'hangar', many: 'hangars' },"},
		{name: "a view that declares its table: the table's word", resource: "MissionBoards", want: "rowWords: { one: 'mission', many: 'missions' },"},
		{name: "the table two views show", resource: "SquadronMemberships", want: "rowWords: { one: 'squadron membership', many: 'squadron memberships' },"},
		{name: "one of them states no word, and takes the table's", resource: "SquadronRosters", want: "rowWords: { one: 'squadron membership', many: 'squadron memberships' },"},
		{name: "the other states its own", resource: "PilotAssignments", want: "rowWords: { one: 'assignment', many: 'assignments' },"},
		{name: "a view over no declared table states the word its rows go by", resource: "ClientRosters", want: "rowWords: { one: 'client', many: 'clients' },"},
		{name: "a resource the generated management declares", resource: "CrewSectorUsers", want: "rowWords: { one: 'person', many: 'people' },"},
	}
	for _, tt := range words {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, block, found := strings.Cut(metadata, "\n  [Resources."+tt.resource+"]: {\n")
			if !found {
				t.Fatalf("the generated metadata holds no %s", tt.resource)
			}
			head, _, _ := strings.Cut(block, "\n    fields: [")
			if !strings.Contains(head, tt.want) {
				t.Errorf("%s: want %q, got:\n%s", tt.resource, tt.want, head)
			}
		})
	}

	t.Run("a list writes a label only where adding is called something else", func(t *testing.T) {
		t.Parallel()

		configs, err := filepath.Glob(filepath.Join(console, "configs", "*.config.ts"))
		if err != nil || len(configs) == 0 {
			t.Fatalf("listing the console's configs: %v (%d found)", err, len(configs))
		}
		var labels []string
		for _, config := range configs {
			source, err := os.ReadFile(config)
			if err != nil {
				t.Fatalf("reading %s: %v", config, err)
			}
			for _, line := range strings.Split(string(source), "\n") {
				if strings.Contains(line, "createButtonLabel:") {
					labels = append(labels, filepath.Base(config)+": "+strings.TrimSpace(line))
				}
			}
		}
		want := []string{
			"distressCalls.config.ts: createButtonLabel: 'File a distress call',",
			"distressCalls.config.ts: createButtonLabel: 'Relay a message',",
			"squadrons.config.ts: createButtonLabel: 'Add a pilot',",
		}
		if strings.Join(labels, "\n") != strings.Join(want, "\n") {
			t.Errorf("labels the configs write = %q, want %q", labels, want)
		}
	})
}

// TestConsoleHandsTheClientItsHooks pins how both browser applications render the
// client's judgment, where the library's HTTP interceptor used to judge responses one
// layer below the client and toast every in-place refusal a second time: each
// app.config.ts spreads the options provideResourceClient hands its factory (the
// transport that counts activity, the error hook that returns the browser to the login
// page on a 401) into the generated createApi and registers no interceptor, HttpClient
// keeps the XSRF echo and the release header alone (apiVersionInterceptor, the library's
// one functional interceptor, which judges nothing), no provider after it re-provides
// ErrorHandler (the BrowserAnimationsModule import that did is the standalone
// provideAnimationsAsync), each login page composes the library's login component, which
// reads AuthService.redirectUrl once and clears it, names no path of its own and posts
// nothing through HttpClient, and the flight deck reports its refusals in place (the
// declared 409 answer, the 403 on an edit) so they reach no notice. The committed
// sources are the proof the wiring is present; the redirect, the notice and the login
// pages are driven in the browser (README, "Running it"). Needs no emulator.
//
// Demonstrates: client.login-redirect, client.uncaught-notice, client.login-component.
func TestConsoleHandsTheClientItsHooks(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	web := filepath.Join(filepath.Dir(thisFile), "..", "..", "web")

	// No interceptor, and no importProvidersFrom(BrowserAnimationsModule): BrowserModule's providers
	// carry Angular's default ErrorHandler, which would replace the adapter's and silence the notice.
	noInterceptor := []string{"HTTP_INTERCEPTORS", "ui-interceptor", "withInterceptorsFromDi", "ApiInterceptor", "BrowserAnimationsModule", "importProvidersFrom"}

	tests := []struct {
		name   string
		file   string
		want   []string
		absent []string
	}{
		{
			name: "the console spreads the adapter's options into createApi and registers the release-header interceptor alone",
			file: "console/src/app/app.config.ts",
			want: []string{
				"provideResourceClient((options) => createApi({ baseUrl: environment.apiUrl, ...options }))",
				"provideHttpClient(withInterceptors([apiVersionInterceptor]), withXsrfConfiguration({ cookieName: 'crew-xsrf' }))",
				"{ provide: API_VERSION, useValue: APP_VERSION }",
				"provideAnimationsAsync(),",
				"{ provide: FRONTEND_LOGIN_PATH, useValue: '/login' }",
				"{ provide: BASE_URL, useValue: environment.baseUrl }",
			},
			absent: noInterceptor,
		},
		{
			name: "the portal the same, over its own cookie",
			file: "portal/src/app/app.config.ts",
			want: []string{
				"provideResourceClient((options) => createApi({ baseUrl: environment.apiUrl, ...options }))",
				"provideHttpClient(withInterceptors([apiVersionInterceptor]), withXsrfConfiguration({ cookieName: 'members-xsrf' }))",
				"{ provide: API_VERSION, useValue: APP_VERSION }",
				"provideAnimationsAsync(),",
			},
			absent: noInterceptor,
		},
		{
			name: "the console's login page composes the library's login component and names no path of its own",
			file: "console/src/app/components/login/login.component.ts",
			want: []string{
				"import { LoginMethodsComponent } from '@impulseframework/resource-angular/auth-login-methods';",
				"import { ReleaseComponent } from '@impulseframework/resource-angular/ui-release';",
				"imports: [LoginMethodsComponent, MatCardModule, MatIconModule, ReleaseComponent],",
				"this.idle.stop();",
				"this.idle.start();",
				"void this.router.navigateByUrl(page);",
			},
			absent: []string{"HttpClient", "/user/", "user/methods", "redirectUrl", "location.assign", "logout()"},
		},
		{
			name: "the console's page hands the component where a sign-in lands and takes the page it emits",
			file: "console/src/app/components/login/login.component.html",
			want: []string{
				`<impulse-login-methods #methods home="/" (signedIn)="go($event)" />`,
				`[class.arriving]="methods.arriving()"`,
				`<mat-card-footer class="release"><impulse-release /></mat-card-footer>`,
			},
			absent: []string{"<form", "ngSubmit", "/user/", "returnUrl"},
		},
		{
			name: "the portal's login page the same, with its own words for the directory",
			file: "portal/src/app/components/login/login.component.ts",
			want: []string{
				"import { LoginMethodsComponent } from '@impulseframework/resource-angular/auth-login-methods';",
				"export const METHOD_LABELS: Readonly<Record<string, string>> = { directory: 'your company directory' };",
				"this.idle.stop();",
				"void this.router.navigateByUrl(page);",
			},
			absent: []string{"HttpClient", "/user/", "user/methods", "redirectUrl", "location.assign", "logout()"},
		},
		{
			name: "the portal's page hands the component the root, where a sign-in lands, and its labels",
			file: "portal/src/app/components/login/login.component.html",
			want: []string{
				`<impulse-login-methods #methods home="/" [labels]="labels" (signedIn)="go($event)" />`,
				`[class.arriving]="methods.arriving()"`,
				`<mat-card-footer class="release"><impulse-release /></mat-card-footer>`,
			},
			absent: []string{"<form", "/user/", "returnUrl"},
		},
		{
			name: "the console signs out through a route composing the library's sign-out page",
			file: "console/src/app/app.routes.ts",
			want: []string{
				"path: 'logout',",
				"import('@impulseframework/resource-angular/auth-sign-out').then((comp) => comp.SignOutComponent)",
			},
		},
		{
			name:   "the frame's Sign out lands on that route: the console names no other sign-out path",
			file:   "console/src/app/app.config.ts",
			want:   []string{"provideFrame({"},
			absent: []string{"FRONTEND_SIGN_OUT_PATH"},
		},
		{
			name: "the flight deck reports the declared answer and the edit refusal in place",
			file: "console/src/app/components/sector/flight-deck/flight-deck.component.ts",
			want: []string{
				"if (answer.status === 409) {",
				"this.refusal.set(",
				"this.editRefusal.set(serverWords(e));",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(filepath.Join(web, tt.file))
			if err != nil {
				t.Fatalf("reading %s: %v", tt.file, err)
			}
			source := string(data)
			for _, want := range tt.want {
				if !strings.Contains(source, want) {
					t.Errorf("%s: missing %q", tt.file, want)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(source, absent) {
					t.Errorf("%s: carries %q, want absent", tt.file, absent)
				}
			}
		})
	}
}

// TestThemeChoiceSurface pins how the console and the portal offer the person's choice of
// look: theme.scss hands its palettes to the library's palettes mixin under quoted ids,
// app.config.ts names the same ids to provideTheme (the portal, with one palette, names
// none), and the library's frame, which every signed-in page of both sits in, offers the
// choice under Appearance in the person's menu: light or dark, and the colors where the
// application names more than one palette; nothing of the choice is stored on the server.
//
// Demonstrates: webapp.theme-choice.
func TestThemeChoiceSurface(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	web := filepath.Join(filepath.Dir(thisFile), "..", "..", "web")

	tests := []struct {
		name string
		file string
		want []string
	}{
		{
			name: "the console's theme offers three palettes through the mixin",
			file: "console/src/theme.scss",
			want: []string{
				"@use '@impulseframework/resource-angular/palettes';",
				"@use './theme-colors' as brand;",
				"@include theme.palettes(",
				"'brand': (",
				"primary: brand.$primary-palette,",
				"tertiary: brand.$tertiary-palette,",
				"'workspace': (",
				"primary: palettes.$workspace-primary,",
				"tertiary: palettes.$workspace-tertiary,",
				"'violet': (",
				"primary: mat.$violet-palette,",
				"tertiary: mat.$magenta-palette,",
			},
		},
		{
			name: "the console names the same ids to provideTheme",
			file: "console/src/app/app.config.ts",
			want: []string{
				"import { provideTheme } from '@impulseframework/resource-angular/ui-theme';",
				"{ id: 'brand', label: 'Lodestar' },",
				"{ id: 'workspace', label: 'Workspace' },",
				"{ id: 'violet', label: 'Violet' },",
			},
		},
		{
			name: "the console's signed-in pages sit in the frame, whose person's menu offers the choice",
			file: "console/src/app/app.routes.ts",
			want: []string{"component: FrameComponent,"},
		},
		{
			name: "the portal offers one palette and names none to provideTheme",
			file: "portal/src/theme.scss",
			want: []string{"@use './theme-colors' as brand;", "@include theme.palettes(", "'brand': (", "primary: brand.$primary-palette,", "tertiary: brand.$tertiary-palette,"},
		},
		{
			name: "the portal starts the service with no palette",
			file: "portal/src/app/app.config.ts",
			want: []string{"import { provideTheme } from '@impulseframework/resource-angular/ui-theme';", "provideTheme(),"},
		},
		{
			name: "the portal's the same",
			file: "portal/src/app/app.routes.ts",
			want: []string{"component: FrameComponent,"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(filepath.Join(web, tt.file))
			if err != nil {
				t.Fatalf("reading %s: %v", tt.file, err)
			}
			source := string(data)
			for _, want := range tt.want {
				if !strings.Contains(source, want) {
					t.Errorf("%s: missing %q", tt.file, want)
				}
			}
		})
	}
}
