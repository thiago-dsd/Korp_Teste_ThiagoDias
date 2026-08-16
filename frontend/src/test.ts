// Global setup for unit tests.
//
// The Angular unit-test builder initializes the TestBed environment itself, so
// this file must not call initTestEnvironment: doing it twice fails with
// NG0400.
//
// Components across the app rely on a few application-wide providers (SVG icon
// registry, HTTP client, router). Registering them here keeps every spec free
// of boilerplate; TestBed merges this configuration with the one each spec
// declares.
import { importProvidersFrom } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { provideRouter } from '@angular/router';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { beforeEach } from 'vitest';

// The test DOM runs on an opaque origin, where Web Storage is not available.
// Components and services that persist preferences expect it to exist, so the
// suite provides an in-memory implementation.
if (typeof globalThis.localStorage === 'undefined') {
  const store = new Map<string, string>();
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => void store.set(key, String(value)),
      removeItem: (key: string) => void store.delete(key),
      clear: () => store.clear(),
      key: (index: number) => [...store.keys()][index] ?? null,
      get length() {
        return store.size;
      },
    },
  });
}

// The test DOM does not implement media queries, which responsive components
// read on creation.
if (typeof globalThis.matchMedia !== 'function') {
  Object.defineProperty(globalThis, 'matchMedia', {
    configurable: true,
    value: (query: string): MediaQueryList =>
      ({
        matches: false,
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }) as unknown as MediaQueryList,
  });
}

beforeEach(() => {
  TestBed.configureTestingModule({
    providers: [
      importProvidersFrom(AngularSvgIconModule.forRoot()),
      provideHttpClient(),
      provideHttpClientTesting(),
      provideRouter([]),
      // Animations are not exercised in unit tests, but components declaring
      // synthetic properties fail without an animations provider.
      provideNoopAnimations(),
    ],
  });
});
