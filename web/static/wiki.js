// The storage editor serializes only the markup supported by both renderers.
const escapeStorage = (text) => text.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;');
const mentionStorage = (id, name) => `<ac:link><ri:user ri:account-id="${escapeStorage(id)}" /><ac:plain-text-link-body>${escapeStorage(name)}</ac:plain-text-link-body></ac:link>`;
const mentionQuery = /(^|\s)@([^\s@<>]{0,40})$/;
// The macros the editor draws, written back as the storage Confluence keeps.
const panelMacros = ['info', 'note', 'warning', 'tip', 'panel'];
const macroIDStorage = (node) => (node.dataset.macroId ? ` ac:macro-id="${escapeStorage(node.dataset.macroId)}"` : '');
const macroParameter = (name, value) => `<ac:parameter ac:name="${name}">${escapeStorage(value)}</ac:parameter>`;

// Typing @ in a page, blog post or comment offers the site's people; choosing
// one writes a Confluence user mention, which tells that person.
const mentionPicker = (people) => {
  const list = document.createElement('ul');
  list.id = 'wiki-mention-list';
  list.className = 'wiki-mention-list';
  list.setAttribute('role', 'listbox');
  list.setAttribute('aria-label', 'People to mention');
  list.hidden = true;
  // The list stays in the document so the fields' aria-controls always
  // refer to it; it moves beside whichever field is asking.
  document.body.append(list);
  let field = null;
  let adapter = null;
  let matches = [];
  let active = 0;
  const close = () => {
    list.hidden = true;
    if (field) field.removeAttribute('aria-activedescendant');
  };
  const highlight = (index) => {
    active = index;
    [...list.children].forEach((option, i) => option.setAttribute('aria-selected', String(i === index)));
    if (matches.length) field.setAttribute('aria-activedescendant', `wiki-mention-option-${index}`);
  };
  const choose = (index) => {
    const person = matches[index];
    if (!person) return;
    adapter.insert(person);
    close();
  };
  const update = (target, targetAdapter) => {
    const query = targetAdapter.query();
    if (query === null) {
      if (field === target) close();
      return;
    }
    field = target;
    adapter = targetAdapter;
    const needle = query.toLowerCase();
    matches = people.filter((person) => person.name.toLowerCase().includes(needle)).slice(0, 8);
    list.replaceChildren(...matches.map((person, index) => {
      const option = document.createElement('li');
      option.id = `wiki-mention-option-${index}`;
      option.setAttribute('role', 'option');
      option.textContent = person.name;
      option.addEventListener('mousedown', (event) => {
        event.preventDefault();
        choose(index);
      });
      return option;
    }));
    if (!matches.length) {
      close();
      return;
    }
    target.after(list);
    list.hidden = false;
    highlight(0);
  };
  const attach = (target, targetAdapter) => {
    target.setAttribute('aria-autocomplete', 'list');
    target.setAttribute('aria-controls', list.id);
    target.addEventListener('input', () => update(target, targetAdapter));
    target.addEventListener('blur', () => { if (field === target) close(); });
    target.addEventListener('keydown', (event) => {
      if (list.hidden || field !== target) return;
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        const step = event.key === 'ArrowDown' ? 1 : -1;
        highlight((active + step + matches.length) % matches.length);
      } else if (event.key === 'Enter' || event.key === 'Tab') {
        event.preventDefault();
        choose(active);
      } else if (event.key === 'Escape') {
        event.preventDefault();
        close();
      }
    });
  };
  return { attach, update };
};

const textareaMentions = (textarea) => ({
  query() {
    if (textarea.selectionStart !== textarea.selectionEnd) return null;
    const match = mentionQuery.exec(textarea.value.slice(0, textarea.selectionStart));
    return match ? match[2] : null;
  },
  insert(person) {
    const caret = textarea.selectionStart;
    const start = textarea.value.lastIndexOf('@', caret - 1);
    const markup = `${mentionStorage(person.id, person.name)} `;
    textarea.setRangeText(markup, start, caret, 'end');
    textarea.focus();
  },
});

const editorMentions = (editor) => ({
  query() {
    const selection = document.getSelection();
    if (!selection.isCollapsed || !editor.contains(selection.anchorNode) || selection.anchorNode.nodeType !== Node.TEXT_NODE) return null;
    const match = mentionQuery.exec(selection.anchorNode.textContent.slice(0, selection.anchorOffset));
    return match ? match[2] : null;
  },
  insert(person) {
    const selection = document.getSelection();
    const node = selection.anchorNode;
    const caret = selection.anchorOffset;
    const range = document.createRange();
    range.setStart(node, node.textContent.lastIndexOf('@', caret - 1));
    range.setEnd(node, caret);
    range.deleteContents();
    const link = document.createElement('a');
    link.href = `/people/${encodeURIComponent(person.id)}`;
    link.textContent = `@${person.name}`;
    const space = document.createTextNode(' ');
    range.insertNode(space);
    range.insertNode(link);
    selection.collapse(space, 1);
    editor.focus();
  },
});


