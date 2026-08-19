/**
 * Turning a listing into a spreadsheet.
 *
 * Operators reconcile stock and invoices in a spreadsheet, and retyping a
 * screen into one is where the numbers stop matching. Exporting is done here
 * rather than on the services because what is worth exporting is exactly what
 * is on screen, filters included.
 */

/** A cell as it goes into the file. Dates are written as ISO strings. */
export type CsvValue = string | number | null | undefined;

/**
 * Quotes a value for CSV.
 *
 * A value starting with a formula character is prefixed with a quote: a
 * spreadsheet would otherwise execute it, which turns an exported description
 * into a way of running something on the machine that opens the file.
 */
function escapeCell(value: CsvValue): string {
  if (value === null || value === undefined) {
    return '';
  }

  let text = String(value);
  if (/^[=+\-@\t\r]/.test(text)) {
    text = `'${text}`;
  }
  if (/[",\n\r]/.test(text)) {
    return `"${text.replace(/"/g, '""')}"`;
  }
  return text;
}

/** Builds the contents of a CSV file from a header and its rows. */
export function toCsv(headers: string[], rows: CsvValue[][]): string {
  const lines = [headers.map(escapeCell).join(','), ...rows.map((row) => row.map(escapeCell).join(','))];
  // Excel needs CRLF to treat the file as one row per line on every platform.
  return lines.join('\r\n');
}

/**
 * Hands the file to the browser.
 *
 * The BOM is what makes Excel read the file as UTF-8; without it accented
 * descriptions arrive mangled, which is the first thing anyone notices.
 */
export function downloadCsv(filename: string, content: string): void {
  const blob = new Blob([`\ufeff${content}`], { type: 'text/csv;charset=utf-8;' });
  const url = URL.createObjectURL(blob);

  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  link.style.display = 'none';
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);

  // Released on the next tick: revoking it straight away cancels the download
  // in some browsers.
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

/** Names an export after what it holds and when it was taken. */
export function csvFilename(prefix: string, now = new Date()): string {
  const stamp = now.toISOString().slice(0, 19).replace(/[:T]/g, '-');
  return `${prefix}-${stamp}.csv`;
}
