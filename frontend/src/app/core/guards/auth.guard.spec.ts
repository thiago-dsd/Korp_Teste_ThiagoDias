import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { ActivatedRouteSnapshot, Router, RouterStateSnapshot, UrlTree, provideRouter } from '@angular/router';
import { Observable, firstValueFrom, isObservable } from 'rxjs';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { environment } from 'src/environments/environment';
import { apiErrorInterceptor } from '../interceptor/api-error.interceptor';
import { AuthService } from '../services/auth.service';
import { authGuard, guestGuard } from './auth.guard';

const authUrl = `${environment.identityApiUrl}/auth`;

function sessionPayload() {
  return {
    user: { id: 'u-1', email: 'ada@example.com', name: 'Ada Lovelace', created_at: '' },
    access_token: 'access-1',
    refresh_token: 'refresh-1',
    token_type: 'Bearer',
    expires_in: 900,
  };
}

/** Runs a guard the way the router does. */
function runGuard(guard: typeof authGuard, url = '/products'): boolean | UrlTree | Observable<boolean | UrlTree> {
  const route = {} as ActivatedRouteSnapshot;
  const state = { url } as RouterStateSnapshot;
  return TestBed.runInInjectionContext(() => guard(route, state)) as boolean | UrlTree | Observable<boolean | UrlTree>;
}

/** Waits for a guard that answers asynchronously. */
function settle(result: boolean | UrlTree | Observable<boolean | UrlTree>): Promise<boolean | UrlTree> {
  if (!isObservable(result)) {
    throw new Error('the guard answered synchronously');
  }
  return firstValueFrom(result);
}

describe('authGuard', () => {
  let http: HttpTestingController;
  let auth: AuthService;

  beforeEach(() => {
    localStorage.clear();
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(withInterceptors([apiErrorInterceptor])),
        provideHttpClientTesting(),
        provideRouter([]),
      ],
    });
    http = TestBed.inject(HttpTestingController);
    auth = TestBed.inject(AuthService);
  });

  afterEach(() => {
    http.verify();
    localStorage.clear();
  });

  it('should let a signed in person through', () => {
    auth.login('ada@example.com', 'correct horse battery staple').subscribe();
    http.expectOne(`${authUrl}/login`).flush(sessionPayload());

    expect(runGuard(authGuard)).toBe(true);
  });

  it('should send a stranger to the sign in screen keeping where they were going', () => {
    const result = runGuard(authGuard, '/invoices/new') as UrlTree;

    expect(result).toBeInstanceOf(UrlTree);
    expect(TestBed.inject(Router).serializeUrl(result)).toBe('/auth/sign-in?returnUrl=%2Finvoices%2Fnew');
  });

  it('should restore a stored session before turning anyone away', async () => {
    localStorage.setItem('invoice-system.refresh-token', 'refresh-1');

    const settled = settle(runGuard(authGuard));
    http.expectOne(`${authUrl}/refresh`).flush(sessionPayload());

    expect(await settled).toBe(true);
  });

  it('should turn someone away when the stored session is no longer valid', async () => {
    localStorage.setItem('invoice-system.refresh-token', 'refresh-1');

    const settled = settle(runGuard(authGuard));
    http
      .expectOne(`${authUrl}/refresh`)
      .flush(
        { error: { code: 'invalid_refresh_token', message: 'Session expired.' } },
        { status: 401, statusText: 'Unauthorized' },
      );

    expect(await settled).toBeInstanceOf(UrlTree);
  });
});

describe('guestGuard', () => {
  let http: HttpTestingController;
  let auth: AuthService;

  beforeEach(() => {
    localStorage.clear();
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(withInterceptors([apiErrorInterceptor])),
        provideHttpClientTesting(),
        provideRouter([]),
      ],
    });
    http = TestBed.inject(HttpTestingController);
    auth = TestBed.inject(AuthService);
  });

  afterEach(() => {
    http.verify();
    localStorage.clear();
  });

  it('should let a visitor reach the sign in screen', () => {
    expect(runGuard(guestGuard, '/auth/sign-in')).toBe(true);
  });

  it('should send a signed in person to the application', () => {
    auth.login('ada@example.com', 'correct horse battery staple').subscribe();
    http.expectOne(`${authUrl}/login`).flush(sessionPayload());

    const result = runGuard(guestGuard, '/auth/sign-in') as UrlTree;
    expect(TestBed.inject(Router).serializeUrl(result)).toBe('/home');
  });
});
