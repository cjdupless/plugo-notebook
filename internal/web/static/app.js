import { escapeHTML, highlightGo } from '/highlight.js';
import { renderMarkdown } from '/markdown.js';

const state = {
  info: null,
  notebooks: [],
  notebook: null,
  scope: [],
  focusedCellId: null,
  editingMarkdown: new Set(),
  runningCells: new Set(),
  inspectOpen: new Set(),
  inspectedCode: new Map(),
  dirty: false,
  saveTimer: null,
  scopeTimer: null,
  toastTimer: null,
  modalCopyText: '',
};

const els = {
  sidebar: document.getElementById('sidebar'),
  notebookList: document.getElementById('notebook-list'),
  scopeList: document.getElementById('scope-list'),
  scopeCount: document.getElementById('scope-count'),
  cellsContainer: document.getElementById('cells-container'),
  titleInput: document.getElementById('notebook-title'),
  saveStatus: document.getElementById('save-status'),
  goVersionPill: document.getElementById('go-version-pill'),
  modalOverlay: document.getElementById('modal-overlay'),
  modalTitle: document.getElementById('modal-title'),
  modalBody: document.getElementById('modal-body'),
  btnModalCopy: document.getElementById('btn-modal-copy'),
  btnModalDownload: document.getElementById('btn-modal-download'),
  toast: document.getElementById('toast'),
};

// ---------------- API Helpers ----------------

async function api(path, options = {}) {
  const res = await fetch(path, {
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) },
    ...options,
  });
  if (!res.ok) {
    let msg = `Request failed (${res.status})`;
    try {
      const errData = await res.json();
      if (errData.error) msg = errData.error;
    } catch {
      // ignore
    }
    throw new Error(msg);
  }
  const ct = res.headers.get('content-type') || '';
  if (ct.includes('application/json')) return res.json();
  return res.text();
}

