import { HttpClient } from '@angular/common/http';
import { Injectable, computed, inject, signal } from '@angular/core';
import { Observable, catchError, finalize, map, of, shareReplay, tap, throwError } from 'rxjs';

import { environment } from 'src/environments/environment';
import { ApiError } from '../models/api-error.model';
import { UserRole, AuthUser } from '../models/auth.model';

/** Where the refresh token is kept between reloads. */
const REFRESH_TOKEN_KEY = 'invoice-system.refresh-token';

interface SessionPayload {
  user: { id: string; email: string; name: string; role: UserRole; created_at: string };
  access_token: string;
  refresh_token: string;
  token_type: string;
  expires_in: number;
}

/**
 * Holds the session of the person using the application.
 *
 * The access token is kept in memory only, so it disappears when the tab is
 * closed. The refresh token has to survive a reload to keep someone signed in,
 * so it goes to local storage; it is rotated on every use and the service
 * cancels the whole session when it sees an old one being replayed, which is
 * what limits the damage if it ever leaks.
 */
@Injectable({ providedIn: 'root' })
export class AuthService {
  private readonly http = inject(HttpClient);
  private readonly baseUrl = `${environment.identityApiUrl}/auth`;

  private readonly session = signal<AuthUser | null>(null);
  private readonly token = signal<string | null>(null);

  /** Shared refresh in flight, so parallel requests trigger a single call. */
  private refreshing: Observable<string> | null = null;

  /** Who is signed in, or null. */
  readonly currentUser = this.session.asReadonly();
  readonly accessToken = this.token.asReadonly();
  readonly isAuthenticated = computed(() => this.session() !== null);

  /**
   * Whether the person may change the catalogue and the balances. The service
   * enforces this regardless; the screens use it to avoid offering an action
   * that would only come back as a refusal.
   */
  readonly isAdmin = computed(() => this.session()?.role === 'admin');

  /** True when a session may be restored from a stored refresh token. */
  hasStoredSession(): boolean {
    return this.readRefreshToken() !== null;
  }

  register(email: string, name: string, password: string): Observable<AuthUser> {
    return this.http
      .post<SessionPayload>(`${this.baseUrl}/register`, { email, name, password })
      .pipe(map((payload) => this.startSession(payload)));
  }

  login(email: string, password: string): Observable<AuthUser> {
    return this.http
      .post<SessionPayload>(`${this.baseUrl}/login`, { email, password })
      .pipe(map((payload) => this.startSession(payload)));
  }

  /** Ends the session on the server and clears it locally either way. */
  logout(): Observable<void> {
    const refreshToken = this.readRefreshToken();
    this.clearSession();

    if (!refreshToken) {
      return of(undefined);
    }
    return this.http.post<void>(`${this.baseUrl}/logout`, { refresh_token: refreshToken }).pipe(
      catchError(() => of(undefined)),
      map(() => undefined),
    );
  }

  /** Reads the profile of the signed in person. */
  profile(): Observable<AuthUser> {
    return this.http.get<SessionPayload['user']>(`${this.baseUrl}/me`).pipe(
      map(toUser),
      tap((user) => this.session.set(user)),
    );
  }

  /** Deletes the account after confirming the password, and signs out. */
  deleteAccount(password: string): Observable<void> {
    return this.http.delete<void>(`${this.baseUrl}/me`, { body: { password } }).pipe(
      tap(() => this.clearSession()),
      map(() => undefined),
    );
  }

  /**
   * Exchanges the stored refresh token for a new session and returns the new
   * access token. Callers share one request: a page firing several calls at
   * once must not rotate the token several times.
   */
  refreshSession(): Observable<string> {
    if (this.refreshing) {
      return this.refreshing;
    }

    const refreshToken = this.readRefreshToken();
    if (!refreshToken) {
      return throwError(() => new ApiError('no_session', 'Sign in to continue.', 401));
    }

    this.refreshing = this.http.post<SessionPayload>(`${this.baseUrl}/refresh`, { refresh_token: refreshToken }).pipe(
      map((payload) => this.startSession(payload)),
      map(() => this.token() as string),
      catchError((error: unknown) => {
        // The session is gone: either it expired or a replay was detected.
        this.clearSession();
        return throwError(() => error);
      }),
      finalize(() => (this.refreshing = null)),
      shareReplay(1),
    );

    return this.refreshing;
  }

  /** Restores the session on start up when a refresh token is stored. */
  restoreSession(): Observable<AuthUser | null> {
    if (!this.hasStoredSession()) {
      return of(null);
    }
    return this.refreshSession().pipe(
      map(() => this.session()),
      catchError(() => of(null)),
    );
  }

  private startSession(payload: SessionPayload): AuthUser {
    const user = toUser(payload.user);
    this.session.set(user);
    this.token.set(payload.access_token);
    this.writeRefreshToken(payload.refresh_token);
    return user;
  }

  private clearSession(): void {
    this.session.set(null);
    this.token.set(null);
    this.writeRefreshToken(null);
  }

  private readRefreshToken(): string | null {
    try {
      return localStorage.getItem(REFRESH_TOKEN_KEY);
    } catch {
      // Storage can be unavailable, for example in private browsing.
      return null;
    }
  }

  private writeRefreshToken(token: string | null): void {
    try {
      if (token) {
        localStorage.setItem(REFRESH_TOKEN_KEY, token);
      } else {
        localStorage.removeItem(REFRESH_TOKEN_KEY);
      }
    } catch {
      // Not being able to store it only means the session ends with the tab.
    }
  }
}

function toUser(payload: SessionPayload['user']): AuthUser {
  // A token issued before roles existed reports none; treating that as the
  // lesser role keeps the screens from offering what would be refused.
  return {
    id: payload.id,
    email: payload.email,
    name: payload.name,
    role: payload.role ?? 'operator',
    createdAt: payload.created_at,
  };
}
