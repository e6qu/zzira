(() => {
  const board = document.querySelector('[data-wallboard]');
  if (!board) return;
  const interval = Math.max(5, Number(board.dataset.interval) || 30) * 1000;
  const refresh = Number(board.dataset.refresh || 0);
  const slides = [...board.querySelectorAll('[data-wallboard-slide]')];
  const button = board.querySelector('[data-wallboard-pause]');
  const status = document.getElementById('wallboard-status');
  const loaded = Date.now();
  let timer;
  const current = () => slides.find(slide => !slide.hidden) || slides[0];
  const showNext = items => {
    const next = (items.findIndex(item => !item.hidden) + 1) % items.length;
    items.forEach((item, index) => { item.hidden = index !== next; });
    return next;
  };
  function rotate() {
    // A slide show shows each dashboard in turn and reloads after a full cycle,
    // so every pass draws the latest work; a single wallboard rotates its
    // colour groups and reloads when the dashboard's refresh interval passes.
    if (slides.length > 1) {
      if (showNext(slides) === 0) { location.reload(); return; }
    } else {
      for (const group of current()?.querySelectorAll('[data-wallboard-group]') || []) {
        const gadgets = [...group.querySelectorAll(':scope > [data-wallboard-gadget]')];
        if (gadgets.length > 1) showNext(gadgets);
      }
      if (refresh > 0 && Date.now() - loaded >= refresh) { location.reload(); return; }
    }
    timer = setTimeout(rotate, interval);
  }
  button?.addEventListener('click', () => {
    const paused = button.getAttribute('aria-pressed') !== 'true';
    button.setAttribute('aria-pressed', String(paused));
    button.textContent = paused ? 'Resume rotation' : 'Pause rotation';
    status.textContent = paused ? 'Rotation paused.' : 'Rotation resumed.';
    clearTimeout(timer);
    if (!paused) timer = setTimeout(rotate, interval);
  });
  if (button || refresh > 0) timer = setTimeout(rotate, interval);
})();
