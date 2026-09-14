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

document.addEventListener('DOMContentLoaded', () => {
  const peopleTemplate = document.querySelector('#wiki-mention-people');
  const people = peopleTemplate ? [...peopleTemplate.content.querySelectorAll('option')].map((option) => ({ id: option.value, name: option.textContent })) : [];
  const picker = people.length ? mentionPicker(people) : null;
  if (picker) document.querySelectorAll('textarea[data-wiki-mentions]').forEach((textarea) => picker.attach(textarea, textareaMentions(textarea)));

  const editor = document.querySelector('[data-wiki-editor]');
  if (!editor) return;
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
});
