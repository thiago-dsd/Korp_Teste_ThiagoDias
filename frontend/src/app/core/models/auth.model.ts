/** Person signed in to the application. */
export interface AuthUser {
  id: string;
  email: string;
  name: string;
  createdAt: string;
}

/** A signed in session: who it belongs to and the token that proves it. */
export interface Session {
  user: AuthUser;
  accessToken: string;
}
