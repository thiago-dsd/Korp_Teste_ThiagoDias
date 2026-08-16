import { HttpInterceptorFn, HttpRequest } from '@angular/common/http';
import { inject } from '@angular/core';
import { catchError, switchMap, throwError } from 'rxjs';

import { AuthService } from '../services/auth.service';
import { ApiError } from '../models/api-error.model';

/** Endpoints that must never carry an access token. */
const PUBLIC_PATHS = ['/auth/login', '/auth/register', '/auth/refresh', '/auth/logout', '/.well-known/'];

/**
 * Puts the access token on every call to the services and renews it when it
 * has expired.
 *
 * Access tokens are short lived on purpose, so a request failing with 401 is
 * expected rather than exceptional: the interceptor refreshes the session once
 * and replays the request, and the person using the application never notices.
 * Refreshes are shared by {@link AuthService}, so a page firing several calls
 * at once renews the session a single time.
 */
export const authInterceptor: HttpInterceptorFn = (request, next) => {
  const auth = inject(AuthService);

  if (isPublic(request) || !isApiRequest(request)) {
    return next(request);
  }

  const token = auth.accessToken();
  const authorized = token ? withToken(request, token) : request;

  return next(authorized).pipe(
    catchError((error: unknown) => {
      if (!isExpiredSession(error) || !auth.hasStoredSession()) {
        return throwError(() => error);
      }

      return auth.refreshSession().pipe(
        switchMap((renewed) => next(withToken(request, renewed))),
        catchError(() => throwError(() => error)),
      );
    }),
  );
};

function withToken(request: HttpRequest<unknown>, token: string): HttpRequest<unknown> {
  return request.clone({ setHeaders: { Authorization: `Bearer ${token}` } });
}

function isPublic(request: HttpRequest<unknown>): boolean {
  return PUBLIC_PATHS.some((path) => request.url.includes(path));
}

/** Local assets are fetched with the same client and must stay untouched. */
function isApiRequest(request: HttpRequest<unknown>): boolean {
  return request.url.startsWith('http://') || request.url.startsWith('https://') || request.url.startsWith('/api');
}

function isExpiredSession(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401;
}
