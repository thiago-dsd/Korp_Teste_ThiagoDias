/**
 * Error payload returned by both services.
 *
 * Every failure crosses the network in the same shape, so the screens can
 * always show a message written for the operator and, when the request was
 * rejected by validation, point at the offending fields.
 */
export interface ApiErrorBody {
  code: string;
  message: string;
  details?: Record<string, string>;
  request_id?: string;
}

/** Error thrown by the API layer after an unsuccessful request. */
export class ApiError extends Error {
  constructor(
    readonly code: string,
    message: string,
    readonly status: number,
    readonly details: Record<string, string> = {},
    readonly requestId?: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }

  /** True when the service is unreachable or answered that it cannot serve now. */
  get isUnavailable(): boolean {
    return this.status === 0 || this.status === 503;
  }

  /** True when the request clashed with the current state of the resource. */
  get isConflict(): boolean {
    return this.status === 409;
  }
}