// An open page or blog post reports itself every few seconds. The answer says
// who else has it open, and whether its version or comments have changed
// since it was loaded, so readers and writers see each other's work.
const startLive = (element) => {
  const people = element.querySelector('.wiki-live-people');
  const notice = element.querySelector('.wiki-live-notice');
  const editing = element.dataset.editing === 'true';
  const interval = Number(element.dataset.interval) || 15000;
  let baseline = null;
  const describe = (present) => present.map((person) => `${person.displayName}${person.editing ? ' (editing)' : ''}`).join(', ');
  const report = async () => {
    const body = new URLSearchParams({ editing: String(editing) });
    let state;
    try {
      const response = await fetch(element.dataset.presenceUrl, { method: 'POST', body, credentials: 'same-origin', headers: { Accept: 'application/json' } });
      if (!response.ok) return;
      state = await response.json();
    } catch {
      return;
    }
    if (!baseline) baseline = { version: state.version, comments: state.commentCount };
    const others = state.present || [];
    const coEditors = others.filter((person) => person.editing);
    if (editing && coEditors.length) {
      people.textContent = element.dataset.liveEditing === 'true'
        ? `${describe(coEditors)} ${coEditors.length === 1 ? 'is' : 'are'} also editing. Your changes merge as you type.`
        : `${describe(coEditors)} ${coEditors.length === 1 ? 'is' : 'are'} also editing. Save often; a save made after theirs asks you to merge.`;
    } else {
      people.textContent = others.length ? `Also here: ${describe(others)}` : '';
    }
    notice.replaceChildren();
    if (state.version > baseline.version) {
      if (!(editing && element.dataset.liveEditing === 'true')) notice.append(editing ? 'A newer version has been published since you started editing. ' : 'This has been updated. ');
      if (!editing) {
        const link = document.createElement('a');
        link.href = window.location.pathname;
        link.textContent = 'Show the latest version';
        notice.append(link);
      }
    } else if (!editing && state.commentCount > baseline.comments) {
      notice.append('New comments have been added. ');
      const link = document.createElement('a');
      link.href = `${window.location.pathname}#${document.querySelector('.wiki-discussion')?.id || ''}`;
      link.textContent = 'Show new comments';
      link.addEventListener('click', () => window.location.reload());
      notice.append(link);
    }
    element.hidden = !people.textContent && !notice.textContent;
  };
  report();
  const timer = setInterval(report, interval);
  window.addEventListener('pagehide', () => clearInterval(timer));
};


// Live editing. Everyone editing a published page shares one document: each
// editor sends its unsent change against the latest revision it holds. When
// someone else's change got there first, the server returns what this editor
// missed; the editor applies it to the text it last synced and rebases its own
// change on top, so both edits survive. Positions are UTF-16 offsets into the
// page's storage markup, as JavaScript strings measure them.
const liveDiff = (before, after) => {
  if (before === after) return null;
  const limit = Math.min(before.length, after.length);
  let start = 0;
  while (start < limit && before.charCodeAt(start) === after.charCodeAt(start)) start += 1;
  let end = 0;
  while (end < limit - start && before.charCodeAt(before.length - 1 - end) === after.charCodeAt(after.length - 1 - end)) end += 1;
  // Never split a surrogate pair at either edge of the change.
  const low = (text, index) => index > 0 && index < text.length && text.charCodeAt(index) >= 0xdc00 && text.charCodeAt(index) <= 0xdfff;
  while (start > 0 && (low(before, start) || low(after, start))) start -= 1;
  while (end > 0 && (low(before, before.length - end) || low(after, after.length - end))) end -= 1;
  return { position: start, delete: before.length - start - end, insert: after.slice(start, after.length - end) };
};

const liveApply = (text, change) => text.slice(0, change.position) + change.insert + text.slice(change.position + change.delete);

// liveRebase moves a local change so it applies after a change that was
// applied before it. Text the other change inserted is never deleted, and an
// insertion at the same place goes after theirs.
const liveRebase = (local, applied) => {
  const aStart = local.position;
  const aEnd = local.position + local.delete;
  const bStart = applied.position;
  const bEnd = applied.position + applied.delete;
  const inserted = applied.insert.length;
  if (aEnd <= bStart && aStart < bStart) return [local];
  if (local.delete === 0 && aStart === bStart) return [{ ...local, position: bStart + inserted }];
  if (aStart >= bEnd) return [{ ...local, position: aStart - applied.delete + inserted }];
  const changes = [];
  const after = aEnd > bEnd ? aEnd - bEnd : 0;
  if (aStart < bStart) {
    if (after) changes.push({ position: bStart + inserted, delete: after, insert: '' });
    changes.push({ position: aStart, delete: bStart - aStart, insert: local.insert });
  } else {
    changes.push({ position: bStart + inserted, delete: after, insert: local.insert });
  }
  return changes.filter((change) => change.delete || change.insert);
};

// liveMerge applies the changes this editor missed to the text it synced and
// carries its own unsent edits over.
const liveMerge = (synced, local, changes) => {
  let base = synced;
  let current = local;
  for (const change of changes) {
    const next = liveApply(base, change);
    const pending = liveDiff(base, current);
    current = pending ? liveRebase(pending, change).reduce(liveApply, next) : next;
    base = next;
  }
  return { synced: base, local: current };
};