function newCellId() {
  const bytes = new Uint8Array(4);
  crypto.getRandomValues(bytes);
  return 'c' + Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

function showToast(msg) {
  els.toast.textContent = msg;
  els.toast.classList.remove('hidden');
  clearTimeout(state.toastTimer);
  state.toastTimer = setTimeout(() => els.toast.classList.add('hidden'), 2400);
}

// ---------------- Theme ----------------

function initTheme() {
  const saved = localStorage.getItem('plugo-theme') || 'dark';
  document.documentElement.setAttribute('data-theme', saved);
}

function toggleTheme() {
  const current = document.documentElement.getAttribute('data-theme') || 'dark';
  const next = current === 'dark' ? 'light' : 'dark';
  document.documentElement.setAttribute('data-theme', next);
  localStorage.setItem('plugo-theme', next);
}

// ---------------- Persistence & Scope ----------------

function setSaveStatus(text, unsaved = false) {
  els.saveStatus.textContent = text;
  els.saveStatus.classList.toggle('unsaved', unsaved);
}

function markDirty() {
  state.dirty = true;
  setSaveStatus('Unsaved', true);
  clearTimeout(state.saveTimer);
  state.saveTimer = setTimeout(() => saveNotebook(true), 900);

  clearTimeout(state.scopeTimer);
  state.scopeTimer = setTimeout(() => refreshScope(), 650);
}

async function saveNotebook(silent = false) {
  if (!state.notebook) return;
  clearTimeout(state.saveTimer);
  setSaveStatus('Saving...', true);
  try {
    const data = await api(`/api/notebooks/${encodeURIComponent(state.notebook.id)}`, {
      method: 'PUT',
      body: JSON.stringify(state.notebook),
    });
    state.notebook.updatedAt = data.notebook.updatedAt;
    state.scope = data.scope || [];
    state.dirty = false;
    setSaveStatus('Saved', false);
    renderScope();
    await loadNotebookList();
    if (!silent) showToast('Notebook saved');
  } catch (err) {
    setSaveStatus('Save error', true);
    showToast(`Save failed: ${err.message}`);
  }
}

async function refreshScope() {
  if (!state.notebook) return;
  try {
    const data = await api('/api/inspect', {
      method: 'POST',
      body: JSON.stringify({ cells: state.notebook.cells }),
    });
    state.scope = data.scope || [];
    renderScope();
  } catch {
    // ignore background scope errors
  }
}

// ---------------- Notebook Loading ----------------

async function loadNotebookList() {
  const data = await api('/api/notebooks');
  state.notebooks = data.notebooks || [];
  renderNotebookList();
}

async function openNotebook(id) {
  if (state.dirty && state.notebook) {
    await saveNotebook(true);
  }
  const data = await api(`/api/notebooks/${encodeURIComponent(id)}`);
  state.notebook = data.notebook;
  state.scope = data.scope || [];
  state.editingMarkdown.clear();
  state.inspectOpen.clear();
  state.inspectedCode.clear();
  state.focusedCellId = state.notebook.cells[0]?.id || null;
  state.dirty = false;

  els.titleInput.value = state.notebook.title;
  document.title = `${state.notebook.title} — Plugo`;
  setSaveStatus('Saved', false);

  renderNotebookList();
  renderScope();
  renderCells();
}

async function createNotebook() {
  const title = window.prompt('New notebook name:', 'Go Playground');
  if (title === null) return;
  try {
    const data = await api('/api/notebooks', {
      method: 'POST',
      body: JSON.stringify({ title: title.trim() || 'Untitled Notebook' }),
    });
    await loadNotebookList();
    await openNotebook(data.notebook.id);
    showToast(`Created "${data.notebook.title}"`);
  } catch (err) {
    showToast(err.message);
  }
}

async function deleteNotebook(id, title) {
  if (!window.confirm(`Delete notebook "${title}"?`)) return;
  try {
    await api(`/api/notebooks/${encodeURIComponent(id)}`, { method: 'DELETE' });
    await loadNotebookList();
    if (state.notebook?.id === id) {
      if (state.notebooks.length > 0) {
        await openNotebook(state.notebooks[0].id);
      } else {
        const created = await api('/api/notebooks', {
          method: 'POST',
          body: JSON.stringify({ title: 'Untitled Notebook' }),
        });
        await loadNotebookList();
        await openNotebook(created.notebook.id);
      }
    }
    showToast('Notebook deleted');
  } catch (err) {
    showToast(err.message);
  }
}

// ---------------- Cell Operations ----------------

function addCell(type, index = null) {
  if (!state.notebook) return;
  const cell = {
    id: newCellId(),
    type,
    source: '',
  };
  if (index === null || index < 0 || index > state.notebook.cells.length) {
    state.notebook.cells.push(cell);
  } else {
    state.notebook.cells.splice(index, 0, cell);
  }
  if (type === 'markdown') {
    state.editingMarkdown.add(cell.id);
  }
  state.focusedCellId = cell.id;
  markDirty();
  renderCells();
  focusCellInput(cell.id);
}

function deleteCell(cellId) {
  if (!state.notebook) return;
  const idx = state.notebook.cells.findIndex((c) => c.id === cellId);
  if (idx < 0) return;
  state.notebook.cells.splice(idx, 1);
  state.editingMarkdown.delete(cellId);
  state.inspectOpen.delete(cellId);
  markDirty();
  renderCells();
}

function moveCell(cellId, delta) {
  if (!state.notebook) return;
  const idx = state.notebook.cells.findIndex((c) => c.id === cellId);
  const nextIdx = idx + delta;
  if (idx < 0 || nextIdx < 0 || nextIdx >= state.notebook.cells.length) return;
  const [cell] = state.notebook.cells.splice(idx, 1);
  state.notebook.cells.splice(nextIdx, 0, cell);
  markDirty();
  renderCells();
}

function duplicateCell(cellId) {
  if (!state.notebook) return;
  const idx = state.notebook.cells.findIndex((c) => c.id === cellId);
  if (idx < 0) return;
  const orig = state.notebook.cells[idx];
  const copy = {
    id: newCellId(),
    type: orig.type,
    source: orig.source,
  };
  state.notebook.cells.splice(idx + 1, 0, copy);
  if (copy.type === 'markdown') state.editingMarkdown.add(copy.id);
  state.focusedCellId = copy.id;
  markDirty();
  renderCells();
  focusCellInput(copy.id);
}

async function runCell(cellId, advance = false) {
  if (!state.notebook) return;
  const idx = state.notebook.cells.findIndex((c) => c.id === cellId);
  if (idx < 0) return;
  const cell = state.notebook.cells[idx];

  if (cell.type === 'markdown') {
    state.editingMarkdown.delete(cell.id);
    renderCells();
    if (advance) advanceFromCell(idx);
    return;
  }

  if (state.runningCells.has(cellId)) return;
  state.runningCells.add(cellId);
  renderCells();

  try {
    const data = await api(
      `/api/notebooks/${encodeURIComponent(state.notebook.id)}/cells/${encodeURIComponent(cellId)}/run`,
      {
        method: 'POST',
        body: JSON.stringify({
          title: state.notebook.title,
          cells: state.notebook.cells,
        }),
      }
    );
    cell.output = data.output;
    cell.execCount = data.execCount;
    if (data.output?.generatedSource) {
      state.inspectedCode.set(cellId, data.output.generatedSource);
    }
    state.scope = data.scope || [];
    state.dirty = false;
    setSaveStatus('Saved', false);
    renderScope();
  } catch (err) {
    cell.output = {
      status: 'error',
      stderr: err.message,
      exitCode: 1,
      durationMs: 0,
    };
  } finally {
    state.runningCells.delete(cellId);
    renderCells();
    if (advance) advanceFromCell(idx);
  }
}

async function stopCell(cellId) {
  if (!state.notebook) return;
  try {
    await api(
      `/api/notebooks/${encodeURIComponent(state.notebook.id)}/cells/${encodeURIComponent(cellId)}/stop`,
      { method: 'POST' }
    );
    showToast('Stopping execution...');
  } catch (err) {
    showToast(err.message);
  }
}

async function runAllCells() {
  if (!state.notebook) return;
  for (const cell of [...state.notebook.cells]) {
    if (cell.type === 'markdown') {
      state.editingMarkdown.delete(cell.id);
    } else {
      await runCell(cell.id, false);
    }
  }
  renderCells();
  showToast('Finished running notebook');
}

async function formatCell(cellId) {
  if (!state.notebook) return;
  const cell = state.notebook.cells.find((c) => c.id === cellId);
  if (!cell || cell.type !== 'code') return;
  try {
    const data = await api('/api/format', {
      method: 'POST',
      body: JSON.stringify({ source: cell.source }),
    });
    cell.source = data.formatted;
    markDirty();
    renderCells();
    showToast('Formatted with gofmt');
  } catch (err) {
    showToast(`gofmt error: ${err.message}`);
  }
}

async function formatAllCells() {
  if (!state.notebook) return;
  let formattedCount = 0;
  for (const cell of state.notebook.cells) {
    if (cell.type !== 'code' || !cell.source.trim()) continue;
    try {
      const data = await api('/api/format', {
        method: 'POST',
        body: JSON.stringify({ source: cell.source }),
      });
      if (data.formatted !== cell.source) {
        cell.source = data.formatted;
        formattedCount++;
      }
    } catch {
      // skip cells with syntax errors
    }
  }
  if (formattedCount > 0) {
    markDirty();
    renderCells();
  }
  showToast(formattedCount ? `Formatted ${formattedCount} Go cell(s)` : 'All Go cells already formatted');
}

async function toggleInspectGo(cellId) {
  if (state.inspectOpen.has(cellId)) {
    state.inspectOpen.delete(cellId);
    renderCells();
    return;
  }
  try {
    const data = await api('/api/inspect', {
      method: 'POST',
      body: JSON.stringify({
        cells: state.notebook.cells,
        targetCellId: cellId,
      }),
    });
    state.inspectedCode.set(cellId, data.generatedSource || '');
    state.inspectOpen.add(cellId);
    renderCells();
  } catch (err) {
    showToast(err.message);
  }
}

function clearAllOutputs() {
  if (!state.notebook) return;
  for (const cell of state.notebook.cells) {
    delete cell.output;
    cell.execCount = 0;
  }
  markDirty();
  renderCells();
  showToast('Cleared all outputs');
}

function advanceFromCell(idx) {
  if (!state.notebook) return;
  if (idx + 1 < state.notebook.cells.length) {
    const next = state.notebook.cells[idx + 1];
    state.focusedCellId = next.id;
    renderCells();
    if (next.type === 'code') focusCellInput(next.id);
    else scrollToCell(next.id);
  } else {
    addCell('code');
  }
}

function focusCellInput(cellId, lineNum = null) {
  requestAnimationFrame(() => {
    const el = document.querySelector(`[data-cell-id="${cellId}"] textarea`);
    if (!el) return;
    el.focus();
    if (lineNum && lineNum > 0) {
      const lines = el.value.split('\n');
      let pos = 0;
      for (let i = 0; i < Math.min(lineNum - 1, lines.length); i++) {
        pos += lines[i].length + 1;
      }
      el.setSelectionRange(pos, pos);
    }
  });
}

function scrollToCell(cellId, lineNum = null) {
  const card = document.querySelector(`[data-cell-id="${cellId}"]`);
  if (!card) return;
  card.scrollIntoView({ behavior: 'smooth', block: 'center' });
  card.classList.add('cell-flash');
  setTimeout(() => card.classList.remove('cell-flash'), 900);
  if (lineNum) focusCellInput(cellId, lineNum);
}

// ---------------- Rendering ----------------

function renderNotebookList() {
  els.notebookList.innerHTML = '';
  for (const nb of state.notebooks) {
    const li = document.createElement('li');
    li.className = 'notebook-item' + (state.notebook?.id === nb.id ? ' active' : '');
    li.innerHTML = `
      <div class="notebook-item-meta">
        <span class="notebook-item-title">${escapeHTML(nb.title)}</span>
        <span class="notebook-item-sub">${nb.cellCount} cell${nb.cellCount === 1 ? '' : 's'}</span>
      </div>
      <button class="notebook-item-del" title="Delete notebook" aria-label="Delete notebook">✕</button>
    `;
    li.addEventListener('click', (e) => {
      if (e.target.closest('.notebook-item-del')) return;
      openNotebook(nb.id);
    });
    li.querySelector('.notebook-item-del').addEventListener('click', (e) => {
      e.stopPropagation();
      deleteNotebook(nb.id, nb.title);
    });
    els.notebookList.appendChild(li);
  }
}

function renderScope() {
  els.scopeCount.textContent = String(state.scope.length);
  els.scopeList.innerHTML = '';
  if (state.scope.length === 0) {
    els.scopeList.innerHTML = `<li class="scope-empty">No shared declarations yet. Define a <code>func</code>, <code>type</code>, <code>const</code>, or <code>var</code> in any Go cell.</li>`;
    return;
  }

  for (const sym of state.scope) {
    const li = document.createElement('li');
    li.className = 'scope-item';
    li.title = sym.signature || sym.name;
    li.innerHTML = `
      <div class="scope-item-top">
        <span class="sym-kind kind-${escapeHTML(sym.kind)}">${escapeHTML(sym.kind)}</span>
        <span class="sym-name">${escapeHTML(sym.name)}</span>
        <span class="sym-loc">#${sym.cellIndex}:${sym.line}</span>
      </div>
      ${sym.signature ? `<span class="sym-sig">${escapeHTML(sym.signature)}</span>` : ''}
    `;
    li.addEventListener('click', () => scrollToCell(sym.cellId, sym.line));
    els.scopeList.appendChild(li);
  }
}

function renderCells() {
  if (!state.notebook) return;
  const activeEl = document.activeElement;
  const activeCellId = activeEl?.closest?.('[data-cell-id]')?.dataset?.cellId || null;
  const selStart = activeEl?.tagName === 'TEXTAREA' ? activeEl.selectionStart : null;
  const selEnd = activeEl?.tagName === 'TEXTAREA' ? activeEl.selectionEnd : null;

  els.cellsContainer.innerHTML = '';

  const cells = state.notebook.cells;
  if (cells.length === 0) {
    const empty = document.createElement('div');
    empty.style.cssText = 'text-align:center;color:var(--text-muted);padding:48px 16px;';
    empty.textContent = 'This notebook has no cells yet. Add a Go or Markdown cell below.';
    els.cellsContainer.appendChild(empty);
    return;
  }

  els.cellsContainer.appendChild(createInsertDivider(0));
  cells.forEach((cell, idx) => {
    els.cellsContainer.appendChild(createCellElement(cell, idx));
    els.cellsContainer.appendChild(createInsertDivider(idx + 1));
  });

  if (activeCellId && selStart !== null) {
    const nextTa = document.querySelector(`[data-cell-id="${activeCellId}"] textarea`);
    if (nextTa) {
      nextTa.focus();
      nextTa.setSelectionRange(selStart, selEnd);
    }
  }
}

function createInsertDivider(insertIndex) {
  const div = document.createElement('div');
  div.className = 'insert-divider';
  div.innerHTML = `
    <div class="insert-divider-btns">
      <button class="insert-pill" data-type="code">+ Go</button>
      <button class="insert-pill" data-type="markdown">+ Markdown</button>
    </div>
  `;
  div.querySelectorAll('.insert-pill').forEach((btn) => {
    btn.addEventListener('click', () => addCell(btn.dataset.type, insertIndex));
  });
  return div;
}

function createCellElement(cell, idx) {
  const isCode = cell.type === 'code';
  const isRunning = state.runningCells.has(cell.id);
  const isEditingMd = !isCode && (state.editingMarkdown.has(cell.id) || !cell.source.trim());
  const out = cell.output;

  // Map diagnostic error lines belonging to THIS cell (from this cell's output or others).
  const errorLines = new Set();
  if (out?.diagnostics) {
    for (const d of out.diagnostics) {
      if (d.cellId === cell.id && d.line > 0) errorLines.add(d.line);
    }
  }

  const card = document.createElement('article');
  card.dataset.cellId = cell.id;
  card.className = [
    'cell',
    isCode ? 'cell-code' : 'cell-markdown',
    state.focusedCellId === cell.id ? 'cell-focused' : '',
    isRunning ? 'cell-running' : '',
    out && out.status !== 'ok' ? 'cell-error' : '',
  ].filter(Boolean).join(' ');

  card.addEventListener('mousedown', () => {
    if (state.focusedCellId !== cell.id) {
      state.focusedCellId = cell.id;
      document.querySelectorAll('.cell-focused').forEach((el) => el.classList.remove('cell-focused'));
      card.classList.add('cell-focused');
    }
  });

  // ---- Header ----
  const header = document.createElement('div');
  header.className = 'cell-header';

  let timingHTML = '';
  if (isRunning) {
    timingHTML = `<span class="cell-timing running">compiling &amp; running...</span>`;
  } else if (isCode && out) {
    const cls = out.status === 'ok' ? 'ok' : 'error';
    const label = out.status === 'ok' ? `${out.durationMs}ms` : `${out.status} · ${out.durationMs}ms`;
    timingHTML = `<span class="cell-timing ${cls}">${escapeHTML(label)}</span>`;
  }

  header.innerHTML = `
    <div class="cell-header-left">
      <span class="cell-badge">${isCode ? 'Go' : 'MD'}</span>
      <span class="cell-index-label">Cell #${idx + 1}</span>
      ${isCode && cell.execCount ? `<span class="cell-exec-count">[${cell.execCount}]</span>` : ''}
      ${timingHTML}
    </div>
    <div class="cell-header-right">
      ${isCode
        ? (isRunning
            ? `<button class="cell-action stop-btn" data-act="stop" title="Stop execution">■ Stop</button>`
            : `<button class="cell-action run-btn" data-act="run" title="Run Go cell (Shift+Enter)">▶ Run</button>`)
        : (isEditingMd
            ? `<button class="cell-action run-btn" data-act="render-md" title="Render Markdown (Shift+Enter)">✓ Render</button>`
            : `<button class="cell-action" data-act="edit-md" title="Edit Markdown">✎ Edit</button>`)}
      ${isCode ? `<button class="cell-action" data-act="format" title="Format with gofmt (Alt+Shift+F)">gofmt</button>` : ''}
      ${isCode ? `<button class="cell-action" data-act="inspect" title="Inspect generated package main Go code">{ } Go</button>` : ''}
      <button class="cell-action" data-act="up" title="Move cell up" ${idx === 0 ? 'disabled' : ''}>↑</button>
      <button class="cell-action" data-act="down" title="Move cell down" ${idx === state.notebook.cells.length - 1 ? 'disabled' : ''}>↓</button>
      <button class="cell-action" data-act="dup" title="Duplicate cell">⧉</button>
      <button class="cell-action danger" data-act="delete" title="Delete cell">✕</button>
    </div>
  `;

  header.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-act]');
    if (!btn) return;
    const act = btn.dataset.act;
    if (act === 'run') runCell(cell.id, false);
    else if (act === 'stop') stopCell(cell.id);
    else if (act === 'format') formatCell(cell.id);
    else if (act === 'inspect') toggleInspectGo(cell.id);
    else if (act === 'render-md') { state.editingMarkdown.delete(cell.id); renderCells(); }
    else if (act === 'edit-md') { state.editingMarkdown.add(cell.id); renderCells(); focusCellInput(cell.id); }
    else if (act === 'up') moveCell(cell.id, -1);
    else if (act === 'down') moveCell(cell.id, 1);
    else if (act === 'dup') duplicateCell(cell.id);
    else if (act === 'delete') deleteCell(cell.id);
  });

  card.appendChild(header);

  // ---- Body ----
  if (isCode) {
    card.appendChild(createCodeEditor(cell, errorLines));
    if (state.inspectOpen.has(cell.id)) {
      card.appendChild(createGeneratedDrawer(cell.id));
    }
    if (out) {
      card.appendChild(createOutputPanel(cell));
    }
  } else if (isEditingMd) {
    card.appendChild(createMarkdownEditor(cell));
  } else {
    const rendered = document.createElement('div');
    rendered.className = 'md-rendered';
    rendered.innerHTML = renderMarkdown(cell.source);
    rendered.title = 'Double-click to edit Markdown';
    rendered.addEventListener('dblclick', () => {
      state.editingMarkdown.add(cell.id);
      renderCells();
      focusCellInput(cell.id);
    });
    card.appendChild(rendered);
  }

  return card;
}

