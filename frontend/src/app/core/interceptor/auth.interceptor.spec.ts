import { HttpClient, provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { environment } from 'src/environments/environment';
import { AuthService } from '../services/auth.service';
import { apiErrorInterceptor } from './api-error.interceptor';
import { authInterceptor } from './auth.interceptor';

const productsUrl = `${environment.stockApiUrl}/products`;
const authUrl = `${environment.identityApiUrl}/auth`;

function sessionPayload(accessToken: string, refreshToken: string) {
  return {
    user: { id: 'u-1', email: 'ada@example.com', name: 'Ada Lovelace', created_at: '' },
    access_token: accessToken,
    refresh_token: refreshToken,
    token_type: 'Bearer',
    expires_in: 900,
  };
}

describe('authInterceptor', () => {
  let http: HttpTestingController;
  let client: HttpClient;
  let auth: AuthService;

  function signIn(): void {
    auth.login('ada@example.com', 'correct horse battery staple').subscribe();
    http.expectOne(`${authUrl}/login`).flush(sessionPayload('access-1', 'refresh-1'));
  }

  beforeEach(() => {
    localStorage.clear();
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(withInterceptors([authInterceptor, apiErrorInterceptor])),
        provideHttpClientTesting(),
      ],
    });
    http = TestBed.inject(HttpTestingController);
    client = TestBed.inject(HttpClient);
    auth = TestBed.inject(AuthService);
  });

  afterEach(() => {
    http.verify();
    localStorage.clear();
  });

  it('should send the access token on calls to the services', () => {
    signIn();

    client.get(productsUrl).subscribe();

    const request = http.expectOne(productsUrl);
    expect(request.request.headers.get('Authorization')).toBe('Bearer access-1');
    request.flush({ items: [] });
  });

  it('should not send a token to the sign in endpoints', () => {
    auth.login('ada@example.com', 'correct horse battery staple').subscribe();

    const request = http.expectOne(`${authUrl}/login`);
    expect(request.request.headers.has('Authorization')).toBe(false);
    request.flush(sessionPayload('access-1', 'refresh-1'));
  });

  it('should leave local assets untouched', () => {
    signIn();

    client.get('assets/icons/logo.svg', { responseType: 'text' }).subscribe();

    const request = http.expectOne('assets/icons/logo.svg');
    expect(request.request.headers.has('Authorization')).toBe(false);
    request.flush('<svg></svg>');
  });

  it('should renew an expired session and replay the request', () => {
    signIn();

    let received: unknown;
    client.get(productsUrl).subscribe((result) => (received = result));

    // The access token expired.
    http
      .expectOne(productsUrl)
      .flush(
        { error: { code: 'invalid_token', message: 'Your session is not valid anymore.' } },
        { status: 401, statusText: 'Unauthorized' },
      );

    // The session is refreshed behind the scenes...
    http.expectOne(`${authUrl}/refresh`).flush(sessionPayload('access-2', 'refresh-2'));

    // ...and the original request runs again with the new token.
    const replay = http.expectOne(productsUrl);
    expect(replay.request.headers.get('Authorization')).toBe('Bearer access-2');
    replay.flush({ items: [{ id: 'p-1' }] });

    expect(received).toEqual({ items: [{ id: 'p-1' }] });
    expect(auth.isAuthenticated()).toBe(true);
  });

  it('should refresh only once for requests that fail together', () => {
    signIn();

    client.get(productsUrl).subscribe({ error: () => undefined });
    client.get(`${environment.billingApiUrl}/invoices`).subscribe({ error: () => undefined });

    const expired = { error: { code: 'invalid_token', message: 'Expired.' } };
    http.expectOne(productsUrl).flush(expired, { status: 401, statusText: 'Unauthorized' });
    http.expectOne(`${environment.billingApiUrl}/invoices`).flush(expired, { status: 401, statusText: 'Unauthorized' });

    // A single refresh serves both.
    http.expectOne(`${authUrl}/refresh`).flush(sessionPayload('access-2', 'refresh-2'));

    http.expectOne(productsUrl).flush({ items: [] });
    http.expectOne(`${environment.billingApiUrl}/invoices`).flush({ items: [] });
  });

  it('should give up and end the session when the refresh is refused', () => {
    signIn();

    let failure: unknown;
    client.get(productsUrl).subscribe({ error: (error) => (failure = error) });

    http
      .expectOne(productsUrl)
      .flush(
        { error: { code: 'invalid_token', message: 'Your session is not valid anymore.' } },
        { status: 401, statusText: 'Unauthorized' },
      );
    http
      .expectOne(`${authUrl}/refresh`)
      .flush(
        { error: { code: 'invalid_refresh_token', message: 'Session expired.' } },
        { status: 401, statusText: 'Unauthorized' },
      );

    expect(failure).toBeTruthy();
    expect(auth.isAuthenticated()).toBe(false);
    expect(auth.hasStoredSession()).toBe(false);
  });

  it('should not try to refresh when there is no session at all', () => {
    let failure: unknown;
    client.get(productsUrl).subscribe({ error: (error) => (failure = error) });

    http
      .expectOne(productsUrl)
      .flush(
        { error: { code: 'missing_token', message: 'Sign in to continue.' } },
        { status: 401, statusText: 'Unauthorized' },
      );

    expect(failure).toBeTruthy();
  });

  it('should let other failures through untouched', () => {
    signIn();

    let failure: unknown;
    client.get(productsUrl).subscribe({ error: (error) => (failure = error) });

    http
      .expectOne(productsUrl)
      .flush(
        { error: { code: 'internal_error', message: 'An unexpected error occurred.' } },
        { status: 500, statusText: 'Internal Server Error' },
      );

    expect((failure as { code: string }).code).toBe('internal_error');
  });
});
