import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { environment } from 'src/environments/environment';
import { apiErrorInterceptor } from '../interceptor/api-error.interceptor';
import { AuthService } from './auth.service';

const authUrl = `${environment.identityApiUrl}/auth`;

function sessionPayload(accessToken = 'access-1', refreshToken = 'refresh-1') {
  return {
    user: { id: 'u-1', email: 'ada@example.com', name: 'Ada Lovelace', created_at: '2026-01-01T00:00:00Z' },
    access_token: accessToken,
    refresh_token: refreshToken,
    token_type: 'Bearer',
    expires_in: 900,
  };
}

describe('AuthService', () => {
  let service: AuthService;
  let http: HttpTestingController;

  beforeEach(() => {
    localStorage.clear();
    TestBed.configureTestingModule({
      providers: [provideHttpClient(withInterceptors([apiErrorInterceptor])), provideHttpClientTesting()],
    });
    service = TestBed.inject(AuthService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
    localStorage.clear();
  });

  it('should start a session when registering', () => {
    service.register('ada@example.com', 'Ada Lovelace', 'correct horse battery staple').subscribe();

    const request = http.expectOne(`${authUrl}/register`);
    expect(request.request.body).toEqual({
      email: 'ada@example.com',
      name: 'Ada Lovelace',
      password: 'correct horse battery staple',
    });
    request.flush(sessionPayload());

    expect(service.isAuthenticated()).toBe(true);
    expect(service.currentUser()?.email).toBe('ada@example.com');
    expect(service.accessToken()).toBe('access-1');
  });

  it('should keep the access token out of storage and the refresh token in it', () => {
    service.login('ada@example.com', 'correct horse battery staple').subscribe();
    http.expectOne(`${authUrl}/login`).flush(sessionPayload('access-1', 'refresh-1'));

    expect(localStorage.getItem('invoice-system.refresh-token')).toBe('refresh-1');
    expect(service.hasStoredSession()).toBe(true);
  });

  it('should report failed sign in without starting a session', () => {
    let failure: unknown;
    service.login('ada@example.com', 'wrong').subscribe({ error: (error) => (failure = error) });

    http
      .expectOne(`${authUrl}/login`)
      .flush(
        { error: { code: 'invalid_credentials', message: 'Email or password is incorrect.' } },
        { status: 401, statusText: 'Unauthorized' },
      );

    expect(service.isAuthenticated()).toBe(false);
    expect((failure as { code: string }).code).toBe('invalid_credentials');
  });

  it('should rotate the stored refresh token when refreshing', () => {
    service.login('ada@example.com', 'correct horse battery staple').subscribe();
    http.expectOne(`${authUrl}/login`).flush(sessionPayload('access-1', 'refresh-1'));

    let renewed: string | undefined;
    service.refreshSession().subscribe((token) => (renewed = token));

    const request = http.expectOne(`${authUrl}/refresh`);
    expect(request.request.body).toEqual({ refresh_token: 'refresh-1' });
    request.flush(sessionPayload('access-2', 'refresh-2'));

    expect(renewed).toBe('access-2');
    expect(service.accessToken()).toBe('access-2');
    expect(localStorage.getItem('invoice-system.refresh-token')).toBe('refresh-2');
  });

  it('should refresh a single time when several callers ask at once', () => {
    service.login('ada@example.com', 'correct horse battery staple').subscribe();
    http.expectOne(`${authUrl}/login`).flush(sessionPayload());

    const tokens: string[] = [];
    service.refreshSession().subscribe((token) => tokens.push(token));
    service.refreshSession().subscribe((token) => tokens.push(token));

    http.expectOne(`${authUrl}/refresh`).flush(sessionPayload('access-2', 'refresh-2'));

    expect(tokens).toEqual(['access-2', 'access-2']);
  });

  it('should clear the session when the refresh token is refused', () => {
    service.login('ada@example.com', 'correct horse battery staple').subscribe();
    http.expectOne(`${authUrl}/login`).flush(sessionPayload());

    service.refreshSession().subscribe({ error: () => undefined });
    http
      .expectOne(`${authUrl}/refresh`)
      .flush(
        { error: { code: 'invalid_refresh_token', message: 'Your session has expired.' } },
        { status: 401, statusText: 'Unauthorized' },
      );

    expect(service.isAuthenticated()).toBe(false);
    expect(service.hasStoredSession()).toBe(false);
  });

  it('should sign out locally even when the service cannot be reached', () => {
    service.login('ada@example.com', 'correct horse battery staple').subscribe();
    http.expectOne(`${authUrl}/login`).flush(sessionPayload());

    service.logout().subscribe();
    http.expectOne(`${authUrl}/logout`).error(new ProgressEvent('error'), { status: 0, statusText: 'Unknown' });

    expect(service.isAuthenticated()).toBe(false);
    expect(service.hasStoredSession()).toBe(false);
  });

  it('should delete the account and end the session', () => {
    service.login('ada@example.com', 'correct horse battery staple').subscribe();
    http.expectOne(`${authUrl}/login`).flush(sessionPayload());

    service.deleteAccount('correct horse battery staple').subscribe();

    const request = http.expectOne(`${authUrl}/me`);
    expect(request.request.method).toBe('DELETE');
    expect(request.request.body).toEqual({ password: 'correct horse battery staple' });
    request.flush(null, { status: 204, statusText: 'No Content' });

    expect(service.isAuthenticated()).toBe(false);
    expect(service.hasStoredSession()).toBe(false);
  });

  it('should keep the session when deleting the account is refused', () => {
    service.login('ada@example.com', 'correct horse battery staple').subscribe();
    http.expectOne(`${authUrl}/login`).flush(sessionPayload());

    service.deleteAccount('wrong password').subscribe({ error: () => undefined });
    http
      .expectOne(`${authUrl}/me`)
      .flush(
        { error: { code: 'invalid_credentials', message: 'Email or password is incorrect.' } },
        { status: 401, statusText: 'Unauthorized' },
      );

    expect(service.isAuthenticated()).toBe(true);
  });

  it('should restore a session from the stored refresh token', () => {
    localStorage.setItem('invoice-system.refresh-token', 'refresh-1');

    let restored: unknown;
    service.restoreSession().subscribe((user) => (restored = user));
    http.expectOne(`${authUrl}/refresh`).flush(sessionPayload('access-2', 'refresh-2'));

    expect(restored).toBeTruthy();
    expect(service.isAuthenticated()).toBe(true);
  });

  it('should not try to restore a session when nothing is stored', () => {
    let restored: unknown = 'untouched';
    service.restoreSession().subscribe((user) => (restored = user));

    expect(restored).toBeNull();
    expect(service.isAuthenticated()).toBe(false);
  });
});
