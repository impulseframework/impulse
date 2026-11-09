import { HttpClient, provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ApplicationRef, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { of, Subject, throwError } from 'rxjs';
import {
  safeHttpResource,
  safeRxResource,
  staleHttpResource,
  staleRxResource,
  swrHttpResource,
  swrRxResource,
} from './safe-resource';
import { SwrCacheService } from './swr-cache.service';

describe('safe-resource', () => {
  let httpTestingController: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    httpTestingController = TestBed.inject(HttpTestingController);
  });

  afterEach(() => httpTestingController.verify());

  /**
   * Zoneless: change detection runs the resource's loader (the request goes out, or the
   * synchronous stream emits), the pending value settles, and change detection runs the
   * effects that follow it (the SWR cache write).
   */
  async function settle(): Promise<void> {
    TestBed.tick();
    await TestBed.inject(ApplicationRef).whenStable();
    TestBed.tick();
  }

  describe('safeHttpResource', () => {
    it('returns undefined before the HTTP response and the response value afterwards', async () => {
      const safeRef = TestBed.runInInjectionContext(() => safeHttpResource<string>(() => '/api/test'));

      expect(safeRef.safeValue()).toBeUndefined();

      safeRef.resource.reload();
      TestBed.tick();

      const request = httpTestingController.expectOne('/api/test');
      request.flush('ok');
      await settle();

      expect(safeRef.safeValue()).toBe('ok');
      expect(safeRef.resource.value()).toBe('ok');
    });

    it('returns undefined after the HTTP response returns an error', async () => {
      const safeRef = TestBed.runInInjectionContext(() => safeHttpResource<string>(() => '/api/test'));

      expect(safeRef.safeValue()).toBeUndefined();

      safeRef.resource.reload();
      TestBed.tick();

      const request = httpTestingController.expectOne('/api/test');
      request.flush('error', { status: 500, statusText: 'Server Error' });
      await settle();

      expect(safeRef.safeValue()).toBeUndefined();
    });
  });

  describe('safeRxResource', () => {
    it('returns undefined before stream emits and the latest value after emission', async () => {
      const stream$ = new Subject<string>();

      const safeRef = TestBed.runInInjectionContext(() =>
        safeRxResource<string, void>({
          stream: () => stream$.asObservable(),
        }),
      );

      expect(safeRef.safeValue()).toBeUndefined();

      safeRef.resource.reload();
      TestBed.tick();

      stream$.next('first');
      await settle();
      expect(safeRef.safeValue()).toBe('first');
      expect(safeRef.resource.value()).toBe('first');

      stream$.next('second');
      await settle();
      expect(safeRef.safeValue()).toBe('second');
    });

    it('returns undefined after the HTTP response returns an error', async () => {
      const http = TestBed.inject(HttpClient);

      const safeRef = TestBed.runInInjectionContext(() =>
        safeRxResource<string>({
          stream: () => http.get<string>('/api/test'),
        }),
      );

      expect(safeRef.safeValue()).toBeUndefined();

      safeRef.resource.reload();
      TestBed.tick();

      const request = httpTestingController.expectOne('/api/test');
      request.flush('error', { status: 500, statusText: 'Server Error' });
      await settle();

      expect(safeRef.safeValue()).toBeUndefined();
      expect(safeRef.resource.hasValue()).toBe(false);
    });
  });

  describe('staleHttpResource', () => {
    it('returns undefined before the first HTTP response and the response value afterwards', async () => {
      const staleRef = TestBed.runInInjectionContext(() => staleHttpResource<string>(() => '/api/test'));

      expect(staleRef.safeValue()).toBeUndefined();

      staleRef.resource.reload();
      TestBed.tick();

      httpTestingController.expectOne('/api/test').flush('ok');
      await settle();

      expect(staleRef.safeValue()).toBe('ok');
      expect(staleRef.resource.value()).toBe('ok');
    });

    it('preserves the previous value while reloading', async () => {
      const staleRef = TestBed.runInInjectionContext(() => staleHttpResource<string>(() => '/api/test'));

      staleRef.resource.reload();
      TestBed.tick();
      httpTestingController.expectOne('/api/test').flush('first');
      await settle();

      expect(staleRef.safeValue()).toBe('first');

      staleRef.resource.reload();
      TestBed.tick();

      // Still in loading state — previous value should be retained
      expect(staleRef.resource.isLoading()).toBe(true);
      expect(staleRef.safeValue()).toBe('first');

      httpTestingController.expectOne('/api/test').flush('second');
      await settle();

      expect(staleRef.safeValue()).toBe('second');
    });

    it('returns undefined while reloading if the previous state was an error', async () => {
      const staleRef = TestBed.runInInjectionContext(() => staleHttpResource<string>(() => '/api/test'));

      staleRef.resource.reload();
      TestBed.tick();
      httpTestingController.expectOne('/api/test').flush('error', { status: 500, statusText: 'Server Error' });
      await settle();

      expect(staleRef.safeValue()).toBeUndefined();

      staleRef.resource.reload();
      TestBed.tick();

      // Previous state was an error, so no value should be retained during loading
      expect(staleRef.resource.isLoading()).toBe(true);
      expect(staleRef.safeValue()).toBeUndefined();

      httpTestingController.expectOne('/api/test').flush('recovered');
      await settle();

      expect(staleRef.safeValue()).toBe('recovered');
    });

    it('returns the defaultValue before the first load and after an error', async () => {
      const staleRef = TestBed.runInInjectionContext(() =>
        staleHttpResource<string>(() => '/api/test', undefined, 'default'),
      );

      expect(staleRef.safeValue()).toBe('default');

      staleRef.resource.reload();
      TestBed.tick();
      httpTestingController.expectOne('/api/test').flush('error', { status: 500, statusText: 'Server Error' });
      await settle();

      expect(staleRef.safeValue()).toBe('default');
    });
  });

  describe('staleRxResource', () => {
    it('returns undefined before stream emits and the latest value after emission', async () => {
      const stream$ = new Subject<string>();

      const staleRef = TestBed.runInInjectionContext(() =>
        staleRxResource<string, void>({ stream: () => stream$.asObservable() }),
      );

      expect(staleRef.safeValue()).toBeUndefined();

      staleRef.resource.reload();
      TestBed.tick();

      stream$.next('first');
      await settle();

      expect(staleRef.safeValue()).toBe('first');
      expect(staleRef.resource.value()).toBe('first');
    });

    it('preserves the previous value while reloading', async () => {
      const stream$ = new Subject<string>();

      const staleRef = TestBed.runInInjectionContext(() =>
        staleRxResource<string, void>({ stream: () => stream$.asObservable() }),
      );

      staleRef.resource.reload();
      TestBed.tick();
      stream$.next('first');
      await settle();

      expect(staleRef.safeValue()).toBe('first');

      staleRef.resource.reload();
      TestBed.tick();

      // Still in loading state — previous value should be retained
      expect(staleRef.resource.isLoading()).toBe(true);
      expect(staleRef.safeValue()).toBe('first');

      stream$.next('second');
      await settle();

      expect(staleRef.safeValue()).toBe('second');
    });

    it('returns undefined while reloading if the previous state was an error', async () => {
      const http = TestBed.inject(HttpClient);

      const staleRef = TestBed.runInInjectionContext(() =>
        staleRxResource<string>({ stream: () => http.get<string>('/api/test') }),
      );

      staleRef.resource.reload();
      TestBed.tick();
      httpTestingController.expectOne('/api/test').flush('error', { status: 500, statusText: 'Server Error' });
      await settle();

      expect(staleRef.safeValue()).toBeUndefined();

      staleRef.resource.reload();
      TestBed.tick();

      // Previous state was an error, so no value should be retained during loading
      expect(staleRef.resource.isLoading()).toBe(true);
      expect(staleRef.safeValue()).toBeUndefined();

      httpTestingController.expectOne('/api/test').flush('recovered');
      await settle();

      expect(staleRef.safeValue()).toBe('recovered');
    });

    it('returns the defaultValue before the first load and after an error', async () => {
      const http = TestBed.inject(HttpClient);

      const staleRef = TestBed.runInInjectionContext(() =>
        staleRxResource<string>({ stream: () => http.get<string>('/api/test') }, 'default'),
      );

      expect(staleRef.safeValue()).toBe('default');

      staleRef.resource.reload();
      TestBed.tick();
      httpTestingController.expectOne('/api/test').flush('error', { status: 500, statusText: 'Server Error' });
      await settle();

      expect(staleRef.safeValue()).toBe('default');
    });
  });

  describe('swrHttpResource', () => {
    it('returns undefined before first HTTP response, then saves to cache', async () => {
      const safeRef = TestBed.runInInjectionContext(() => swrHttpResource<string>(() => '/api/test'));
      const swr = TestBed.inject(SwrCacheService);

      expect(safeRef.safeValue()).toBeUndefined();

      TestBed.tick();
      const request = httpTestingController.expectOne('/api/test');
      request.flush('first response');
      // processes response + effects
      await settle();

      expect(safeRef.safeValue()).toBe('first response');
      expect(safeRef.resource.value()).toBe('first response');
      expect(swr.get('/api/test')).toBe('first response');
    });

    it('returns defaultValue before first HTTP response', async () => {
      const safeRef = TestBed.runInInjectionContext(() =>
        swrHttpResource<string>(() => '/api/test', undefined, 'initial default'),
      );

      expect(safeRef.safeValue()).toBe('initial default');

      TestBed.tick();
      httpTestingController.expectOne('/api/test').flush('response');
      // processes response + effects
      await settle();

      expect(safeRef.safeValue()).toBe('response');
    });

    it('returns defaultValue when the HTTP response is an error', async () => {
      const safeRef = TestBed.runInInjectionContext(() =>
        swrHttpResource<string>(() => '/api/test', undefined, 'initial default'),
      );

      TestBed.tick();
      httpTestingController.expectOne('/api/test').flush('error', { status: 500, statusText: 'Server Error' });
      await settle();

      expect(safeRef.safeValue()).toBe('initial default');
    });

    it('returns undefined when the HTTP response is an error and no defaultValue', async () => {
      const safeRef = TestBed.runInInjectionContext(() => swrHttpResource<string>(() => '/api/test'));

      TestBed.tick();
      httpTestingController.expectOne('/api/test').flush('error', { status: 500, statusText: 'Server Error' });
      await settle();

      expect(safeRef.safeValue()).toBeUndefined();
    });

    it('returns stale cached value during refetch, then updates on fresh response', async () => {
      const safeRef = TestBed.runInInjectionContext(() => swrHttpResource<string>(() => '/api/test'));
      const swr = TestBed.inject(SwrCacheService);

      TestBed.tick();
      httpTestingController.expectOne('/api/test').flush('first response');
      // processes response + effects
      await settle();

      expect(safeRef.safeValue()).toBe('first response');
      expect(swr.get('/api/test')).toBe('first response');

      safeRef.resource.reload();
      TestBed.tick();

      // Stale value should be served while refetch is in flight
      expect(safeRef.safeValue()).toBe('first response');
      expect(swr.get('/api/test')).toBe('first response');

      // Fresh response arrives
      httpTestingController.expectOne('/api/test').flush('second response');
      // processes response + effects
      await settle();

      expect(safeRef.safeValue()).toBe('second response');
      expect(safeRef.resource.value()).toBe('second response');
      expect(swr.get('/api/test')).toBe('second response');
    });
  });

  describe('swrRxResource', () => {
    it('returns undefined before first response, then saves to cache', async () => {
      const params = signal<{ input: string } | undefined>(undefined);
      const safeRef = TestBed.runInInjectionContext(() =>
        swrRxResource<string, { input: string } | undefined>('test-key', {
          params: () => params(),
          stream: ({ params }) => of(params.input),
        }),
      );
      const swr = TestBed.inject(SwrCacheService);

      expect(safeRef.safeValue()).toBeUndefined();

      params.set({ input: 'first response' });
      await settle();

      expect(safeRef.safeValue()).toBe('first response');
      expect(safeRef.resource.value()).toBe('first response');
      expect(swr.get('test-key' + JSON.stringify({ input: 'first response' }))).toBe('first response');
    });

    it('returns defaultValue before first response', async () => {
      const params = signal<{ input: string } | undefined>(undefined);
      const safeRef = TestBed.runInInjectionContext(() =>
        swrRxResource<string, { input: string } | undefined>(
          'test-key',
          {
            params: () => params(),
            stream: ({ params }) => of(params.input),
          },
          'initial default',
        ),
      );

      expect(safeRef.safeValue()).toBe('initial default');

      params.set({ input: 'response' });
      await settle();

      expect(safeRef.safeValue()).toBe('response');
    });

    it('returns defaultValue when the stream errors', async () => {
      const params = signal<{ input: string } | undefined>(undefined);
      const safeRef = TestBed.runInInjectionContext(() =>
        swrRxResource<string, { input: string } | undefined>(
          'test-key',
          {
            params: () => params(),
            stream: () => throwError(() => new Error('something went wrong')),
          },
          'initial default',
        ),
      );

      params.set({ input: 'trigger' });
      await settle();

      expect(safeRef.safeValue()).toBe('initial default');
    });

    it('returns undefined when the stream errors and no defaultValue', async () => {
      const params = signal<{ input: string } | undefined>(undefined);
      const safeRef = TestBed.runInInjectionContext(() =>
        swrRxResource<string, { input: string } | undefined>('test-key', {
          params: () => params(),
          stream: () => throwError(() => new Error('something went wrong')),
        }),
      );

      params.set({ input: 'trigger' });
      await settle();

      expect(safeRef.safeValue()).toBeUndefined();
    });

    it('returns stale cached value during refetch, then updates on fresh response', async () => {
      const params = signal<{ input: string } | undefined>(undefined);
      const safeRef = TestBed.runInInjectionContext(() =>
        swrRxResource<string, { input: string } | undefined>('test-key', {
          params: () => params(),
          stream: ({ params }) => of(params.input),
        }),
      );
      const swr = TestBed.inject(SwrCacheService);

      params.set({ input: 'first response' });
      await settle();

      expect(safeRef.safeValue()).toBe('first response');
      expect(swr.get('test-key' + JSON.stringify({ input: 'first response' }))).toBe('first response');

      safeRef.resource.reload();
      TestBed.tick();

      // Stale value should be served while refetch is in flight
      expect(safeRef.safeValue()).toBe('first response');

      params.set({ input: 'second response' });
      await settle();

      expect(safeRef.safeValue()).toBe('second response');
      expect(safeRef.resource.value()).toBe('second response');
      expect(swr.get('test-key' + JSON.stringify({ input: 'second response' }))).toBe('second response');
    });

    it('appends JSON-stringified params to the cache key', async () => {
      const params = signal<{ userId: number } | undefined>(undefined);
      const safeRef = TestBed.runInInjectionContext(() =>
        swrRxResource<string, { userId: number } | undefined>('test-key', {
          params: () => params(),
          stream: ({ params }) => of(`user-${params.userId}-details`),
        }),
      );
      const swr = TestBed.inject(SwrCacheService);

      params.set({ userId: 67 });
      await settle();

      expect(safeRef.safeValue()).toBe('user-67-details');
      expect(swr.get('test-key')).toBeUndefined();
      expect(swr.get('test-key' + JSON.stringify({ userId: 67 }))).toBe('user-67-details');

      params.set({ userId: 47 });
      await settle();

      expect(safeRef.safeValue()).toBe('user-47-details');
      expect(swr.get('test-key')).toBeUndefined();
      expect(swr.get('test-key' + JSON.stringify({ userId: 47 }))).toBe('user-47-details');
    });
  });
});
