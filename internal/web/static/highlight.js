// Lightweight, accurate Go syntax highlighter for Plugo code cells and Markdown blocks.

const GO_KEYWORDS = new Set([
  'break', 'case', 'chan', 'const', 'continue', 'default', 'defer', 'else',
  'fallthrough', 'for', 'func', 'go', 'goto', 'if', 'import', 'interface',
  'map', 'package', 'range', 'return', 'select', 'struct', 'switch', 'type', 'var'
]);

const GO_TYPES = new Set([
  'any', 'bool', 'byte', 'comparable', 'complex64', 'complex128', 'error',
  'float32', 'float64', 'int', 'int8', 'int16', 'int32', 'int64', 'rune',
  'string', 'uint', 'uint8', 'uint16', 'uint32', 'uint64', 'uintptr'
]);

const GO_BUILTINS = new Set([
  'append', 'cap', 'clear', 'close', 'complex', 'copy', 'delete', 'false',
  'imag', 'iota', 'len', 'make', 'max', 'min', 'new', 'nil', 'panic',
  'print', 'println', 'real', 'recover', 'true'
]);

export function escapeHTML(str) {
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

export function highlightGo(source) {
  const src = String(source || '');
  let out = '';
  let i = 0;
  const n = src.length;

  while (i < n) {
    const ch = src[i];

    // Line comment
    if (ch === '/' && src[i + 1] === '/') {
      let end = src.indexOf('\n', i);
      if (end === -1) end = n;
      out += `<span class="tok-cmt">${escapeHTML(src.slice(i, end))}</span>`;
      i = end;
      continue;
    }

    // Block comment
    if (ch === '/' && src[i + 1] === '*') {
      let end = src.indexOf('*/', i + 2);
      end = end === -1 ? n : end + 2;
      out += `<span class="tok-cmt">${escapeHTML(src.slice(i, end))}</span>`;
      i = end;
      continue;
    }

    // Raw string
    if (ch === '`') {
      let end = src.indexOf('`', i + 1);
      end = end === -1 ? n : end + 1;
      out += `<span class="tok-str">${escapeHTML(src.slice(i, end))}</span>`;
      i = end;
      continue;
    }

    // Double-quoted string or rune literal
    if (ch === '"' || ch === '\'') {
      const quote = ch;
      let j = i + 1;
      while (j < n) {
        if (src[j] === '\\') {
          j += 2;
          continue;
        }
        if (src[j] === quote) {
          j++;
          break;
        }
        if (src[j] === '\n') break;
        j++;
      }
      out += `<span class="tok-str">${escapeHTML(src.slice(i, j))}</span>`;
      i = j;
      continue;
    }

    // Numbers
    if (ch >= '0' && ch <= '9') {
      let j = i + 1;
      while (j < n && /[0-9a-fA-FxXoObB_.eEpP+-]/.test(src[j])) {
        if ((src[j] === '+' || src[j] === '-') && !/[eEpP]/.test(src[j - 1])) break;
        j++;
      }
      out += `<span class="tok-num">${escapeHTML(src.slice(i, j))}</span>`;
      i = j;
      continue;
    }

    // Identifiers, keywords, types, builtins, package selectors, calls
    if (/[A-Za-z_]/.test(ch)) {
      let j = i + 1;
      while (j < n && /[A-Za-z0-9_]/.test(src[j])) j++;
      const word = src.slice(i, j);
      const next = src[j];

      if (GO_KEYWORDS.has(word)) {
        out += `<span class="tok-kw">${word}</span>`;
      } else if (GO_TYPES.has(word)) {
        out += `<span class="tok-type">${word}</span>`;
      } else if (GO_BUILTINS.has(word)) {
        out += `<span class="tok-builtin">${word}</span>`;
      } else if (next === '(') {
        out += `<span class="tok-fn">${word}</span>`;
      } else if (next === '.' && word[0] >= 'a' && word[0] <= 'z') {
        out += `<span class="tok-pkg">${word}</span>`;
      } else {
        out += word;
      }
      i = j;
      continue;
    }

    out += escapeHTML(ch);
    i++;
  }

  // Ensure a trailing newline renders an empty final line matching textarea height.
  if (src.endsWith('\n')) {
    out += ' ';
  }
  return out;
}