// renderStorage draws the storage markup the rich editor keeps, the way the
// server renders it, or returns null for markup the editor cannot hold.
const renderStorage = (storage) => {
  const allowed = new Set(['p', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'ul', 'ol', 'li', 'blockquote', 'pre', 'code', 'strong', 'em', 'b', 'i', 'u', 's', 'a', 'br', 'hr', 'table', 'thead', 'tbody', 'tr', 'th', 'td']);
  const parsed = new DOMParser().parseFromString(`<root xmlns:ac="urn:ac" xmlns:ri="urn:ri">${storage}</root>`, 'application/xml');
  if (parsed.getElementsByTagName('parsererror').length) return null;
  const draw = (node) => {
    if (node.nodeType === Node.TEXT_NODE) return document.createTextNode(node.textContent);
    if (node.nodeType !== Node.ELEMENT_NODE) return null;
    if (node.prefix === 'ac' && node.localName === 'link') {
      const user = [...node.children].find((child) => child.localName === 'user');
      const label = [...node.children].find((child) => child.localName === 'plain-text-link-body');
      if (!user) return null;
      const link = document.createElement('a');
      link.href = `/people/${encodeURIComponent(user.getAttribute('ri:account-id') || '')}`;
      link.textContent = `@${(label?.textContent || 'user').trim()}`;
      return link;
    }
    if (node.prefix || !allowed.has(node.localName)) return undefined;
    const element = document.createElement(node.localName);
    if (node.localName === 'a' && node.getAttribute('href')) element.setAttribute('href', node.getAttribute('href'));
    for (const child of node.childNodes) {
      const drawn = draw(child);
      if (drawn === undefined) return undefined;
      if (drawn) element.append(drawn);
    }
    return element;
  };
  const nodes = [];
  for (const child of parsed.documentElement.childNodes) {
    const drawn = draw(child);
    if (drawn === undefined) return null;
    if (drawn) nodes.push(drawn);
  }
  return nodes;
};

const caretOffset = (root) => {
  const selection = document.getSelection();
  if (!selection.rangeCount || !root.contains(selection.anchorNode)) return null;
  const range = document.createRange();
  range.selectNodeContents(root);
  range.setEnd(selection.anchorNode, selection.anchorOffset);
  return range.toString().length;
};

const placeCaret = (root, offset) => {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  let remaining = offset;
  let node = walker.nextNode();
  while (node) {
    if (remaining <= node.textContent.length) {
      document.getSelection().collapse(node, remaining);
      return;
    }
    remaining -= node.textContent.length;
    node = walker.nextNode();
  }
  document.getSelection().selectAllChildren(root);
  document.getSelection().collapseToEnd();
};

// movedOffset keeps a caret beside the same text when the text before it
// changes length.
const movedOffset = (before, after, offset) => {
  let prefix = 0;
  while (prefix < before.length && prefix < after.length && before[prefix] === after[prefix]) prefix += 1;
  return offset <= prefix ? offset : Math.max(prefix, Math.min(after.length, offset + after.length - before.length));
};

// liveMovePosition moves a place in a text through a change to it: a place
// before the change stays, one after it moves by the change's length, and one
// inside what it deleted lands after what it inserted.
const liveMovePosition = (position, change) => {
  if (position <= change.position) return position;
  if (position >= change.position + change.delete) return position - change.delete + change.insert.length;
  return change.position + change.insert.length;
};

// Everyone else editing shows as a named caret where they are working. The
// carets are drawn in a layer over the page, so they never touch the text,
// and are hidden from assistive technology, which hears who is editing from
// the live status instead.
const liveCursorColor = (id) => [...id].reduce((sum, character) => (sum * 31 + character.charCodeAt(0)) % 6, 0) + 1;

const liveCursorLayer = () => {
  let layer = document.querySelector('.wiki-remote-carets');
  if (!layer) {
    layer = document.createElement('div');
    layer.className = 'wiki-remote-carets';
    layer.setAttribute('aria-hidden', 'true');
    document.body.append(layer);
  }
  return layer;
};

