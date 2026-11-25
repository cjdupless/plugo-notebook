// Clean, safe Markdown renderer with Go syntax highlighting for fenced code blocks.
import { escapeHTML, highlightGo } from '/highlight.js';

export function renderMarkdown(source) {
  const raw = String(source || '').replace(/\r\n/g, '\n');
  if (!raw.trim()) {
    return '';
  }

  const lines = raw.split('\n');
  const html = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    // Blank line
    if (!line.trim()) {
      i++;
      continue;
    }

    // Fenced code block ```lang
    const fenceMatch = line.match(/^```([\w+-]*)\s*$/);
    if (fenceMatch) {
      const lang = (fenceMatch[1] || '').toLowerCase();
      i++;
      const codeLines = [];
      while (i < lines.length && !/^```\s*$/.test(lines[i])) {
        codeLines.push(lines[i]);
        i++;
      }
      if (i < lines.length) i++;
      const code = codeLines.join('\n');
      const renderedCode = (lang === 'go' || lang === 'golang') ? highlightGo(code) : escapeHTML(code);
      html.push(`<pre><code class="lang-${escapeHTML(lang)}">${renderedCode}</code></pre>`);
      continue;
    }

    // Horizontal rule
    if (/^\s*([-*_])\s*\1\s*\1[\s\-*_]*$/.test(line)) {
      html.push('<hr />');
      i++;
      continue;
    }

    // ATX Headings (# .. ######)
    const headingMatch = line.match(/^(#{1,6})\s+(.*)$/);
    if (headingMatch) {
      const level = headingMatch[1].length;
      html.push(`<h${level}>${renderInline(headingMatch[2].trim())}</h${level}>`);
      i++;
      continue;
    }

    // Blockquote
    if (/^\s*>\s?/.test(line)) {
      const quoteLines = [];
      while (i < lines.length && /^\s*>\s?/.test(lines[i])) {
        quoteLines.push(lines[i].replace(/^\s*>\s?/, ''));
        i++;
      }
      html.push(`<blockquote>${renderMarkdown(quoteLines.join('\n'))}</blockquote>`);
      continue;
    }

    // Pipe table
    if (line.includes('|') && i + 1 < lines.length && /^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)+\|?\s*$/.test(lines[i + 1])) {
      const headerCells = splitTableRow(line);
      i += 2;
      const bodyRows = [];
      while (i < lines.length && lines[i].trim() && lines[i].includes('|')) {
        bodyRows.push(splitTableRow(lines[i]));
        i++;
      }
      const thead = `<thead><tr>${headerCells.map((c) => `<th>${renderInline(c)}</th>`).join('')}</tr></thead>`;
      const tbody = bodyRows.length
        ? `<tbody>${bodyRows.map((r) => `<tr>${r.map((c) => `<td>${renderInline(c)}</td>`).join('')}</tr>`).join('')}</tbody>`
        : '';
      html.push(`<table>${thead}${tbody}</table>`);
      continue;
    }

    // Unordered list (including task list items)
    if (/^\s*[-*+]\s+/.test(line)) {
      const items = [];
      while (i < lines.length && /^\s*[-*+]\s+/.test(lines[i])) {
        let itemText = lines[i].replace(/^\s*[-*+]\s+/, '');
        const taskMatch = itemText.match(/^\[([ xX])\]\s+(.*)$/);
        if (taskMatch) {
          const checked = taskMatch[1].toLowerCase() === 'x' ? 'checked' : '';
          items.push(`<li style="list-style:none;margin-left:-1.1em"><input type="checkbox" disabled ${checked} /> ${renderInline(taskMatch[2])}</li>`);
        } else {
          items.push(`<li>${renderInline(itemText)}</li>`);
        }
        i++;
      }
      html.push(`<ul>${items.join('')}</ul>`);
      continue;
    }

    // Ordered list
    if (/^\s*\d+\.\s+/.test(line)) {
      const items = [];
      while (i < lines.length && /^\s*\d+\.\s+/.test(lines[i])) {
        items.push(`<li>${renderInline(lines[i].replace(/^\s*\d+\.\s+/, ''))}</li>`);
        i++;
      }
      html.push(`<ol>${items.join('')}</ol>`);
      continue;
    }

    // Paragraph (collect consecutive non-block lines)
    const paraLines = [];
    while (
      i < lines.length &&
      lines[i].trim() &&
      !/^```/.test(lines[i]) &&
      !/^#{1,6}\s+/.test(lines[i]) &&
      !/^\s*>\s?/.test(lines[i]) &&
      !/^\s*[-*+]\s+/.test(lines[i]) &&
      !/^\s*\d+\.\s+/.test(lines[i]) &&
      !/^\s*([-*_])\s*\1\s*\1[\s\-*_]*$/.test(lines[i])
    ) {
      paraLines.push(lines[i]);
      i++;
    }
    html.push(`<p>${renderInline(paraLines.join('\n')).replace(/\n/g, '<br />')}</p>`);
  }

  return html.join('\n');
}

function splitTableRow(line) {
  let trimmed = line.trim();
  if (trimmed.startsWith('|')) trimmed = trimmed.slice(1);
  if (trimmed.endsWith('|')) trimmed = trimmed.slice(0, -1);
  return trimmed.split('|').map((c) => c.trim());
}

function sanitizeURL(url) {
  const trimmed = url.trim();
  if (/^(https?:|mailto:|\/|#)/i.test(trimmed)) {
    return trimmed;
  }
  return '#';
}

function renderInline(text) {
  // Protect inline code spans first so markdown symbols inside backticks are not parsed.
  const codeSpans = [];
  let escaped = escapeHTML(text).replace(/`([^`]+)`/g, (_, inner) => {
    const idx = codeSpans.push(`<code>${inner}</code>`) - 1;
    return `\u0000CODE${idx}\u0000`;
  });

  // Links [label](url)
  escaped = escaped.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_, label, href) => {
    const safeHref = sanitizeURL(href);
    return `<a href="${safeHref}" target="_blank" rel="noopener noreferrer">${label}</a>`;
  });

  // Bold + italic, bold, italic, strikethrough
  escaped = escaped
    .replace(/\*\*\*(.+?)\*\*\*/g, '<strong><em>$1</em></strong>')
    .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
    .replace(/__(.+?)__/g, '<strong>$1</strong>')
    .replace(/(^|[^*])\*([^*]+)\*(?!\*)/g, '$1<em>$2</em>')
    .replace(/~~(.+?)~~/g, '<del>$1</del>');

  // Restore protected inline code spans
  return escaped.replace(/\u0000CODE(\d+)\u0000/g, (_, idx) => codeSpans[Number(idx)]);
}
