// The storage editor serializes only the markup supported by both renderers.
document.addEventListener('DOMContentLoaded', () => {
  const editor = document.querySelector('[data-wiki-editor]');
  if (!editor) return;
  const form = editor.closest('form');
  const source = form.querySelector('[name=body]');
  const toolbar = form.querySelector('[data-wiki-toolbar]');
  const escape = (text) => text.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;');
  const serialize = (node) => {
    if (node.nodeType === Node.TEXT_NODE) return escape(node.textContent);
    if (node.nodeType !== Node.ELEMENT_NODE) return '';
    const tag = node.tagName.toLowerCase();
    const content = [...node.childNodes].map(serialize).join('');
    if (tag === 'span') return content;
    if (tag === 'br' || tag === 'hr') return `<${tag}/>`;
    const normalized = tag === 'div' ? 'p' : tag;
    const href = tag === 'a' ? ` href="${escape(node.getAttribute('href') || '')}"` : '';
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
  editor.addEventListener('paste', (event) => {
    event.preventDefault();
    document.execCommand('insertText', false, event.clipboardData.getData('text/plain'));
  });
  form.addEventListener('submit', () => {
    source.value = [...editor.childNodes].map(serialize).join('');
  });
});
