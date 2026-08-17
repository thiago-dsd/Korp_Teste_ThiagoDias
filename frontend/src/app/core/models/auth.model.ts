/** Person signed in to the application. */
export interface AuthUser {
  id: string;
  email: string;
  name: string;
  /**
   * What this person may do. Operators issue and print invoices; managing the
   * catalogue and the balances is kept to administrators, so the screens stop
   * offering what the service would refuse anyway.
   */
  role: UserRole;
  createdAt: string;
}

export type UserRole = 'operator' | 'admin';

/** A signed in session: who it belongs to and the token that proves it. */
export interface Session {
  user: AuthUser;
  accessToken: string;
}