const startLiveEditing = (status, text, form) => {
  let session = '';
  let revision = -1;
  let synced = null;
  let initial = text.get();
  let busy = false;
  let composing = false;
  let stopped = false;
  let offline = false;
  let cursors = [];
  // Edits not yet shared are kept on this device, so closing the page or
  // reloading it offline loses nothing: the next editor on this page starts
  // from them and merges them like any unsent typing.
  // Each open editor keeps its own entry and refreshes it with every
  // exchange, so a second editor of the same page never takes typing the
  // first still holds: it only takes what an editor that closed, or stopped
  // answering for ten seconds, left behind.
  const keptPrefix = `zzira-live:${status.dataset.liveUrl}#`;
  const keptKey = `${keptPrefix}${Date.now().toString(36)}${Math.random().toString(36).slice(2, 10)}`;
  const keep = (closed = false) => {
    try {
      const current = text.get();
      if (synced !== null && current !== synced) localStorage.setItem(keptKey, JSON.stringify({ session, revision, synced, text: current, at: Date.now(), closed }));
      else if (synced !== null) localStorage.removeItem(keptKey);
    } catch {
      // Storage may be full or unavailable; live editing carries on without it.
    }
  };
  let kept = null;
  let keptFrom = '';
  try {
    for (let index = 0; index < localStorage.length; index += 1) {
      const key = localStorage.key(index);
      if (!key || !key.startsWith(keptPrefix)) continue;
      const entry = JSON.parse(localStorage.getItem(key) || 'null');
      if (!entry || !Number.isFinite(entry.at) || !(entry.closed || Date.now() - entry.at > 10000)) continue;
      if (!kept || entry.at > kept.at) {
        kept = entry;
        keptFrom = key;
      }
    }
  } catch {
    kept = null;
  }
  const drawCursors = () => {
    const layer = liveCursorLayer();
    layer.replaceChildren();
    if (stopped || synced === null || !cursors.length) return;
    const local = text.get();
    const pending = liveDiff(synced, local);
    const positions = cursors.map((cursor) => (pending ? liveMovePosition(cursor.position, pending) : cursor.position));
    const bounds = text.element.getBoundingClientRect();
    text.caretRects(positions.map((position) => Math.min(position, local.length))).forEach((rect, index) => {
      if (!rect || rect.top + rect.height < bounds.top || rect.top > bounds.bottom || rect.left < bounds.left - 2 || rect.left > bounds.right + 2) return;
      const caret = document.createElement('span');
      caret.className = `wiki-remote-caret wiki-remote-caret-${liveCursorColor(cursors[index].accountId || '')}`;
      caret.style.top = `${rect.top}px`;
      caret.style.left = `${rect.left}px`;
      caret.style.height = `${rect.height}px`;
      const name = document.createElement('span');
      name.className = 'wiki-remote-caret-name';
      name.textContent = cursors[index].displayName;
      caret.append(name);
      layer.append(caret);
      if (name.getBoundingClientRect().right > window.innerWidth) name.classList.add('wiki-remote-caret-name-end');
    });
  };
  const say = (message) => {
    status.hidden = false;
    if (status.textContent !== message) status.textContent = message;
  };
  const sync = async () => {
    if (busy || composing || stopped) return;
    busy = true;
    try {
      const sent = text.get();
      const selection = text.selection();
      const change = synced === null ? null : liveDiff(synced, sent);
      const response = await fetch(status.dataset.liveUrl, {
        method: 'POST', credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({ session, revision, changes: change ? [change] : [], cursor: selection }),
      });
      if (!response.ok) throw new Error(`live editing answered ${response.status}`);
      const state = await response.json();
      if (stopped) return;
      const current = text.get();
      if (typeof state.body === 'string') {
        // The whole document: this editor is new, restarted or far behind.
        if (synced === null && current === initial && initial !== state.body) {
          // The page opened with text of its own, such as a save the server
          // refused, so that text is kept and shared as this editor's change.
          synced = state.body;
        } else {
          const base = synced === null ? initial : synced;
          const merged = liveMerge(base, current, [liveDiff(base, state.body)].filter(Boolean));
          synced = state.body;
          if (merged.local !== current) text.set(merged.local);
        }
      } else if (state.applied) {
        synced = sent;
      } else if (state.changes.length) {
        const merged = liveMerge(synced, current, state.changes);
        synced = merged.synced;
        if (merged.local !== current) text.set(merged.local);
      }
      session = state.session;
      revision = state.revision;
      cursors = Array.isArray(state.cursors) ? state.cursors : [];
      drawCursors();
      // Publishing saves the version after the one the session started from,
      // which moves on when someone else publishes meanwhile.
      const version = form.querySelector('input[name=version]');
      if (version && state.version) version.value = String(state.version + 1);
      if (offline || status.hidden) say('Live editing is on: changes merge with everyone editing this page as you type.');
      offline = false;
      if (liveDiff(synced, text.get())) window.setTimeout(sync, 50);
    } catch {
      if (!offline) say('Live editing is reconnecting. Your changes are kept on this device and merge when it is back.');
      offline = true;
      cursors = [];
      drawCursors();
    } finally {
      busy = false;
      keep();
    }
  };
  if (kept && typeof kept.synced === 'string' && typeof kept.text === 'string' && typeof kept.session === 'string' && Number.isInteger(kept.revision)) {
    try {
      localStorage.removeItem(keptFrom);
    } catch {
      // Nothing to forget.
    }
    if (kept.text !== initial) {
      session = kept.session;
      revision = kept.revision;
      synced = kept.synced;
      text.set(kept.text);
      say('Your changes from before are back and merge when live editing connects.');
    }
  }
  let pending = 0;
  const soon = () => {
    window.clearTimeout(pending);
    pending = window.setTimeout(sync, 250);
  };
  text.element.addEventListener('input', soon);
  text.element.addEventListener('input', drawCursors);
  text.element.addEventListener('scroll', drawCursors);
  window.addEventListener('scroll', drawCursors, { passive: true });
  window.addEventListener('resize', drawCursors);
  text.element.addEventListener('compositionstart', () => { composing = true; });
  text.element.addEventListener('compositionend', () => { composing = false; soon(); });
  const timer = window.setInterval(sync, 1000);
  form.addEventListener('submit', () => { stopped = true; window.clearInterval(timer); drawCursors(); });
  window.addEventListener('pagehide', () => {
    window.clearInterval(timer);
    keep(true);
  });
  sync();
};

