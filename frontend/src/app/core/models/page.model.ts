/**
 * A page of a listing.
 *
 * Paging is done by cursor: the client sends back the cursor it received to
 * read the next page, and stops when it comes back empty.
 */
export interface Page<T> {
  items: T[];
  /** Empty on the last page. */
  nextCursor: string;
}