function createCodeEditor(cell, errorLines) {
  const wrap = document.createElement('div');
  wrap.className = 'code-editor-wrap';

  const gutter = document.createElement('div');
  gutter.className = 'code-gutter';

  const stage = document.createElement('div');
  stage.className = 'code-stage';

  const pre = document.createElement('pre');
  pre.className = 'code-highlight';
  pre.setAttribute('aria-hidden', 'true');

  const ta = document.createElement('textarea');
  ta.className = 'code-textarea';
  ta.spellcheck = false;
  ta.autocomplete = 'off';
  ta.autocapitalize = 'off';
  ta.placeholder = '// Write Go code here (Shift+Enter to run)...';
  ta.value = cell.source;

  const sync = () => {
    pre.innerHTML = highlightGo(ta.value);
    const lineCount = Math.max(1, ta.value.split('\n').length);
    let gutterHTML = '';
    for (let ln = 1; ln <= lineCount; ln++) {
      gutterHTML += `<span class="gutter-line${errorLines.has(ln) ? ' err-line' : ''}">${ln}</span>`;
    }
    gutter.innerHTML = gutterHTML;
    ta.style.height = 'auto';
    const nextHeight = Math.max(68, pre.scrollHeight);
    ta.style.height = `${nextHeight}px`;
    pre.style.minHeight = `${nextHeight}px`;
  };

  ta.addEventListener('input', () => {
    cell.source = ta.value;
    sync();
    markDirty();
  });

  ta.addEventListener('scroll', () => {
    pre.scrollLeft = ta.scrollLeft;
  });

  ta.addEventListener('keydown', (e) => {
    // Shift+Enter: run & advance
    if (e.key === 'Enter' && e.shiftKey) {
      e.preventDefault();
      runCell(cell.id, true);
      return;
    }
    // Ctrl/Cmd+Enter: run in place
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      runCell(cell.id, false);
      return;
    }
    // Alt+Shift+F: gofmt
    if ((e.key === 'f' || e.key === 'F') && e.altKey && e.shiftKey) {
      e.preventDefault();
      formatCell(cell.id);
      return;
    }
    // Tab / Shift+Tab indentation (Go idiomatic tabs)
    if (e.key === 'Tab') {
      e.preventDefault();
      const start = ta.selectionStart;
      const end = ta.selectionEnd;
      if (e.shiftKey) {
        const before = ta.value.slice(0, start);
        const lineStart = before.lastIndexOf('\n') + 1;
        if (ta.value[lineStart] === '\t') {
          ta.value = ta.value.slice(0, lineStart) + ta.value.slice(lineStart + 1);
          ta.setSelectionRange(Math.max(lineStart, start - 1), Math.max(lineStart, end - 1));
        }
      } else {
        ta.value = ta.value.slice(0, start) + '\t' + ta.value.slice(end);
        ta.setSelectionRange(start + 1, start + 1);
      }
      cell.source = ta.value;
      sync();
      markDirty();
      return;
    }
    // Smart auto-indent on Enter
    if (e.key === 'Enter' && !e.shiftKey && !e.ctrlKey && !e.metaKey) {
      const start = ta.selectionStart;
      const before = ta.value.slice(0, start);
      const lineStart = before.lastIndexOf('\n') + 1;
      const currentLine = before.slice(lineStart);
      const indentMatch = currentLine.match(/^[\t ]*/);
      let indent = indentMatch ? indentMatch[0] : '';
      if (currentLine.trimEnd().endsWith('{')) {
        indent += '\t';
      }
      if (indent) {
        e.preventDefault();
        const end = ta.selectionEnd;
        ta.value = ta.value.slice(0, start) + '\n' + indent + ta.value.slice(end);
        const pos = start + 1 + indent.length;
        ta.setSelectionRange(pos, pos);
        cell.source = ta.value;
        sync();
        markDirty();
      }
    }
  });

  stage.appendChild(pre);
  stage.appendChild(ta);
  wrap.appendChild(gutter);
  wrap.appendChild(stage);

  requestAnimationFrame(sync);
  return wrap;
}