const textareaText = (textarea) => ({
  element: textarea,
  get: () => textarea.value,
  set: (value) => {
    const focused = document.activeElement === textarea;
    const start = movedOffset(textarea.value, value, textarea.selectionStart);
    const end = movedOffset(textarea.value, value, textarea.selectionEnd);
    textarea.value = value;
    if (focused) textarea.setSelectionRange(start, end);
  },
  selection: () => ({ position: textarea.selectionStart, end: textarea.selectionEnd }),
  // A textarea cannot say where a place in its text is drawn, so a hidden
  // copy with the same box and type measures it.
  caretRects: (positions) => {
    const style = getComputedStyle(textarea);
    const mirror = document.createElement('div');
    for (const property of ['boxSizing', 'width', 'borderTopWidth', 'borderRightWidth', 'borderBottomWidth', 'borderLeftWidth', 'borderStyle', 'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft', 'fontStyle', 'fontVariant', 'fontWeight', 'fontStretch', 'fontSize', 'lineHeight', 'fontFamily', 'textAlign', 'textTransform', 'textIndent', 'letterSpacing', 'wordSpacing', 'tabSize', 'overflowWrap', 'wordBreak']) {
      mirror.style[property] = style[property];
    }
    Object.assign(mirror.style, { position: 'absolute', visibility: 'hidden', top: '0', left: '0', whiteSpace: 'pre-wrap', overflow: 'hidden' });
    document.body.append(mirror);
    const box = textarea.getBoundingClientRect();
    const lineHeight = parseFloat(style.lineHeight) || parseFloat(style.fontSize) * 1.2;
    const rects = positions.map((position) => {
      const marker = document.createElement('span');
      marker.textContent = '\u200b';
      mirror.replaceChildren(document.createTextNode(textarea.value.slice(0, position)), marker);
      return {
        top: box.top + marker.offsetTop - textarea.scrollTop,
        left: box.left + marker.offsetLeft - textarea.scrollLeft,
        height: lineHeight,
      };
    });
    mirror.remove();
    return rects;
  },
});

// storagePieces lays the rich editor's storage markup out beside its nodes:
// each text node with its escaped text, the markup between them as it is,
// and a mention as a single piece, so a place in the storage and a place in
// the editor can each be found from the other.
const storagePieces = (root, serialize) => {
  const pieces = [];
  const walk = (node) => {
    const storage = serialize(node);
    if (node.nodeType === Node.TEXT_NODE) {
      pieces.push({ node, storage, text: node.textContent });
      return;
    }
    if (node.nodeType !== Node.ELEMENT_NODE) return;
    const inner = [...node.childNodes].map(serialize).join('');
    const at = storage === inner ? 0 : inner ? storage.lastIndexOf(`${inner}</`) : -1;
    if (at < 0) {
      pieces.push({ node, storage, atomic: true });
      return;
    }
    if (at > 0) pieces.push({ storage: storage.slice(0, at) });
    node.childNodes.forEach(walk);
    if (at + inner.length < storage.length) pieces.push({ storage: storage.slice(at + inner.length) });
  };
  root.childNodes.forEach(walk);
  return pieces;
};

// storageOffsetOfText is the place in the storage of a place in the editor's
// text; a place inside a mention is taken to its start.
const storageOffsetOfText = (pieces, textOffset) => {
  let storage = 0;
  let text = 0;
  for (const piece of pieces) {
    if (piece.text !== undefined) {
      if (textOffset <= text + piece.text.length) return storage + escapeStorage(piece.text.slice(0, textOffset - text)).length;
      text += piece.text.length;
    } else if (piece.atomic) {
      const length = piece.node.textContent.length;
      if (textOffset < text + length) return storage;
      text += length;
    }
    storage += piece.storage.length;
  }
  return storage;
};

// domPointAtStorage is the place in the editor of a place in its storage; a
// place inside markup is taken to the start of the text after it.
const domPointAtStorage = (root, pieces, offset) => {
  let storage = 0;
  let last = null;
  let inMarkup = false;
  for (const piece of pieces) {
    const end = storage + piece.storage.length;
    if (piece.text !== undefined) {
      if (inMarkup) return { node: piece.node, offset: 0 };
      if (offset <= end) {
        let raw = 0;
        let escaped = storage;
        while (raw < piece.text.length) {
          const next = escaped + escapeStorage(piece.text[raw]).length;
          if (next > offset) break;
          escaped = next;
          raw += 1;
        }
        return { node: piece.node, offset: raw };
      }
      last = { node: piece.node, offset: piece.text.length };
    } else if (offset < end && offset >= storage) {
      if (piece.atomic) return { node: piece.node.parentNode, offset: [...piece.node.parentNode.childNodes].indexOf(piece.node) };
      inMarkup = true;
    }
    storage = end;
  }
  return last || { node: root, offset: root.childNodes.length };
};

