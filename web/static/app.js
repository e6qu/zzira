// ZZIRA page glue: owns the sync worker, feeds it the current view, swaps
// locally-rendered HTML into the DOM, and queues commands in the outbox when
// offline (server-wins reconciliation on reconnect).
(function () {
  'use strict';
  if (!('Worker' in window)) return;

  const worker = new Worker('/static/worker.js');
  const banner = () => document.getElementById('sync-banner');

  function currentIssueId() {
    return document.body.getAttribute('data-current-issue') || '';
  }

  function currentIssueKey() {
    const el = document.querySelector('.issue-view');
    return el ? el.getAttribute('data-issue-key') : '';
  }

  window.__bannerLog = [];
  function announce(text, ms) {
    window.__bannerLog.push(text);
    const el = banner();
    if (!el) return;
    el.textContent = text;
    el.hidden = false;
    if (ms) setTimeout(() => { el.hidden = true; }, ms);
  }

  worker.onmessage = (e) => {
    const msg = e.data || {};
    switch (msg.type) {
      case 'ready':
        announce('local sync ready (renderer ' + (msg.renderer || '?') + ')', 2500);
        pushView();
        break;
      case 'synced':
        sawSync = true;
        announce('synced \u00b7 seq ' + msg.seq, 1500);
        refreshBoardRegion();
        break;
      case 'queued':
        announce('offline \u2014 queued (' + msg.size + ')', 4000);
        break;
      case 'offline':
        announce('offline \u2014 showing local copy', 4000);
        break;
      case 'html':
        // Skip the worker's redundant initial render: the server already
        // SSR-rendered this view. Offline reloads have an empty root (cached
        // shell) and MUST apply.
        if (!sawSync && document.querySelector('#issue-root .issue-view')) break;
        if (msg.issueId && msg.issueId === currentIssueId()) {
          applyRootHtml(msg.html);
        }
        break;
      case 'info':
        announce('worker: ' + (msg.message || ''), 4000);
        break;
      case 'dialog-html':
        {
          const root = document.getElementById('modal-root');
          if (root) { root.innerHTML = msg.html; root.style.display = 'block'; initRichEditors(root); }
        }
        break;
      case 'error':
        announce('sync error: ' + (msg.message || 'unknown'), 5000);
        break;
    }
  };

  let pendingRootHtml = null;
  let sawSync = false;

  // applyRootHtml swaps in a re-render unless the user is typing inside the
  // issue view; in that case it is deferred until focus leaves (no clobbered
  // edits, no lost renders).
  function seqOf(html) {
    const m = /data-seq="(\d+)"/.exec(html);
    return m ? Number(m[1]) : 0;
  }
  function seqOfDom() {
    const el = document.querySelector('#issue-root');
    return el ? Number(el.getAttribute('data-seq') || 0) : 0;
  }
  function applyRootHtml(html) {
    const root = document.getElementById('issue-root');
    if (!root) return;
    const active = document.activeElement;
    const editing = active && root.contains(active) &&
      (active.closest('[contenteditable]') ||
       ['INPUT', 'TEXTAREA', 'SELECT'].includes(active.tagName));
    // Also defer if the create/edit dialog is open (the user is mid-edit)
    const modalOpen = root.style.display === 'block' ||
      (document.getElementById('modal-root') && document.getElementById('modal-root').style.display === 'block');
    if (editing || modalOpen) { pendingRootHtml = html; return; }
    if (root.outerHTML === html) { pendingRootHtml = null; return; } // no-op render
    const incomingSeq = seqOf(html);
    if (incomingSeq > 0 && incomingSeq < seqOfDom()) {
      pendingRootHtml = null;
      return; // stale replica render: the DOM already has newer data
    }
    root.outerHTML = html;
    pendingRootHtml = null;
    pushView();
  }

  document.addEventListener('focusout', () => {
    if (!pendingRootHtml) return;
    const active = document.activeElement;
    const root = document.getElementById('issue-root');
    if (root && (!active || !root.contains(active) ||
        (!active.closest('[contenteditable]') && !['INPUT', 'TEXTAREA', 'SELECT'].includes(active.tagName)))) {
      root.outerHTML = pendingRootHtml;
      pendingRootHtml = null;
      pushView();
    }
  });

  // Live board: after each sync, refresh the region so other users' rank and
  // transition actions appear without a reload.
  function refreshBoardRegion() {
    const boardID = document.body.getAttribute('data-board');
    if (!boardID) return;
    fetch(`/board/${boardID}/fragment`).then(r => {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      return r.text();
    }).then(html => {
      const board = document.getElementById('board');
      if (board) {
        board.outerHTML = html;
        initBoard(document);
      }
    }).catch(() => { /* offline: the local replica already ordered the board */ });
  }

  function pushView() {
    if (currentIssueId()) worker.postMessage({ type: 'view', issueId: currentIssueId() });
  }
  document.addEventListener('DOMContentLoaded', pushView);
  document.body.addEventListener('htmx:afterSettle', pushView);

  // ---- Outbox: intercept command forms when offline ----
  const KIND_BY_PATH = [
    [/\/comments$/, 'comment'],
    [/\/transition$/, 'transition'],
    [/\/edit$/, 'edit'],
  ];

  function outboxKind(path) {
    for (const [re, kind] of KIND_BY_PATH) {
      if (re.test(path)) return kind;
    }
    return null;
  }

  document.body.addEventListener('htmx:beforeRequest', (evt) => {
    const elt = evt.detail.elt;
    if (!elt || elt.tagName !== 'FORM') return;
    if (navigator.onLine) return; // online: let htmx do its thing
    const path = elt.getAttribute('hx-post') || elt.getAttribute('hx-delete');
    const kind = outboxKind(path || '');
    if (!kind) return; // reads fail naturally offline
    evt.preventDefault();
    evt.stopPropagation();
    const body = new URLSearchParams(new FormData(elt)).toString();
    worker.postMessage({ type: 'enqueue', method: 'POST', path, body, kind });
  }, true);

  window.addEventListener('online', () => announce('back online \u2014 syncing\u2026', 2000));

  // ---- Modal helpers ----
  window.zzira = {
    openModal() {
      const root = document.getElementById('modal-root');
      if (root) root.style.display = 'block';
    },
    closeModal() {
      const root = document.getElementById('modal-root');
      if (root) { root.style.display = 'none'; root.innerHTML = ''; }
    },
    openEdit(key) {
      const root = document.getElementById('modal-root');
      if (!root) return;
      // Network first; on network failure the worker renders the dialog from
      // the local replica (designed offline path — the failure is announced).
      fetch(`/issues/${key}/edit`).then(r => {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.text();
      }).then(html => {
        root.innerHTML = html;
        root.style.display = 'block';
        window.zzira.initRichEditors(root);
      }).catch(err => {
        announce('offline — local editor', 3000);
        worker.postMessage({ type: 'edit-dialog', issueId: currentIssueId() });
        root.style.display = 'block';
      });
    }
  };

  // ---- Rich editor: contenteditable + DOM → ADF ----
  function adfText(text, marks) {
    if (!text) return null;
    const node = { type: 'text', text };
    if (marks && marks.length) node.marks = marks;
    return node;
  }

  function marksFromNode(el, inherited) {
    const marks = (inherited || []).slice();
    const tag = el.tagName;
    if (tag === 'STRONG' || tag === 'B') marks.push({ type: 'strong' });
    if (tag === 'EM' || tag === 'I') marks.push({ type: 'em' });
    if (tag === 'U') marks.push({ type: 'underline' });
    if (tag === 'S' || tag === 'STRIKE' || tag === 'DEL') marks.push({ type: 'strike' });
    if (tag === 'CODE') marks.push({ type: 'code' });
    if (tag === 'A') marks.push({ type: 'link', attrs: { href: el.getAttribute('href') || '#' } });
    return marks;
  }

  function serializeInline(nodes, marks, out) {
    nodes.forEach((n) => {
      if (n.nodeType === Node.TEXT_NODE) {
        const t = adfText(n.textContent, marks);
        if (t) out.push(t);
      } else if (n.nodeType === Node.ELEMENT_NODE) {
        if (n.tagName === 'BR') { out.push({ type: 'hardBreak' }); return; }
        serializeInline(n.childNodes, marksFromNode(n, marks), out);
      }
    });
  }

  function serializeBlock(el) {
    const tag = el.tagName;
    if (/^H[1-6]$/.test(tag)) {
      const content = [];
      serializeInline(el.childNodes, [], content);
      return { type: 'heading', attrs: { level: Number(tag[1]) }, content };
    }
    if (tag === 'UL' || tag === 'OL') {
      const items = [];
      el.querySelectorAll(':scope > li').forEach((li) => {
        const content = [];
        // nested lists become sibling blocks inside the list item
        const nested = [];
        li.childNodes.forEach((c) => {
          if (c.nodeType === Node.ELEMENT_NODE && (c.tagName === 'UL' || c.tagName === 'OL')) {
            nested.push(serializeBlock(c));
          } else {
            serializeInline([c], [], content);
          }
        });
        items.push({ type: 'listItem', content: [{ type: 'paragraph', content }, ...nested] });
      });
      return { type: tag === 'UL' ? 'bulletList' : 'orderedList', content: items };
    }
    if (tag === 'PRE') {
      return { type: 'codeBlock', content: [{ type: 'text', text: el.textContent }] };
    }
    if (tag === 'BLOCKQUOTE') {
      const content = [];
      el.querySelectorAll(':scope > p, :scope > div').forEach((p) => content.push(serializeBlock(p)));
      return { type: 'blockquote', content: content.length ? content : [{ type: 'paragraph' }] };
    }
    const content = [];
    serializeInline(el.childNodes, [], content);
    return { type: 'paragraph', content };
  }

  function domToADF(root) {
    const blocks = [];
    root.querySelectorAll(':scope > *').forEach((el) => {
      if (el.tagName === 'DIV' && !el.querySelector('p,div,ul,ol,h1,h2,h3,h4,h5,h6,pre,blockquote')) {
        // contenteditable often produces bare divs — treat as paragraphs
        const content = [];
        serializeInline(el.childNodes, [], content);
        blocks.push({ type: 'paragraph', content });
      } else {
        blocks.push(serializeBlock(el));
      }
    });
    if (!blocks.length) blocks.push({ type: 'paragraph' });
    return { type: 'doc', version: 1, content: blocks };
  }

  function initRichEditors(scope) {
    (scope || document).querySelectorAll('[data-rich-editor]').forEach((editor) => {
      if (editor.dataset.ready) return;
      editor.dataset.ready = '1';
      const form = editor.closest('form');
      // toolbar
      const toolbar = form && form.querySelector('[data-editor-toolbar]');
      if (toolbar) {
        toolbar.querySelectorAll('button[data-cmd]').forEach((btn) => {
          btn.addEventListener('mousedown', (e) => e.preventDefault()); // keep selection
          btn.addEventListener('click', () => {
            editor.focus();
            document.execCommand(btn.dataset.cmd, false, null);
          });
        });
      }
      // paste normalization: plain text only
      editor.addEventListener('paste', (e) => {
        e.preventDefault();
        const text = (e.clipboardData || window.clipboardData).getData('text/plain');
        document.execCommand('insertText', false, text);
      });
      // live-sync: htmx snapshots parameters at trigger time, so the hidden
      // input must be current on every keystroke
      if (form) {
        const sync = () => {
          const hidden = form.querySelector('input[name=adf]');
          if (hidden) hidden.value = JSON.stringify(domToADF(editor));
        };
        editor.addEventListener('input', sync);
        sync();
      }
    });
  }
  window.zzira.initRichEditors = initRichEditors;
  document.addEventListener('DOMContentLoaded', () => initRichEditors(document));
  document.body.addEventListener('htmx:afterSettle', () => initRichEditors(document));

  // After a successful form POST the server has committed: pull the new
  // actions immediately so the replica can't clobber the fresh fragment.
  document.body.addEventListener('htmx:afterRequest', (evt) => {
    const elt = evt.detail.elt;
    if (evt.detail.successful && elt && elt.tagName === 'FORM') {
      worker.postMessage({ type: 'sync-now' });
      const path = elt.getAttribute('hx-post') || '';
      if (path.includes('/edit')) zzira.closeModal();
    }
  });

  // ---- Board drag & drop: rank (+ transition) commands ----
  function initBoard(scope) {
    const board = (scope || document).getElementById('board');
    if (!board || board.dataset.ready) return;
    board.dataset.ready = '1';
    const boardID = document.body.getAttribute('data-board') || '';

    board.querySelectorAll('[data-column]').forEach((col) => {
      new Sortable(col, {
        group: 'board',
        animation: 150,
        onEnd: (evt) => {
          const card = evt.item;
          const issue = card.getAttribute('data-issue');
          const status = card.closest('[data-column]').closest('.board-column')
            .getAttribute('data-status');
          const siblings = Array.from(card.parentElement.children).filter((c) => c !== card);
          const idx = siblings.indexOf(card);
          const after = idx > 0 ? siblings[idx - 1].getAttribute('data-issue') : '';
          const before = idx < siblings.length - 1 ? siblings[idx + 1].getAttribute('data-issue') : '';
          const body = new URLSearchParams({ issue, status, before, after }).toString();
          sendOrQueue('POST', '/board/' + boardID + '/rank', body, 'rank');
        },
      });
    });
  }
  window.zzira.initBoard = initBoard;
  document.addEventListener('DOMContentLoaded', () => initBoard(document));
  document.body.addEventListener('htmx:afterSettle', () => initBoard(document));

  function sendOrQueue(method, path, body, kind) {
    if (navigator.onLine) {
      fetch(path, {
        method,
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body,
      }).then((res) => {
        if (!res.ok) announce('command failed: ' + res.status, 4000);
      }).catch(() => {
        worker.postMessage({ type: 'enqueue', method, path, body, kind });
        announce('offline \u2014 queued', 3000);
      });
      return;
    }
    worker.postMessage({ type: 'enqueue', method, path, body, kind });
  }

  // ---- Service worker for offline page shells ----
  if ('serviceWorker' in navigator) {
    navigator.serviceWorker.register('/sw.js').catch((err) => console.error('sw registration failed:', err));
  }
})();