function createMarkdownEditor(cell) {
  const wrap = document.createElement('div');
  wrap.className = 'md-editor-wrap';

  const ta = document.createElement('textarea');
  ta.className = 'md-textarea';
  ta.placeholder = 'Write Markdown here... (Shift+Enter to render)';
  ta.value = cell.source;

  const autoResize = () => {
    ta.style.height = 'auto';
    ta.style.height = `${Math.max(96, ta.scrollHeight)}px`;
  };

  ta.addEventListener('input', () => {
    cell.source = ta.value;
    autoResize();
    markDirty();
  });

  ta.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.shiftKey || e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      runCell(cell.id, e.shiftKey);
    } else if (e.key === 'Escape' && cell.source.trim()) {
      state.editingMarkdown.delete(cell.id);
      renderCells();
    }
  });

  wrap.appendChild(ta);
  requestAnimationFrame(autoResize);
  return wrap;
}

function createGeneratedDrawer(cellId) {
  const code = state.inspectedCode.get(cellId) || '';
  const drawer = document.createElement('div');
  drawer.className = 'generated-drawer';
  drawer.innerHTML = `
    <div class="generated-header">
      <span>Generated Go Compilation Unit (<code>package main</code>)</span>
      <button class="btn-mini" data-copy>Copy Go</button>
    </div>
    <pre class="generated-code">${highlightGo(code)}</pre>
  `;
  drawer.querySelector('[data-copy]').addEventListener('click', () => {
    navigator.clipboard?.writeText(code);
    showToast('Copied generated Go code');
  });
  return drawer;
}