const richText = (editor, serialize) => {
  let lastSelection = null;
  return {
  element: editor,
  get: () => [...editor.childNodes].map(serialize).join(''),
  set: (storage) => {
    const nodes = renderStorage(storage);
    if (!nodes) return;
    const focused = document.activeElement === editor;
    const offset = focused ? caretOffset(editor) : null;
    const before = editor.textContent;
    editor.replaceChildren(...nodes);
    if (focused && offset !== null) placeCaret(editor, movedOffset(before, editor.textContent, offset));
  },
  // The caret is measured in the storage the editor sends, and the last one
  // is kept while the editor does not have the focus.
  selection: () => {
    const current = document.getSelection();
    if (current.rangeCount && editor.contains(current.anchorNode)) {
      const range = current.getRangeAt(0);
      const pieces = storagePieces(editor, serialize);
      const textAt = (container, offset) => {
        const before = document.createRange();
        before.selectNodeContents(editor);
        before.setEnd(container, offset);
        return before.toString().length;
      };
      lastSelection = {
        position: storageOffsetOfText(pieces, textAt(range.startContainer, range.startOffset)),
        end: storageOffsetOfText(pieces, textAt(range.endContainer, range.endOffset)),
      };
    }
    return lastSelection;
  },
  caretRects: (positions) => {
    const pieces = storagePieces(editor, serialize);
    return positions.map((position) => {
      const point = domPointAtStorage(editor, pieces, position);
      const range = document.createRange();
      range.setStart(point.node, point.offset);
      range.collapse(true);
      const box = range.getClientRects()[0];
      if (box && box.height) return { top: box.top, left: box.left, height: box.height };
      // A collapsed place at the edge of a line has no box in some browsers,
      // so the character beside it, or the element holding it, gives it.
      if (point.node.nodeType === Node.TEXT_NODE && point.node.textContent.length) {
        const at = Math.min(point.offset, point.node.textContent.length - 1);
        const beside = document.createRange();
        beside.setStart(point.node, at);
        beside.setEnd(point.node, at + 1);
        const character = beside.getBoundingClientRect();
        return { top: character.top, left: point.offset > at ? character.right : character.left, height: character.height };
      }
      const holder = (point.node.nodeType === Node.TEXT_NODE ? point.node.parentElement : point.node).getBoundingClientRect();
      return { top: holder.top, left: holder.left, height: Math.min(holder.height, 24) || 20 };
    });
  },
  };
};

