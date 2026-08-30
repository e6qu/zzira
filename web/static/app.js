// ZZIRA page glue: owns the sync worker, feeds it the current view, swaps
// locally-rendered HTML into the DOM, and queues commands in the outbox when
// offline (server-wins reconciliation on reconnect).
(function () {
  'use strict';
  if (!('Worker' in window)) return;

  const worker = new Worker('/static/worker.js?v=4');
  const banner = () => document.getElementById('sync-banner');
  let workerReady = false;
  const pendingWorkerMessages = [];

  // A page can be used before the SQLite/WASM worker finishes booting. Keep
  // commands until it advertises readiness instead of dropping the first
  // view or an offline edit on the floor.
  function postWorker(message) {
    if (!workerReady) {
      pendingWorkerMessages.push(message);
      return;
    }
    worker.postMessage(message);
  }

  // HTML returned by the Go renderer is inserted outside HTMX's own swap
  // machinery. Hydrate it explicitly so forms in replica renders and fetched
  // dialogs keep their hx-* behavior (including the offline outbox hook).
  function hydrate(scope) {
    if (!scope) return;
    if (window.htmx) window.htmx.process(scope);
    initRichEditors(scope);
    initBoard(scope);
  }

  function setModalHTML(html) {
    const root = document.getElementById('modal-root');
    if (!root) return;
    root.innerHTML = html;
    root.style.display = 'block';
    hydrate(root);
  }

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
        workerReady = true;
        while (pendingWorkerMessages.length) worker.postMessage(pendingWorkerMessages.shift());
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
        setModalHTML(msg.html);
        break;
      case 'error':
        announce('sync error: ' + (msg.message || 'unknown'), 5000);
        break;
    }
  };

  let pendingRootHtml = null;
  let sawSync = false;
  let offlineMode = !navigator.onLine;

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
    hydrate(document.getElementById('issue-root'));
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
      hydrate(document.getElementById('issue-root'));
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
    const issue = document.querySelector('.issue-view');
    if (!issue) return;
    const issueId = issue.getAttribute('data-issue-id');
    if (!issueId) return;
    // Seed the worker with the SSR issue before its first network sync. This
    // is the explicit hand-off that makes an immediate offline edit possible.
    postWorker({
      type: 'seed-view',
      issue: {
        id: issueId,
        key: issue.getAttribute('data-issue-key') || '',
        summary: (issue.querySelector('.issue-summary') || {}).textContent || '',
        description: (issue.querySelector('.issue-description') || {}).textContent || '',
      },
    });
    postWorker({ type: 'view', issueId });
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

  function queueOfflineForm(form) {
    const path = form.getAttribute('hx-post') || form.getAttribute('hx-delete');
    const kind = outboxKind(path || '');
    if (!kind) return false;
    const body = new URLSearchParams(new FormData(form)).toString();
    postWorker({ type: 'enqueue', method: 'POST', path, body, kind });
    return true;
  }

  // Catch native submits as well as HTMX requests. This is important during
  // first-load offline transitions, when a dialog can exist before HTMX has
  // had a chance to process its dynamically inserted form.
  document.body.addEventListener('submit', (evt) => {
    const form = evt.target;
    if (!(form instanceof HTMLFormElement) || (!offlineMode && navigator.onLine)) return;
    if (!queueOfflineForm(form)) return;
    evt.preventDefault();
    evt.stopPropagation();
  }, true);

  document.body.addEventListener('htmx:beforeRequest', (evt) => {
    const elt = evt.detail.elt;
    if (!elt || elt.tagName !== 'FORM') return;
    if (!offlineMode && navigator.onLine) return; // online: let htmx do its thing
    if (!queueOfflineForm(elt)) return; // reads fail naturally offline
    evt.preventDefault();
    evt.stopPropagation();
  }, true);

  window.addEventListener('online', () => {
    offlineMode = false;
    announce('back online \u2014 syncing\u2026', 2000);
    // Reconnection is a protocol event, not an opportunity to wait for the
    // next maintenance tick. This message follows buffered offline commands.
    postWorker({ type: 'sync-now' });
  });
  window.addEventListener('offline', () => { offlineMode = true; });

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
      // Offline editing is a declared capability of the SSR-to-replica
      // hand-off: the current document carries the command schema and values
      // that were current when it was rendered. It does not depend on either
      // a network failure or the worker's boot time.
      if (!navigator.onLine) {
        const command = document.getElementById('offline-edit-dialog');
        if (!command) {
          announce('offline editing is unavailable for this view', 4000);
          return;
        }
        setModalHTML(command.innerHTML);
        return;
      }
      fetch(`/issues/${key}/edit`).then(r => {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.text();
      }).then(html => {
        setModalHTML(html);
      }).catch(() => announce('could not open editor', 4000));
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
    root.childNodes.forEach((node) => {
      // Plain typing in a contenteditable creates a direct text node in
      // Chromium; querySelectorAll(':scope > *') misses it and used to turn
      // a real comment into an empty ADF paragraph.
      if (node.nodeType === Node.TEXT_NODE) {
        if (node.textContent.trim()) {
          const content = [];
          serializeInline([node], [], content);
          blocks.push({ type: 'paragraph', content });
        }
        return;
      }
      if (node.nodeType !== Node.ELEMENT_NODE) return;
      const el = node;
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
      postWorker({ type: 'sync-now' });
      const path = elt.getAttribute('hx-post') || '';
      if (path.includes('/edit')) zzira.closeModal();
    }
  });

  // ---- Board drag & drop: rank (+ transition) commands ----
  function initBoard(scope) {
    const board = (scope || document).querySelector('#board');
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
    if (!offlineMode && navigator.onLine) {
      fetch(path, {
        method,
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body,
      }).then((res) => {
        if (!res.ok) announce('command failed: ' + res.status, 4000);
      }).catch(() => {
        postWorker({ type: 'enqueue', method, path, body, kind });
        announce('offline \u2014 queued', 3000);
      });
      return;
    }
    postWorker({ type: 'enqueue', method, path, body, kind });
  }

  // ---- Service worker for offline page shells ----
  if ('serviceWorker' in navigator) {
    navigator.serviceWorker.register('/sw.js').catch((err) => console.error('sw registration failed:', err));
  }
})();
