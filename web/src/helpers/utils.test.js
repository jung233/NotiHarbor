import { openPage, safeHttpURL, sanitizeHTML } from './utils';

describe('sanitizeHTML', () => {
  it('removes executable elements, event handlers, and script URL schemes', () => {
    const dirtyHTML = [
      '<p onclick="alert(1)">safe text</p>',
      '<script>alert(document.cookie)</script>',
      '<img src="x" onerror="alert(1)">',
      '<svg onload="alert(1)"><circle></circle></svg>',
      '<math><mtext><script>alert(2)</script></mtext></math>',
      '<a href="JaVaScRiPt:alert(1)" onmouseover="alert(1)">bad link</a>',
      '<a href="data:text/html,<script>alert(1)</script>">data link</a>',
    ].join('');

    const sanitized = sanitizeHTML(dirtyHTML);
    const documentFragment = new DOMParser().parseFromString(sanitized, 'text/html');

    expect(documentFragment.querySelector('script, img, svg')).toBeNull();
    expect(documentFragment.querySelector('[onclick], [onerror], [onload], [onmouseover]')).toBeNull();
    expect(documentFragment.querySelector('a[href^="javascript:"], a[href^="data:"]')).toBeNull();
    expect(documentFragment.body.textContent).toContain('safe text');
    expect(documentFragment.body.textContent).toContain('bad link');
  });

  it('preserves safe markup and normalizes allowed links while removing unsafe attributes', () => {
    const sanitized = sanitizeHTML(
      '<p class="external-style"><strong>Important</strong> <a href="https://example.test/path" target="_blank" rel="opener">details</a><table><tr><td colspan="2" style="color:red">cell</td></tr></table></p>'
    );
    const documentFragment = new DOMParser().parseFromString(sanitized, 'text/html');
    const link = documentFragment.querySelector('a');
    const cell = documentFragment.querySelector('td');

    expect(documentFragment.querySelector('strong')?.textContent).toBe('Important');
    expect(link?.getAttribute('href')).toBe('https://example.test/path');
    expect(link?.hasAttribute('target')).toBe(false);
    expect(link?.hasAttribute('rel')).toBe(false);
    expect(cell?.getAttribute('colspan')).toBe('2');
    expect(cell?.hasAttribute('style')).toBe(false);
    expect(documentFragment.querySelector('[class]')).toBeNull();
  });
});

describe('safe navigation URLs', () => {
  afterEach(() => jest.restoreAllMocks());

  it('rejects script/data URLs and blank values while allowing HTTP(S)', () => {
    // eslint-disable-next-line no-script-url -- intentionally exercise an unsafe URL.
    expect(safeHttpURL('javascript:alert(1)')).toBe('');
    expect(safeHttpURL('data:text/html,attack')).toBe('');
    expect(safeHttpURL('')).toBe('');
    expect(safeHttpURL('https://example.test/')).toBe('https://example.test/');
  });

  it('never opens an unsafe URL in a new window', () => {
    const openSpy = jest.spyOn(window, 'open').mockImplementation(() => null);

    // eslint-disable-next-line no-script-url -- intentionally exercise an unsafe URL.
    openPage('javascript:alert(1)');
    openPage('data:text/html,attack');
    expect(openSpy).not.toHaveBeenCalled();

    openPage('https://example.test/path');
    expect(openSpy).toHaveBeenCalledWith(
      'https://example.test/path',
      '_blank',
      'noopener,noreferrer'
    );
  });
});