document.addEventListener('DOMContentLoaded', () => {
  document.querySelectorAll('[data-wiki-live]').forEach(startLive);
  const peopleTemplate = document.querySelector('#wiki-mention-people');
  const people = peopleTemplate ? [...peopleTemplate.content.querySelectorAll('option')].map((option) => ({ id: option.value, name: option.textContent })) : [];
  const picker = people.length ? mentionPicker(people) : null;
  if (picker) document.querySelectorAll('textarea[data-wiki-mentions]').forEach((textarea) => picker.attach(textarea, textareaMentions(textarea)));

  const editor = document.querySelector('[data-wiki-editor]');
  const liveSync = document.querySelector('[data-wiki-live-sync]');
  if (!editor) {
    const textarea = liveSync && liveSync.closest('form').querySelector('textarea[name=body]');
    if (textarea) startLiveEditing(liveSync, textareaText(textarea), liveSync.closest('form'));
    return;
  }
  const form = editor.closest('form');
  const source = form.querySelector('[name=body]');
  const toolbar = form.querySelector('[data-wiki-toolbar]');
  const serialize = (node) => {
    if (node.nodeType === Node.TEXT_NODE) return escapeStorage(node.textContent);
    if (node.nodeType !== Node.ELEMENT_NODE) return '';
    const tag = node.tagName.toLowerCase();
    if (tag === 'a' && (node.getAttribute('href') || '').startsWith('/people/')) {
      return mentionStorage(decodeURIComponent(node.getAttribute('href').slice('/people/'.length)), node.textContent.replace(/^@/, ''));
    }
    const macro = node.dataset ? node.dataset.macro : '';
    if (macro === 'toc') {
      // A table of contents lists the page's own headings; the page it is
      // written back into builds the list again.
      return `<ac:structured-macro ac:name="toc"${macroIDStorage(node)}/>`;
    }
    if (macro === 'status') {
      return `<ac:structured-macro ac:name="status"${macroIDStorage(node)}>` +
        `${macroParameter('colour', node.dataset.colour || 'grey')}${macroParameter('title', node.textContent)}</ac:structured-macro>`;
    }
    if (macro === 'code') {
      return `<ac:structured-macro ac:name="code"${macroIDStorage(node)}>` +
        `${node.dataset.language ? macroParameter('language', node.dataset.language) : ''}` +
        `<ac:plain-text-body>${escapeStorage(node.textContent)}</ac:plain-text-body></ac:structured-macro>`;
    }
    if (panelMacros.includes(macro)) {
      const title = node.querySelector(':scope > .wiki-panel-title');
      const body = [...node.childNodes].filter((child) => child !== title).map(serialize).join('');
      return `<ac:structured-macro ac:name="${macro}"${macroIDStorage(node)}>` +
        `${title && title.textContent.trim() ? macroParameter('title', title.textContent.trim()) : ''}` +
        `<ac:rich-text-body>${body}</ac:rich-text-body></ac:structured-macro>`;
    }
    const content = [...node.childNodes].map(serialize).join('');
    if (macro === 'layout') return `<ac:layout>${content}</ac:layout>`;
    if (node.classList && node.classList.contains('wiki-layout-section')) {
      return `<ac:layout-section ac:type="${escapeStorage(node.dataset.layoutType || 'two_equal')}">${content}</ac:layout-section>`;
    }
    if (node.classList && node.classList.contains('wiki-layout-cell')) return `<ac:layout-cell>${content}</ac:layout-cell>`;
    if (tag === 'span') return content;
    if (tag === 'br' || tag === 'hr') return `<${tag}/>`;
    const normalized = tag === 'div' ? 'p' : tag;
    const href = tag === 'a' ? ` href="${escapeStorage(node.getAttribute('href') || '')}"` : '';
    return `<${normalized}${href}>${content}</${normalized}>`;
  };
  editor.hidden = false;
  source.hidden = true;
  toolbar.hidden = false;
  document.querySelector('#wiki-body-label').removeAttribute('for');
  // Where the caret last was in the editor. A toolbar control that takes the
  // focus -- a link address, a colour, a menu -- leaves the document with no
  // selection inside the editor, and a command with nowhere to go does
  // nothing at all; so the editor is given its own place back before every
  // command runs.
  let caret = null;
  const rememberCaret = () => {
    const selection = document.getSelection();
    if (!selection || selection.rangeCount === 0) return;
    const range = selection.getRangeAt(0);
    if (editor.contains(range.commonAncestorContainer)) caret = range.cloneRange();
  };
  // Typing does not raise a selection change in every browser, so the caret
  // is read after each of the ways it moves, and once more as the editor
  // hands the focus to a toolbar control.
  document.addEventListener('selectionchange', rememberCaret);
  ['keyup', 'mouseup', 'input', 'focusout'].forEach((event) => editor.addEventListener(event, rememberCaret));
  // The block of the page the caret is in: a child of the editor itself, so
  // a panel, a code block or a layout is put between blocks rather than
  // inside a paragraph.
  const blockAt = (node) => {
    let block = node;
    if (block === editor) return editor.lastElementChild;
    while (block && block.parentNode !== editor) block = block.parentNode;
    return block;
  };
  // Inserting through execCommand leaves the browser to decide what the
  // markup means: Chrome flattens a panel into styled spans and drops the
  // attributes that say what the macro is. These put the nodes in as written.
  const insertBlocks = (html) => {
    focusEditor();
    const template = document.createElement('template');
    template.innerHTML = html;
    const nodes = [...template.content.childNodes];
    if (nodes.length === 0) return;
    const selection = document.getSelection();
    const anchor = selection && selection.rangeCount > 0 ? blockAt(selection.getRangeAt(0).startContainer) : null;
    if (anchor) anchor.after(...nodes); else editor.append(...nodes);
    const caretIn = nodes[nodes.length - 1];
    const range = document.createRange();
    range.selectNodeContents(caretIn);
    range.collapse(false);
    if (selection) { selection.removeAllRanges(); selection.addRange(range); }
    caret = range.cloneRange();
  };
  const insertInline = (html) => {
    focusEditor();
    const template = document.createElement('template');
    template.innerHTML = html;
    const node = template.content.firstChild;
    if (!node) return;
    const selection = document.getSelection();
    if (!selection || selection.rangeCount === 0) {
      editor.append(node);
      return;
    }
    const range = selection.getRangeAt(0);
    range.deleteContents();
    range.insertNode(node);
    range.setStartAfter(node);
    range.collapse(true);
    selection.removeAllRanges();
    selection.addRange(range);
    caret = range.cloneRange();
  };
  const focusEditor = () => {
    editor.focus();
    const selection = document.getSelection();
    // The place the person last had the caret is where they mean the command
    // to land. A browser refocusing a box of its own accord puts the caret at
    // the start, which is not where they were writing.
    if (!selection || !caret || !caret.startContainer.isConnected || !editor.contains(caret.commonAncestorContainer)) return;
    selection.removeAllRanges();
    selection.addRange(caret);
  };
  toolbar.querySelectorAll('[data-wiki-command]').forEach((button) => {
    button.addEventListener('mousedown', (event) => event.preventDefault());
    button.addEventListener('click', () => {
      focusEditor();
      document.execCommand(button.dataset.wikiCommand, false, null);
    });
  });
  // A heading, a quote or a code block is the block the caret is in, changed
  // to another block; the select says which, and goes back to reading what
  // the caret is in so it never claims the wrong one.
  const style = toolbar.querySelector('[data-wiki-style]');
  if (style) {
    style.addEventListener('mousedown', (event) => event.stopPropagation());
    style.addEventListener('change', () => {
      focusEditor();
      document.execCommand('formatBlock', false, style.value);
      style.value = 'p';
    });
  }
  // A link is added around what is selected, or as the address itself when
  // nothing is. The address is checked here so nothing but http, https and
  // mailto reaches the page.
  const linkURL = toolbar.querySelector('[data-wiki-link-url]');
  const linkButton = toolbar.querySelector('[data-wiki-link]');
  if (linkURL && linkButton) {
    linkButton.addEventListener('mousedown', (event) => event.preventDefault());
    linkButton.addEventListener('click', () => {
      const address = linkURL.value.trim();
      if (!/^(https?:|mailto:)/i.test(address)) {
        linkURL.setCustomValidity('A link address begins with http, https or mailto.');
        linkURL.reportValidity();
        return;
      }
      linkURL.setCustomValidity('');
      focusEditor();
      const selection = document.getSelection();
      if (selection && !selection.isCollapsed) {
        document.execCommand('createLink', false, address);
      } else {
        document.execCommand('insertHTML', false, `<a href="${escapeStorage(address)}">${escapeStorage(address)}</a>`);
      }
      linkURL.value = '';
    });
  }
  // A table arrives as a header row and a row under it, which is the shape
  // somebody then types into.
  const tableButton = toolbar.querySelector('[data-wiki-table]');
  if (tableButton) {
    tableButton.addEventListener('mousedown', (event) => event.preventDefault());
    tableButton.addEventListener('click', () => {
      insertBlocks('<table><thead><tr><th>Heading</th><th>Heading</th></tr></thead><tbody><tr><td>Cell</td><td>Cell</td></tr></tbody></table><p></p>');
    });
  }
  // A macro arrives as the shape it is drawn in, which somebody then types
  // into: a panel with a title, a status word, a code block or the page's own
  // table of contents.
  const macroSelect = toolbar.querySelector('[data-wiki-macro]');
  const statusColour = toolbar.querySelector('[data-wiki-status-colour]');
  if (macroSelect) {
    macroSelect.addEventListener('mousedown', (event) => event.stopPropagation());
    macroSelect.addEventListener('change', () => {
      const macro = macroSelect.value;
      macroSelect.value = '';
      if (!macro) return;
      const colour = statusColour ? statusColour.value : 'grey';
      const panelTitles = { info: 'Info', note: 'Note', warning: 'Warning', tip: 'Tip', panel: 'Panel' };
      let markup = '';
      if (panelMacros.includes(macro)) {
        markup = `<div class="wiki-panel wiki-panel-${macro}" data-macro="${macro}"><p class="wiki-panel-title">${panelTitles[macro]}</p><p>Write the panel here.</p></div><p></p>`;
      } else if (macro === 'status') {
        markup = `<span class="wiki-status wiki-status-${colour}" data-macro="status" data-colour="${colour}">Status</span>`;
      } else if (macro === 'code') {
        markup = '<pre class="wiki-code" data-macro="code">Write the code here.</pre><p></p>';
      } else if (macro === 'toc') {
        markup = '<nav class="wiki-toc" data-macro="toc" contenteditable="false" aria-label="Contents"><p>Table of contents</p></nav><p></p>';
      }
      if (macro === 'status') insertInline(markup); else insertBlocks(markup);
    });
  }
  // A layout is a row of columns, each of which holds its own content.
  const layoutSelect = toolbar.querySelector('[data-wiki-layout]');
  if (layoutSelect) {
    layoutSelect.addEventListener('mousedown', (event) => event.stopPropagation());
    layoutSelect.addEventListener('change', () => {
      const layout = layoutSelect.value;
      layoutSelect.value = '';
      if (!layout) return;
      const cells = layout.startsWith('three') ? 3 : 2;
      const cell = (index) => `<div class="wiki-layout-cell"><p>Column ${index}</p></div>`;
      const columns = Array.from({ length: cells }, (_, index) => cell(index + 1)).join('');
      insertBlocks(`<div class="wiki-layout" data-macro="layout"><div class="wiki-layout-section wiki-layout-${layout}" data-layout-type="${layout}">${columns}</div></div><p></p>`);
    });
  }
  // A table of contents is built from the page's headings, so it is read in
  // the editor rather than typed into.
  editor.querySelectorAll('[data-macro=toc]').forEach((contents) => { contents.contentEditable = 'false'; });
  const mentionButton = toolbar.querySelector('[data-wiki-mention]');
  if (picker) {
    const adapter = editorMentions(editor);
    picker.attach(editor, adapter);
    mentionButton.addEventListener('mousedown', (event) => event.preventDefault());
    mentionButton.addEventListener('click', () => {
      focusEditor();
      document.execCommand('insertText', false, '@');
      picker.update(editor, adapter);
    });
  } else {
    mentionButton.remove();
  }
  editor.addEventListener('paste', (event) => {
    event.preventDefault();
    document.execCommand('insertText', false, event.clipboardData.getData('text/plain'));
  });
  form.addEventListener('submit', () => {
    source.value = [...editor.childNodes].map(serialize).join('');
  });
  if (liveSync) startLiveEditing(liveSync, richText(editor, serialize), form);
});
