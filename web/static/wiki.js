// The storage editor serializes only the markup supported by both renderers.
const escapeStorage = (text) => text.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;');
const mentionStorage = (id, name) => `<ac:link><ri:user ri:account-id="${escapeStorage(id)}" /><ac:plain-text-link-body>${escapeStorage(name)}</ac:plain-text-link-body></ac:link>`;
const mentionQuery = /(^|\s)@([^\s@<>]{0,40})$/;

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

const startLiveEditing = (status, text, form) => {
  let session = '';
  let revision = -1;
  let synced = null;
  let initial = text.get();
  let busy = false;
  let composing = false;
  let stopped = false;
  let offline = false;
  const say = (message) => {
    status.hidden = false;
    if (status.textContent !== message) status.textContent = message;
  };
  const sync = async () => {
    if (busy || composing || stopped) return;
    busy = true;
    try {
      const sent = text.get();
      const change = synced === null ? null : liveDiff(synced, sent);
      const response = await fetch(status.dataset.liveUrl, {
        method: 'POST', credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({ session, revision, changes: change ? [change] : [] }),
      });
      if (!response.ok) throw new Error(`live editing answered ${response.status}`);
      const state = await response.json();
      if (stopped) return;
      const current = text.get();
      if (typeof state.body === 'string') {
        // The whole document: this editor is new, restarted or far behind.
        const base = synced === null ? initial : synced;
        const merged = liveMerge(base, current, [liveDiff(base, state.body)].filter(Boolean));
        synced = state.body;
        if (merged.local !== current) text.set(merged.local);
      } else if (state.applied) {
        synced = sent;
      } else if (state.changes.length) {
        const merged = liveMerge(synced, current, state.changes);
        synced = merged.synced;
        if (merged.local !== current) text.set(merged.local);
      }
      session = state.session;
      revision = state.revision;
      // Publishing saves the version after the one the session started from,
      // which moves on when someone else publishes meanwhile.
      const version = form.querySelector('input[name=version]');
      if (version && state.version) version.value = String(state.version + 1);
      if (offline || status.hidden) say('Live editing is on: changes merge with everyone editing this page as you type.');
      offline = false;
      if (liveDiff(synced, text.get())) window.setTimeout(sync, 50);
    } catch {
      if (!offline) say('Live editing is reconnecting. Your changes are kept and merge when it is back.');
      offline = true;
    } finally {
      busy = false;
    }
  };
  let pending = 0;
  const soon = () => {
    window.clearTimeout(pending);
    pending = window.setTimeout(sync, 250);
  };
  text.element.addEventListener('input', soon);
  text.element.addEventListener('compositionstart', () => { composing = true; });
  text.element.addEventListener('compositionend', () => { composing = false; soon(); });
  const timer = window.setInterval(sync, 1000);
  form.addEventListener('submit', () => { stopped = true; window.clearInterval(timer); });
  window.addEventListener('pagehide', () => window.clearInterval(timer));
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
});

const richText = (editor, serialize) => ({
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
});

document.addEventListener('DOMContentLoaded', () => {
  document.querySelectorAll('[data-wiki-live]').forEach(startLive);
  const peopleTemplate = document.querySelector('#wiki-mention-people');
  const people = peopleTemplate ? [...peopleTemplate.content.querySelectorAll('option')].map((option) => ({ id: option.value, name: option.textContent })) : [];
  const picker = people.length ? mentionPicker(people) : null;
  if (picker) document.querySelectorAll('textarea[data-wiki-mentions]').forEach((textarea) => picker.attach(textarea, textareaMentions(textarea)));

  const editor = document.querySelector('[data-wiki-editor]');
  const liveSync = document.querySelector('[data-wiki-live-sync]');
  if (!editor) {
    const textarea = liveSync && liveSync.closest('form').querySelector('#wiki-body');
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
    const content = [...node.childNodes].map(serialize).join('');
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
  toolbar.querySelectorAll('[data-wiki-command]').forEach((button) => {
    button.addEventListener('mousedown', (event) => event.preventDefault());
    button.addEventListener('click', () => {
      editor.focus();
      document.execCommand(button.dataset.wikiCommand, false, null);
    });
  });
  const mentionButton = toolbar.querySelector('[data-wiki-mention]');
  if (picker) {
    const adapter = editorMentions(editor);
    picker.attach(editor, adapter);
    mentionButton.addEventListener('mousedown', (event) => event.preventDefault());
    mentionButton.addEventListener('click', () => {
      editor.focus();
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
