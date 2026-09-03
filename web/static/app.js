// ZZIRA page glue: owns the sync worker, feeds it the current view, swaps
// locally-rendered HTML into the DOM, and queues commands in the outbox when
// offline (server-wins reconciliation on reconnect).
(function () {
  'use strict';

  const authPage = location.pathname === '/login' || location.pathname === '/signed-out';
  if (authPage) sessionStorage.removeItem('zzira-replica-id');

  function savedTheme() {
    const value = localStorage.getItem('zzira-theme');
    return value === 'light' || value === 'dark' ? value : null;
  }

  function applyTheme(theme) {
    document.documentElement.dataset.theme = theme;
    document.querySelectorAll('[data-theme-toggle]').forEach((button) => {
      const dark = theme === 'dark';
      button.setAttribute('aria-pressed', String(dark));
      button.setAttribute('aria-label', dark ? 'Switch to light mode' : 'Switch to dark mode');
      button.innerHTML = `<span aria-hidden="true">${dark ? '🌙' : '☀️'}</span>`;
    });
  }

  function initThemeToggle() {
    const theme = savedTheme() || document.documentElement.dataset.theme;
    applyTheme(theme);
    document.querySelectorAll('[data-theme-toggle]').forEach((button) => {
      if (button.dataset.ready) return;
      button.dataset.ready = '1';
      button.addEventListener('click', () => {
        const next = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
        localStorage.setItem('zzira-theme', next);
        applyTheme(next);
      });
    });
  }
  document.addEventListener('DOMContentLoaded', initThemeToggle);

  // ---- Application shell: persistent sidebar + Jira-style search shortcut ----
  function setNavigationState(open) {
    const mobile = window.matchMedia('(max-width: 960px)').matches;
    const sidebar = document.getElementById('workspace-navigation');
    if (mobile) {
      document.body.classList.toggle('nav-open', open);
      document.body.classList.remove('nav-collapsed');
      if (sidebar) sidebar.inert = !open;
    } else {
      document.body.classList.remove('nav-open');
      document.body.classList.toggle('nav-collapsed', !open);
      if (sidebar) sidebar.inert = false;
      localStorage.setItem('zzira-sidebar', open ? 'open' : 'collapsed');
    }
    document.querySelectorAll('[data-nav-toggle]').forEach((button) => {
      button.setAttribute('aria-expanded', String(open));
      button.setAttribute('aria-label', open ? 'Collapse sidebar' : 'Expand sidebar');
    });
  }

  function initShell() {
    const media = window.matchMedia('(max-width: 960px)');
    const startOpen = media.matches ? false : localStorage.getItem('zzira-sidebar') !== 'collapsed';
    setNavigationState(startOpen);
    document.querySelectorAll('[data-nav-toggle]').forEach((button) => {
      button.addEventListener('click', () => {
        const open = media.matches
          ? !document.body.classList.contains('nav-open')
          : document.body.classList.contains('nav-collapsed');
        setNavigationState(open);
      });
    });
    document.querySelectorAll('#workspace-navigation a').forEach((link) => {
      link.addEventListener('click', () => { if (media.matches) setNavigationState(false); });
    });
    media.addEventListener('change', (event) => {
      setNavigationState(event.matches ? false : localStorage.getItem('zzira-sidebar') !== 'collapsed');
    });
    document.addEventListener('keydown', (event) => {
      const target = event.target;
      const typing = target && (target.matches('input, textarea, select') || target.closest('[contenteditable]'));
      if (event.key === 'Escape' && target instanceof Element) {
        const details = target.closest('details[open]');
        if (details) {
          event.preventDefault();
          details.open = false;
          const summary = details.querySelector('summary');
          if (summary) summary.focus();
          return;
        }
      }
      if (event.key === '/' && !typing && !event.metaKey && !event.ctrlKey && !event.altKey) {
        const search = document.getElementById('global-jql-query');
        if (search) { event.preventDefault(); search.focus(); }
      }
      if (event.key === '[' && event.ctrlKey && !typing) {
        event.preventDefault();
        const open = media.matches
          ? !document.body.classList.contains('nav-open')
          : document.body.classList.contains('nav-collapsed');
        setNavigationState(open);
      }
      if (event.key === 'Escape' && media.matches && document.body.classList.contains('nav-open')) {
        event.preventDefault();
        setNavigationState(false);
        const button = document.querySelector('[data-nav-toggle]');
        if (button) button.focus();
      }
    });
    document.addEventListener('pointerdown', (event) => {
      const target = event.target;
      document.querySelectorAll('details.user-menu[open], details.more-menu[open], details.column-picker[open], details.save-search[open], details.board-card-move[open], details.backlog-item-menu[open], details.create-sprint[open], details.edit-sprint[open], details.start-sprint[open]').forEach((details) => {
        if (!(target instanceof Node) || !details.contains(target)) details.open = false;
      });
    });
  }

  function setSyncRail(state, label, detail) {
    const rail = document.getElementById('sync-rail');
    if (!rail) return;
    rail.dataset.state = state;
    const labelNode = rail.querySelector('[data-sync-label]');
    const detailNode = rail.querySelector('[data-sync-detail]');
    if (labelNode) labelNode.textContent = label;
    if (detailNode) detailNode.textContent = detail;
  }

  function initBoardFilter() {
    const input = document.querySelector('[data-board-filter]');
    if (!input || input.dataset.ready) return;
    input.dataset.ready = '1';
    input.addEventListener('input', () => {
      const query = input.value.trim().toLocaleLowerCase();
      document.querySelectorAll('.board-column').forEach((column) => {
        let visible = 0;
        column.querySelectorAll('.board-card').forEach((card) => {
          const match = !query || card.textContent.toLocaleLowerCase().includes(query);
          card.hidden = !match;
          if (match) visible += 1;
        });
        const count = column.querySelector('.column-count');
        if (count) count.textContent = String(visible);
      });
      const visible = document.querySelectorAll('.board-card:not([hidden])').length;
      const status = document.querySelector('[data-board-filter-status]');
      if (status) status.textContent = visible + (visible === 1 ? ' card shown' : ' cards shown');
    });
  }

  function initNavigator() {
    const navigatorRoot = document.querySelector('[data-navigator]');
    if (!navigatorRoot || navigatorRoot.dataset.ready) return;
    navigatorRoot.dataset.ready = '1';
    const rows = Array.from(navigatorRoot.querySelectorAll('[data-navigator-row]'));
    const preview = document.getElementById('navigator-preview');
    let selectedIndex = rows.length ? 0 : -1;

    function selectRow(index, loadPreview) {
      if (!rows.length) return;
      selectedIndex = Math.max(0, Math.min(index, rows.length - 1));
      rows.forEach((row, rowIndex) => {
        const link = row.querySelector('[data-preview-link]');
        if (rowIndex === selectedIndex) {
          row.setAttribute('data-selected', 'true');
          if (link) link.setAttribute('aria-current', 'true');
        } else {
          row.removeAttribute('data-selected');
          if (link) link.removeAttribute('aria-current');
        }
      });
      const row = rows[selectedIndex];
      row.scrollIntoView({ block: 'nearest', inline: 'nearest' });
      if (!loadPreview) return;
      const path = row.getAttribute('data-preview-url');
      if (path && window.htmx && preview) {
        preview.setAttribute('aria-busy', 'true');
        window.htmx.ajax('GET', path, { target: '#navigator-preview', swap: 'innerHTML' });
      }
    }

    selectRow(selectedIndex, false);
    rows.forEach((row, index) => {
      row.addEventListener('click', () => selectRow(index, false));
      row.addEventListener('focusin', () => selectRow(index, false));
    });

    document.addEventListener('keydown', (event) => {
      const target = event.target;
      const typing = target instanceof Element && (target.matches('input, textarea, select') || target.closest('[contenteditable]'));
      if (typing || event.metaKey || event.ctrlKey || event.altKey || !rows.length) return;
      const key = event.key.toLocaleLowerCase();
      const navigatorFocused = target instanceof Element && Boolean(target.closest('[data-navigator]'));
      if (key === 'j' || (event.key === 'ArrowDown' && navigatorFocused)) {
        event.preventDefault();
        selectRow(selectedIndex + 1, true);
      } else if (key === 'k' || (event.key === 'ArrowUp' && navigatorFocused)) {
        event.preventDefault();
        selectRow(selectedIndex - 1, true);
      } else if (key === 'o' || (event.key === 'Enter' && target === navigatorRoot)) {
        event.preventDefault();
        const link = rows[selectedIndex].querySelector('.key-cell a');
        if (link) location.assign(link.href);
      }
    });

    const columnInputs = Array.from(document.querySelectorAll('[data-navigator-column]'));
    const knownColumns = new Set(columnInputs.map((input) => input.value));
    let visibleColumns = new Set(knownColumns);
    try {
      const storedColumns = localStorage.getItem('zzira-navigator-columns');
      const saved = JSON.parse(storedColumns || 'null');
      if (Array.isArray(saved)) visibleColumns = new Set(saved.filter((column) => knownColumns.has(column)));
      else if (!storedColumns && window.matchMedia('(max-width: 680px)').matches) visibleColumns = new Set(['type']);
    } catch (_) {
      localStorage.removeItem('zzira-navigator-columns');
    }
    function applyColumns() {
      columnInputs.forEach((input) => { input.checked = visibleColumns.has(input.value); });
      knownColumns.forEach((column) => {
        document.querySelectorAll(`[data-column="${CSS.escape(column)}"]`).forEach((cell) => {
          cell.hidden = !visibleColumns.has(column);
        });
      });
    }
    columnInputs.forEach((input) => {
      input.addEventListener('change', () => {
        if (input.checked) visibleColumns.add(input.value);
        else visibleColumns.delete(input.value);
        localStorage.setItem('zzira-navigator-columns', JSON.stringify(Array.from(visibleColumns)));
        applyColumns();
      });
    });
    applyColumns();

    document.body.addEventListener('htmx:beforeRequest', (event) => {
      if (preview && event.detail.elt && event.detail.elt.matches('[data-preview-link]')) preview.setAttribute('aria-busy', 'true');
    });
    document.body.addEventListener('htmx:afterRequest', (event) => {
      if (!preview || !event.detail.elt || !event.detail.elt.matches('[data-preview-link]')) return;
      preview.setAttribute('aria-busy', 'false');
      if (!event.detail.successful) announce('could not load issue preview', 4000);
    });
    document.body.addEventListener('htmx:afterSwap', (event) => {
      if (preview && event.detail.target === preview) preview.setAttribute('aria-busy', 'false');
    });
  }

  let activityFilter = 'comment';
  let activityOldestFirst = false;

  function initIssueTriage(scope) {
    const root = (scope || document).querySelector('#issue-root');
    if (!root || root.dataset.triageReady) return;
    root.dataset.triageReady = '1';
    const ledger = root.querySelector('[data-activity-ledger]');
    if (!ledger) return;
    const entries = Array.from(ledger.querySelectorAll('[data-activity-kind]'));
    const empty = ledger.querySelector('[data-activity-filter-empty]');
    const filters = Array.from(root.querySelectorAll('[data-activity-filter]'));
    let selected = activityFilter;
    let oldestFirst = activityOldestFirst;

    function applyActivityView() {
      let visible = 0;
      entries.forEach((entry) => {
        entry.hidden = selected !== 'all' && entry.dataset.activityKind !== selected;
        if (!entry.hidden) visible += 1;
      });
      entries.sort((left, right) => {
        const order = (left.dataset.created || '').localeCompare(right.dataset.created || '');
        return oldestFirst ? order : -order;
      }).forEach((entry) => ledger.insertBefore(entry, empty));
      if (empty) empty.hidden = visible !== 0 || entries.length === 0;
    }

    filters.forEach((button) => {
      button.setAttribute('aria-pressed', String(button.dataset.activityFilter === selected));
      button.addEventListener('click', () => {
        selected = button.dataset.activityFilter || 'all';
        activityFilter = selected;
        filters.forEach((candidate) => candidate.setAttribute('aria-pressed', String(candidate === button)));
        applyActivityView();
      });
    });
    const sortButton = root.querySelector('[data-activity-sort]');
    if (sortButton) {
      sortButton.addEventListener('click', () => {
        oldestFirst = !oldestFirst;
        activityOldestFirst = oldestFirst;
        sortButton.setAttribute('aria-pressed', String(oldestFirst));
        sortButton.setAttribute('aria-label', oldestFirst ? 'Sort activity newest first' : 'Sort activity oldest first');
        sortButton.textContent = oldestFirst ? 'Oldest first ↑' : 'Newest first ↓';
        applyActivityView();
      });
    }
    if (sortButton && oldestFirst) {
      sortButton.setAttribute('aria-pressed', 'true');
      sortButton.setAttribute('aria-label', 'Sort activity newest first');
      sortButton.textContent = 'Oldest first ↑';
    }
    applyActivityView();
  }

  document.addEventListener('DOMContentLoaded', () => {
    initShell();
    initBoardFilter();
    initNavigator();
    initIssueTriage(document);
    setSyncRail(navigator.onLine ? 'online' : 'offline', navigator.onLine ? 'Ready' : 'Offline', navigator.onLine ? 'Local replica' : 'Showing local copy');
  });
  document.body.addEventListener('htmx:afterSettle', () => initIssueTriage(document));
  document.addEventListener('keydown', (event) => {
    const target = event.target;
    const typing = target instanceof Element && (target.matches('input, textarea, select') || target.closest('[contenteditable]'));
    if (typing || event.metaKey || event.ctrlKey || event.altKey || event.key.toLocaleLowerCase() !== 'w') return;
    const watchButton = document.querySelector('#issue-root .watch-button');
    if (watchButton instanceof HTMLButtonElement) {
      event.preventDefault();
      watchButton.click();
    }
  });

  if (!('Worker' in window)) return;

  // The replica belongs to views that consume it. Starting one on the
  // post-login home page and immediately replacing it on /browse races two
  // OPFS access handles for the same database during navigation.
  const replicaView = document.body.hasAttribute('data-current-issue') ||
    document.body.hasAttribute('data-board');
  function replicaID() {
    const key = 'zzira-replica-id';
    const existing = sessionStorage.getItem(key);
    if (existing) return existing;
    const id = crypto.randomUUID();
    sessionStorage.setItem(key, id);
    return id;
  }
  const worker = replicaView
    ? new Worker('/static/worker.js?v=11&replica=' + encodeURIComponent(replicaID()))
    : null;
  const banner = () => document.getElementById('sync-banner');
  let workerReady = false;
  let mutationAwaitingSync = false;
  let mutationMinSeq = 0;
  const pendingWorkerMessages = [];

  // A page can be used before the SQLite/WASM worker finishes booting. Keep
  // commands until it advertises readiness instead of dropping the first
  // view or an offline edit on the floor.
  function postWorker(message) {
    if (!worker) return;
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
    initModals(scope);
    initIssueTriage(scope);
  }

  let modalReturnFocus = null;
  let modalReturnFocusSelector = '';
  const modalInertState = new Map();
  function setBackgroundInert(inert) {
    Array.from(document.body.children).forEach((child) => {
      if (child.id === 'modal-root') return;
      if (inert) {
        if (!modalInertState.has(child)) modalInertState.set(child, child.inert);
        child.inert = true;
      } else if (modalInertState.has(child)) {
        child.inert = modalInertState.get(child);
        modalInertState.delete(child);
      }
    });
  }
  function modalFocusable(modal) {
    return Array.from(modal.querySelectorAll('a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'));
  }
  function initModals(scope) {
    const root = scope || document;
    const modal = root.querySelector ? root.querySelector('.modal[role="dialog"]') : null;
    if (!modal || modal.dataset.ready) return;
    modal.dataset.ready = '1';
    setBackgroundInert(true);
    root.querySelectorAll('[data-modal-backdrop]').forEach((backdrop) => {
      backdrop.addEventListener('click', () => window.zzira.closeModal());
    });
    modal.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') { event.preventDefault(); window.zzira.closeModal(); return; }
      if (event.key !== 'Tab') return;
      const targets = modalFocusable(modal);
      if (!targets.length) { event.preventDefault(); return; }
      const first = targets[0], last = targets[targets.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    });
    const autofocus = modal.querySelector('[autofocus]') || modalFocusable(modal)[0] || modal;
    autofocus.focus();
  }

  function setModalHTML(html) {
    const root = document.getElementById('modal-root');
    if (!root) return;
    root.innerHTML = html;
    root.style.display = 'block';
    initModals(root);
    if (!navigator.onLine) {
      root.querySelectorAll('form[hx-post], form[hx-delete]').forEach((form) => {
        const path = form.getAttribute('hx-post') || form.getAttribute('hx-delete');
        form.setAttribute('data-outbox-path', path);
        form.removeAttribute('hx-post');
        form.removeAttribute('hx-delete');
        // This is an offline command form, not an HTMX form with an error
        // handler. Bind its submit control directly before hydration so one
        // user action produces one outbox command.
        const queue = (event) => {
          event.preventDefault();
          event.stopImmediatePropagation();
          queueOfflineForm(form);
        };
        form.addEventListener('submit', queue);
        form.querySelectorAll('button[type="submit"]').forEach((button) => {
          button.addEventListener('click', queue, true);
        });
      });
    }
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

  if (worker) worker.onmessage = (e) => {
    const msg = e.data || {};
    switch (msg.type) {
      case 'ready':
        workerReady = true;
        while (pendingWorkerMessages.length) worker.postMessage(pendingWorkerMessages.shift());
        announce('local sync ready (renderer ' + (msg.renderer || '?') + ')', 2500);
        setSyncRail('syncing', 'Syncing', 'Opening local replica');
        pushView();
        break;
      case 'synced':
        sawSync = true;
        if (mutationAwaitingSync && mutationMinSeq > 0 && msg.seq >= mutationMinSeq) {
          mutationAwaitingSync = false;
          mutationMinSeq = 0;
          pendingRootHtml = null;
        }
        announce('synced \u00b7 seq ' + msg.seq, 1500);
        setSyncRail('online', 'Synced', 'Sequence ' + msg.seq);
        refreshBoardRegion();
        break;
      case 'queued':
        announce('offline \u2014 queued (' + msg.size + ')', 4000);
        setSyncRail('offline', 'Queued offline', msg.size + ' pending');
        break;
      case 'offline':
        announce('offline \u2014 showing local copy', 4000);
        setSyncRail('offline', 'Offline', 'Showing local copy');
        break;
      case 'html':
        // Skip the worker's redundant initial render: the server already
        // SSR-rendered this view. Offline reloads have an empty root (cached
        // shell) and MUST apply.
        if (!sawSync && document.querySelector('.issue-view#issue-root')) break;
        if (msg.issueId && msg.issueId === currentIssueId()) {
          if (!offlineMode && navigator.onLine) {
            const key = currentIssueKey();
            if (!key) break;
            fetch('/browse/' + encodeURIComponent(key), { headers: { 'HX-Request': 'true' } })
              .then((response) => {
                if (!response.ok) throw new Error('HTTP ' + response.status);
                return response.text();
              })
              .then(applyRootHtml)
              .catch(() => { /* a later sync or reconnect will retry */ });
          } else {
            applyRootHtml(msg.html);
          }
        }
        break;
      case 'info':
        announce('worker: ' + (msg.message || ''), 4000);
        break;
      case 'dialog-html':
        setModalHTML(msg.html);
        break;
      case 'error':
        mutationAwaitingSync = false;
        mutationMinSeq = 0;
        pendingRootHtml = null;
        announce('sync error: ' + (msg.message || 'unknown'), 5000);
        setSyncRail('offline', 'Sync needs attention', msg.message || 'Unknown error');
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
    // A sync can arrive between filling a field and clicking its submit
    // button. Focus has moved by then, but replacing the root would still
    // erase the user's command before HTMX can send it.
    const dirtyForm = root.querySelector('form[data-dirty="true"]');
    // Also defer if the create/edit dialog is open (the user is mid-edit)
    const modalOpen = root.style.display === 'block' ||
      (document.getElementById('modal-root') && document.getElementById('modal-root').style.display === 'block');
    if (editing || dirtyForm || modalOpen || mutationAwaitingSync) { pendingRootHtml = html; return; }
    if (root.outerHTML === html) { pendingRootHtml = null; return; } // no-op render
    const incomingSeq = seqOf(html);
    if (incomingSeq > 0 && incomingSeq < seqOfDom()) {
      pendingRootHtml = null;
      return; // stale replica render: the DOM already has newer data
    }
    root.outerHTML = html;
    pendingRootHtml = null;
    hydrate(document.getElementById('issue-root'));
  }

  document.addEventListener('focusout', () => {
    if (!pendingRootHtml) return;
    const active = document.activeElement;
    const root = document.getElementById('issue-root');
    const dirtyForm = root && root.querySelector('form[data-dirty="true"]');
    if (root && !dirtyForm && !mutationAwaitingSync && (!active || !root.contains(active) ||
        (!active.closest('[contenteditable]') && !['INPUT', 'TEXTAREA', 'SELECT'].includes(active.tagName)))) {
      root.outerHTML = pendingRootHtml;
      pendingRootHtml = null;
      hydrate(document.getElementById('issue-root'));
    }
  });

  document.addEventListener('input', (event) => {
    const target = event.target;
    if (!target || !target.closest) return;
    const root = target.closest('#issue-root');
    const form = target.closest('form');
    if (root && form) form.dataset.dirty = 'true';
  }, true);

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
  document.body.addEventListener('htmx:afterSettle', () => initModals(document));

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
    const path = form.getAttribute('data-outbox-path') || form.getAttribute('hx-post') || form.getAttribute('hx-delete');
    const kind = outboxKind(path || '');
    if (!kind) return false;
    const body = new URLSearchParams(new FormData(form)).toString();
    postWorker({ type: 'enqueue', method: 'POST', path, body, kind });
    return true;
  }

  // Catch native submits as well as HTMX requests. This is important during
  // first-load offline transitions, when a dialog can exist before HTMX has
  // had a chance to process its dynamically inserted form.
  document.addEventListener('submit', (evt) => {
    const form = evt.target;
    if (!(form instanceof HTMLFormElement) || (!offlineMode && navigator.onLine)) return;
    if (!queueOfflineForm(form)) return;
    evt.preventDefault();
    evt.stopPropagation();
    if (form.closest('[role="dialog"]')) window.zzira.closeModal();
  }, true);

  document.body.addEventListener('htmx:beforeRequest', (evt) => {
    const elt = evt.detail.elt;
    if (!elt || elt.tagName !== 'FORM') return;
    if (!offlineMode && navigator.onLine) {
      mutationAwaitingSync = true;
      // An initial sync may already be in flight. Its acknowledgement must
      // not be mistaken for acknowledgement of the command about to commit.
      mutationMinSeq = Number.MAX_SAFE_INTEGER;
      return; // online: let htmx do its thing
    }
    if (!queueOfflineForm(elt)) return; // reads fail naturally offline
    evt.preventDefault();
    evt.stopPropagation();
    if (elt.closest('[role="dialog"]')) window.zzira.closeModal();
  }, true);

  window.addEventListener('online', () => {
    offlineMode = false;
    announce('back online \u2014 syncing\u2026', 2000);
    setSyncRail('syncing', 'Back online', 'Syncing changes');
    // Reconnection is a protocol event, not an opportunity to wait for the
    // next maintenance tick. This message follows buffered offline commands.
    postWorker({ type: 'sync-now' });
  });
  window.addEventListener('offline', () => {
    offlineMode = true;
    setSyncRail('offline', 'Offline', 'Showing local copy');
  });

  // ---- Modal helpers ----
  window.zzira = {
    openModal(trigger) {
      const root = document.getElementById('modal-root');
      modalReturnFocus = trigger instanceof HTMLElement ? trigger : document.activeElement;
      modalReturnFocusSelector = modalReturnFocus instanceof HTMLElement && modalReturnFocus.id
        ? '#' + CSS.escape(modalReturnFocus.id) : '';
      if (root) root.style.display = 'block';
    },
    closeModal() {
      const root = document.getElementById('modal-root');
      if (root) { root.style.display = 'none'; root.innerHTML = ''; }
      setBackgroundInert(false);
      const returnReference = modalReturnFocus;
      const returnSelector = modalReturnFocusSelector;
      const restoreFocus = () => {
        const target = returnReference instanceof HTMLElement && returnReference.isConnected
          ? returnReference
          : (returnSelector ? document.querySelector(returnSelector) : null);
        if (target instanceof HTMLElement) target.focus();
      };
      restoreFocus();
      // A deferred replica render may replace the issue fragment as the
      // dialog closes. Restore against its stable trigger once that swap has
      // had a chance to complete as well.
      setTimeout(restoreFocus, 0);
      modalReturnFocus = null;
      modalReturnFocusSelector = '';
    },
    openEdit(key, trigger) {
      const root = document.getElementById('modal-root');
      if (!root) return;
      modalReturnFocus = trigger instanceof HTMLElement ? trigger : document.activeElement;
      modalReturnFocusSelector = '#edit-issue-button';
      // Mark the modal region as active before the asynchronous renderer
      // responds so a replica refresh cannot replace the return-focus target.
      root.style.display = 'block';
      // The replica owns offline commands. Request its locally rendered dialog
      // instead of relying on an SSR-only template that a replica render can
      // replace.
      if (!navigator.onLine) {
        const issueId = currentIssueId();
        if (!worker || !issueId) {
          root.style.display = 'none';
          announce('offline editing is unavailable for this view', 4000);
          return;
        }
        postWorker({ type: 'edit-dialog', issueId });
        return;
      }
      fetch(`/issues/${key}/edit`).then(r => {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.text();
      }).then(html => {
        setModalHTML(html);
      }).catch(() => {
        window.zzira.closeModal();
        announce('could not open editor', 4000);
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
      // The server response is newer than any replica render deferred while
      // this form was dirty; never let that stale render win after the swap.
      pendingRootHtml = null;
      mutationMinSeq = seqOf((evt.detail.xhr && evt.detail.xhr.responseText) || '');
      postWorker({ type: 'sync-now' });
      const path = elt.getAttribute('hx-post') || '';
      if (path.includes('/edit')) zzira.closeModal();
    } else if (elt && elt.tagName === 'FORM') {
      mutationAwaitingSync = false;
      mutationMinSeq = 0;
    }
  });

  // ---- Board drag & drop: rank (+ transition) commands ----
  function initBoard(scope) {
    const board = (scope || document).querySelector('#board');
    if (!board || board.dataset.ready) return;
    board.dataset.ready = '1';
    const boardID = document.body.getAttribute('data-board') || '';
    let grabbedCard = null;

    function columnName(column) {
      const name = column.querySelector('.lozenge-column');
      return name ? name.textContent.trim() : 'column';
    }

    function updateColumnCounts() {
      board.querySelectorAll('.board-column').forEach((column) => {
        const count = column.querySelector('.column-count');
        if (count) count.textContent = String(column.querySelectorAll('[data-column] > .board-card').length);
      });
    }

    function rankCard(card, column, index) {
      const target = column.querySelector('[data-column]');
      const cards = Array.from(target.children).filter((item) => item !== card);
      const before = cards[index] || null;
      const after = index > 0 ? cards[index - 1] : null;
      target.insertBefore(card, before);
      const issue = card.getAttribute('data-issue') || '';
      const status = column.getAttribute('data-status') || '';
      sendOrQueue('POST', '/board/' + boardID + '/rank', new URLSearchParams({
        issue, status,
        before: before ? before.getAttribute('data-issue') || '' : '',
        after: after ? after.getAttribute('data-issue') || '' : '',
      }).toString(), 'rank');
      updateColumnCounts();
      announce('Moved ' + (card.getAttribute('data-key') || 'issue') + ' to ' + columnName(column), 2500);
    }

    board.querySelectorAll('.board-card').forEach((card) => {
      const handle = card.querySelector('[data-card-drag]');
      if (!handle) return;
      handle.addEventListener('click', () => {
        if (grabbedCard && grabbedCard !== card) {
          grabbedCard.classList.remove('is-grabbed');
          const previousHandle = grabbedCard.querySelector('[data-card-drag]');
          if (previousHandle) previousHandle.setAttribute('aria-pressed', 'false');
        }
        grabbedCard = grabbedCard === card ? null : card;
        card.classList.toggle('is-grabbed', Boolean(grabbedCard));
        handle.setAttribute('aria-pressed', String(Boolean(grabbedCard)));
        announce(grabbedCard ? 'Picked up ' + (card.getAttribute('data-key') || 'issue') + '. Use arrow keys to move it; press the move button again to drop.' : 'Dropped issue.', 2500);
      });
      handle.addEventListener('keydown', (event) => {
        if (!grabbedCard || grabbedCard !== card || !['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight'].includes(event.key)) return;
        event.preventDefault();
        const source = card.closest('.board-column');
        const columns = Array.from(board.querySelectorAll('.board-column'));
        let targetColumn = source;
        let targetIndex = Array.from(source.querySelector('[data-column]').children).indexOf(card);
        if (event.key === 'ArrowUp') {
          if (targetIndex === 0) return;
          targetIndex -= 1;
        }
        if (event.key === 'ArrowDown') {
          if (targetIndex === source.querySelector('[data-column]').children.length - 1) return;
          targetIndex += 1;
        }
        if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
          const direction = event.key === 'ArrowLeft' ? -1 : 1;
          const sourceIndex = columns.indexOf(source);
          targetColumn = columns[sourceIndex + direction];
          if (!targetColumn) return;
          targetIndex = targetColumn.querySelector('[data-column]').children.length;
        }
        rankCard(card, targetColumn, targetIndex);
      });

      card.querySelectorAll('[data-card-move]').forEach((button) => {
        button.addEventListener('click', () => {
          const direction = button.getAttribute('data-card-move');
          const source = card.closest('.board-column');
          const columns = Array.from(board.querySelectorAll('.board-column'));
          const sourceCards = Array.from(source.querySelector('[data-column]').children);
          const sourceIndex = sourceCards.indexOf(card);
          let targetColumn = source;
          let targetIndex = sourceIndex;
          if (direction === 'up') {
            if (sourceIndex === 0) return;
            targetIndex -= 1;
          } else if (direction === 'down') {
            if (sourceIndex === sourceCards.length - 1) return;
            targetIndex += 1;
          } else {
            const offset = direction === 'left' ? -1 : 1;
            targetColumn = columns[columns.indexOf(source) + offset];
            if (!targetColumn) return;
            targetIndex = targetColumn.querySelector('[data-column]').children.length;
          }
          rankCard(card, targetColumn, targetIndex);
          const details = button.closest('details');
          if (details) details.open = false;
          handle.focus();
        });
      });
    });

    board.querySelectorAll('[data-column]').forEach((col) => {
      new Sortable(col, {
        group: 'board',
        handle: '[data-card-drag]',
        animation: 150,
        onEnd: (evt) => {
          const card = evt.item;
          const issue = card.getAttribute('data-issue');
          const status = card.closest('[data-column]').closest('.board-column')
            .getAttribute('data-status');
          const cards = Array.from(card.parentElement.children);
          const idx = cards.indexOf(card);
          const after = idx > 0 ? cards[idx - 1].getAttribute('data-issue') : '';
          const before = idx < cards.length - 1 ? cards[idx + 1].getAttribute('data-issue') : '';
          const body = new URLSearchParams({ issue, status, before, after }).toString();
          sendOrQueue('POST', '/board/' + boardID + '/rank', body, 'rank');
          updateColumnCounts();
          announce('Moved ' + (card.getAttribute('data-key') || 'issue') + ' to ' + columnName(card.closest('.board-column')), 2500);
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
        if (res.ok) return;
        if ([408, 425, 429].includes(res.status) || res.status >= 500) {
          postWorker({ type: 'enqueue', method, path, body, kind });
          announce('server unavailable — command queued', 4000);
          return;
        }
        announce('command failed: ' + res.status, 4000);
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
    navigator.serviceWorker.register('/sw.js').then((registration) => {
      if (authPage) {
        const target = navigator.serviceWorker.controller || registration.active;
        if (target) target.postMessage({ type: 'CLEAR_PRIVATE_CACHE' });
      }
    }).catch((err) => console.error('sw registration failed:', err));
  }
})();
