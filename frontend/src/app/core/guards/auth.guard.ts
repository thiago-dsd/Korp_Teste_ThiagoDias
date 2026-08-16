import { inject } from '@angular/core';
import { CanActivateFn, Router, UrlTree } from '@angular/router';
import { Observable, map } from 'rxjs';

import { AuthService } from '../services/auth.service';

/**
 * Keeps the application pages for signed in people.
 *
 * On a fresh page load there is no session in memory yet, so the guard gives
 * the stored refresh token a chance first: reloading a page must not throw
 * someone back to the sign in screen.
 */
export const authGuard: CanActivateFn = (_route, state): boolean | UrlTree | Observable<boolean | UrlTree> => {
  const auth = inject(AuthService);
  const router = inject(Router);

  if (auth.isAuthenticated()) {
    return true;
  }

  const signIn = (): UrlTree => router.createUrlTree(['/auth/sign-in'], { queryParams: { returnUrl: state.url } });

  if (!auth.hasStoredSession()) {
    return signIn();
  }

  return auth.restoreSession().pipe(map((user) => (user ? true : signIn())));
};

/** Sends someone who is already signed in away from the sign in screens. */
export const guestGuard: CanActivateFn = (): boolean | UrlTree => {
  const auth = inject(AuthService);
  const router = inject(Router);

  return auth.isAuthenticated() ? router.createUrlTree(['/home']) : true;
};
