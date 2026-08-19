import { parseCatalogue } from './product-import.component';

describe('parseCatalogue', () => {
  it('reads one product per line', () => {
    const lines = parseCatalogue('P-1, Steel bolt, 10\nP-2, Hammer, 3');

    expect(lines.map((line) => line.product)).toEqual([
      { code: 'P-1', description: 'Steel bolt', balance: 10 },
      { code: 'P-2', description: 'Hammer', balance: 3 },
    ]);
  });

  it('accepts semicolons and tabs, which is what spreadsheets paste', () => {
    expect(parseCatalogue('P-1; Steel bolt; 10')[0].product).toEqual({
      code: 'P-1',
      description: 'Steel bolt',
      balance: 10,
    });
    expect(parseCatalogue('P-1\tSteel bolt\t10')[0].product).toEqual({
      code: 'P-1',
      description: 'Steel bolt',
      balance: 10,
    });
  });

  it('keeps a separator that is inside a quoted description', () => {
    expect(parseCatalogue('P-1,"Bolt, 8mm",10')[0].product).toEqual({
      code: 'P-1',
      description: 'Bolt, 8mm',
      balance: 10,
    });
  });

  it('skips the header row a spreadsheet brings along', () => {
    const lines = parseCatalogue('code,description,balance\nP-1,Steel bolt,10');

    expect(lines.length).toBe(1);
    expect(lines[0].product?.code).toBe('P-1');
  });

  it('treats a missing balance as none in stock', () => {
    expect(parseCatalogue('P-1, Steel bolt')[0].product).toEqual({
      code: 'P-1',
      description: 'Steel bolt',
      balance: 0,
    });
  });

  it('ignores blank lines instead of reporting them', () => {
    expect(parseCatalogue('P-1, Steel bolt, 1\n\n   \nP-2, Hammer, 2').length).toBe(2);
  });

  it('reports the line number of a row it cannot read', () => {
    const lines = parseCatalogue('P-1, Steel bolt, 10\nP-2\nP-3, Hammer, -4\nP-4, Saw, half');

    expect(lines[1]).toEqual({ line: 2, error: 'needsCodeAndDescription' });
    expect(lines[2].line).toBe(3);
    expect(lines[2].error).toBe('invalidBalance');
    expect(lines[3].line).toBe(4);
  });
});
