import { csvFilename, toCsv } from './csv';

describe('toCsv', () => {
  it('writes a header and one line per row', () => {
    const csv = toCsv(['code', 'balance'], [['P-1', 10]]);

    expect(csv).toBe('code,balance\r\nP-1,10');
  });

  it('quotes values holding a separator, a quote or a line break', () => {
    const csv = toCsv(['description'], [['Bolt, 8mm'], ['He said "no"'], ['two\nlines']]);

    expect(csv).toBe('description\r\n"Bolt, 8mm"\r\n"He said ""no"""\r\n"two\nlines"');
  });

  it('keeps a spreadsheet from executing a value that looks like a formula', () => {
    const csv = toCsv(['description'], [['=1+1'], ['+SUM(A1)'], ['-cmd'], ['@here']]);

    // A leading quote makes the spreadsheet read it as text.
    expect(csv).toBe("description\r\n'=1+1\r\n'+SUM(A1)\r\n'-cmd\r\n'@here");
  });

  it('writes an empty cell for a missing value', () => {
    const csv = toCsv(['a', 'b'], [[null, undefined]]);

    expect(csv).toBe('a,b\r\n,');
  });
});

describe('csvFilename', () => {
  it('stamps the file with the moment it was taken', () => {
    const name = csvFilename('products', new Date('2026-08-17T10:30:00Z'));

    expect(name).toBe('products-2026-08-17-10-30-00.csv');
  });
});