function createOutputPanel(cell) {
  const out = cell.output;
  const panel = document.createElement('div');
  panel.className = 'cell-output';

  // Meta bar (auto-imports + clear button)
  const meta = document.createElement('div');
  meta.className = 'output-meta';
  const importsHTML = out.autoImports?.length
    ? `<span class="auto-imports-pill" title="Automatically imported Go stdlib packages">auto-imports: ${escapeHTML(out.autoImports.join(', '))}</span>`
    : '';
  meta.innerHTML = `
    <div class="output-meta-left">
      <span>${out.status === 'ok' ? 'Output' : (out.compileError ? 'Compile Error' : `Exit ${out.exitCode}`)}</span>
      ${importsHTML}
    </div>
    <button class="cell-action" data-clear title="Clear output">Clear</button>
  `;
  meta.querySelector('[data-clear]').addEventListener('click', () => {
    delete cell.output;
    markDirty();
    renderCells();
  });
  panel.appendChild(meta);

  // Clickable diagnostics chips
  if (out.diagnostics?.length) {
    const diagBar = document.createElement('div');
    diagBar.className = 'diagnostics-list';
    for (const d of out.diagnostics) {
      const chip = document.createElement('button');
      chip.className = 'diag-chip';
      chip.textContent = `Cell #${d.cellIndex}, Line ${d.line}: ${d.message}`;
      chip.addEventListener('click', () => scrollToCell(d.cellId, d.line));
      diagBar.appendChild(chip);
    }
    panel.appendChild(diagBar);
  }

  if (out.stdout) {
    const pre = document.createElement('pre');
    pre.className = 'output-stdout';
    pre.textContent = out.stdout;
    panel.appendChild(pre);
  }

  if (out.stderr) {
    const pre = document.createElement('pre');
    pre.className = 'output-stderr';
    pre.textContent = out.stderr;
    panel.appendChild(pre);
  }

  if (!out.stdout && !out.stderr && out.status === 'ok') {
    const okMsg = document.createElement('div');
    okMsg.className = 'output-empty-ok';
    okMsg.textContent = '✓ Compiled & ran cleanly (no stdout)';
    panel.appendChild(okMsg);
  }

  return panel;
}

