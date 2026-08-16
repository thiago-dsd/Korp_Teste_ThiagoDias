import { HttpErrorResponse, HttpInterceptorFn } from '@angular/common/http';
import { catchError, throwError } from 'rxjs';

import { ApiError, ApiErrorBody } from '../models/api-error.model';

/**
 * Turns every failed request into an {@link ApiError}.
 *
 * Both services answer errors with the same envelope, so translating it once
 * here keeps the screens free of HTTP details: they only deal with a code, a
 * message written for the operator and the offending fields.
 */
export const apiErrorInterceptor: HttpInterceptorFn = (request, next) =>
  next(request).pipe(
    catchError((error: unknown) => {
      if (!(error instanceof HttpErrorResponse)) {
        return throwError(() => error);
      }
      return throwError(() => toApiError(error));
    }),
  );

function toApiError(response: HttpErrorResponse): ApiError {
  // A status of zero means the browser never got an answer: the service is
  // down, the network failed or the request was blocked by CORS.
  if (response.status === 0) {
    return new ApiError(
      'service_unreachable',
      'The service could not be reached. Check whether it is running and try again.',
      0,
    );
  }

  const body = response.error as { error?: ApiErrorBody } | null;
  const payload = body?.error;

  if (payload?.code && payload?.message) {
    return new ApiError(payload.code, payload.message, response.status, payload.details ?? {}, payload.request_id);
  }

  return new ApiError('unexpected_error', 'Something went wrong. Please try again.', response.status);
}
