/**
 * The answer every bulk endpoint gives.
 *
 * One call touches many resources, so a single status code cannot say what
 * happened. The services always answer with a summary to read first and one
 * compact line per item, carrying the position it had in the request so the
 * screen can line results up with what it sent without matching on values.
 */
export interface BulkResponse {
  /**
   * Whether the items stand or fall together. An atomic call that was refused
   * changed nothing at all, which is a different message to the operator than
   * "three of your twenty items failed".
   */
  atomic: boolean;
  summary: BulkSummary;
  results: BulkResult[];
}

export interface BulkSummary {
  requested: number;
  succeeded: number;
  failed: number;
  skipped: number;
}

/** What happened to a single item. */
export type BulkStatus = 'succeeded' | 'failed' | 'skipped';

export interface BulkResult {
  /** Position the item had in the request. */
  index: number;
  status: BulkStatus;
  id?: string;
  /** The natural key: a product code, an invoice number. */
  reference?: string;
  error?: BulkItemError;
}

export interface BulkItemError {
  code: string;
  message: string;
  details?: Record<string, string>;
}

/**
 * How many items one call may carry. It matches the limit the services
 * enforce, so the screen can stop a selection before the whole batch is
 * refused for being too large.
 */
export const BULK_MAX_ITEMS = 100;

/** The items worth showing back to the operator: the ones that did not go through. */
export function bulkFailures(response: BulkResponse): BulkResult[] {
  return response.results.filter((result) => result.status === 'failed');
}

/** The ids of the items that were applied, so the screen can drop them from a selection. */
export function bulkSucceededIds(response: BulkResponse): string[] {
  return response.results
    .filter((result) => result.status === 'succeeded' && result.id)
    .map((result) => result.id as string);
}

/** True when the payload looks like a bulk answer rather than an error envelope. */
export function isBulkResponse(payload: unknown): payload is BulkResponse {
  if (typeof payload !== 'object' || payload === null) {
    return false;
  }
  const candidate = payload as Partial<BulkResponse>;
  return Array.isArray(candidate.results) && typeof candidate.summary === 'object';
}