// ---------------- Modals ----------------

function openModal(title, bodyHTML, copyText = '', downloadHref = '') {
  els.modalTitle.textContent = title;
  els.modalBody.innerHTML = bodyHTML;
  state.modalCopyText = copyText;
  els.btnModalCopy.classList.toggle('hidden', !copyText);
  if (downloadHref) {
    els.btnModalDownload.href = downloadHref;
    els.btnModalDownload.classList.remove('hidden');
  } else {
    els.btnModalDownload.classList.add('hidden');
  }
  els.modalOverlay.classList.remove('hidden');
}

function closeModal() {
  els.modalOverlay.classList.add('hidden');
}

async function openExportModal() {
  if (!state.notebook) return;
  if (state.dirty) await saveNotebook(true);
  try {
    const id = encodeURIComponent(state.notebook.id);
    const code = await api(`/api/notebooks/${id}/export.go`);
    openModal(
      `Exported Go Source — ${state.notebook.id}.go`,
      `<pre class="modal-code">${highlightGo(code)}</pre>`,
      code,
      `/api/notebooks/${id}/export.go?download=1`
    );
  } catch (err) {
    showToast(`Export failed: ${err.message}`);
  }
}

function openShortcutsModal() {
  openModal(
    'Keyboard Shortcuts',
    `<table class="shortcuts-table">
      <tr><td><kbd>Shift</kbd> + <kbd>Enter</kbd></td><td>Run Go cell or render Markdown, then move to next cell</td></tr>
      <tr><td><kbd>Ctrl</kbd> / <kbd>⌘</kbd> + <kbd>Enter</kbd></td><td>Run Go cell or render Markdown in place</td></tr>
      <tr><td><kbd>Alt</kbd> + <kbd>Shift</kbd> + <kbd>F</kbd></td><td>Format active Go cell with <code>gofmt</code></td></tr>
      <tr><td><kbd>Ctrl</kbd> / <kbd>⌘</kbd> + <kbd>S</kbd></td><td>Save notebook immediately</td></tr>
      <tr><td><kbd>Tab</kbd> / <kbd>Shift+Tab</kbd></td><td>Insert or remove Go tab indentation</td></tr>
      <tr><td>Double-click Markdown</td><td>Switch rendered Markdown cell back to edit mode</td></tr>
    </table>`
  );
}

// ---------------- Init & Event Wiring ----------------

function bindGlobalEvents() {
  document.getElementById('btn-toggle-sidebar').addEventListener('click', () => {
    els.sidebar.classList.toggle('collapsed');
  });
  document.getElementById('btn-theme').addEventListener('click', toggleTheme);
  document.getElementById('btn-new-notebook').addEventListener('click', createNotebook);
  document.getElementById('btn-run-all').addEventListener('click', runAllCells);
  document.getElementById('btn-add-code').addEventListener('click', () => addCell('code'));
  document.getElementById('btn-add-md').addEventListener('click', () => addCell('markdown'));
  document.getElementById('btn-bottom-code').addEventListener('click', () => addCell('code'));
  document.getElementById('btn-bottom-md').addEventListener('click', () => addCell('markdown'));
  document.getElementById('btn-format-all').addEventListener('click', formatAllCells);
  document.getElementById('btn-export-go').addEventListener('click', openExportModal);
  document.getElementById('btn-clear-outputs').addEventListener('click', clearAllOutputs);
  document.getElementById('btn-shortcuts').addEventListener('click', openShortcutsModal);

  document.getElementById('btn-modal-close').addEventListener('click', closeModal);
  els.modalOverlay.addEventListener('click', (e) => {
    if (e.target === els.modalOverlay) closeModal();
  });
  els.btnModalCopy.addEventListener('click', () => {
    navigator.clipboard?.writeText(state.modalCopyText);
    showToast('Copied to clipboard');
  });

  els.titleInput.addEventListener('input', () => {
    if (!state.notebook) return;
    state.notebook.title = els.titleInput.value;
    document.title = `${state.notebook.title || 'Plugo'} — Plugo`;
    markDirty();
  });

  window.addEventListener('keydown', (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
      e.preventDefault();
      saveNotebook(false);
    } else if (e.key === 'Escape' && !els.modalOverlay.classList.contains('hidden')) {
      closeModal();
    }
  });
}

async function boot() {
  initTheme();
  bindGlobalEvents();
  try {
    state.info = await api('/api/info');
    els.goVersionPill.textContent = state.info.goVersion || 'go';
    await loadNotebookList();
    if (state.notebooks.length > 0) {
      await openNotebook(state.notebooks[0].id);
    } else {
      const created = await api('/api/notebooks', {
        method: 'POST',
        body: JSON.stringify({ title: 'Welcome to Plugo' }),
      });
      await loadNotebookList();
      await openNotebook(created.notebook.id);
    }
  } catch (err) {
    showToast(`Failed to start Plugo UI: ${err.message}`);
  }
}

boot();
