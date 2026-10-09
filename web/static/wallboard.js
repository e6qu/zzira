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
  const motion = matchMedia('(prefers-reduced-motion: reduce)');
  const action = button?.dataset.wallboardAction || 'rotation';
  let paused = motion.matches;
  const setPaused = (value, reason = '') => {
    paused = value;
    clearTimeout(timer);
    if (button) {
      button.setAttribute('aria-pressed', String(paused));
      button.textContent = `${paused ? 'Resume' : 'Pause'} ${action}`;
    }
    if (status) status.textContent = `${action === 'refresh' ? 'Refresh' : 'Rotation'} ${paused ? 'paused' : 'resumed'}.${reason}`;
    if (!paused) timer = setTimeout(rotate, interval);
  };
  const current = () => slides.find(slide => !slide.hidden) || slides[0];
  const showNext = items => {
    const next = (items.findIndex(item => !item.hidden) + 1) % items.length;
    items.forEach((item, index) => { item.hidden = index !== next; });
    return next;
  };
  function rotate() {
    if (paused) return;
    // A focused gadget or expanded table must not disappear beneath someone
    // using it. Header controls remain available while updates are deferred.
    if (document.hidden || document.activeElement?.closest('[data-wallboard-gadget]') ||
        current()?.querySelector('[data-wallboard-gadget] details[open]')) {
      if (status) status.textContent = 'Updates paused while you use this wallboard.';
      timer = setTimeout(rotate, interval);
      return;
    }
    if (status) status.textContent = '';
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
  button?.addEventListener('click', () => setPaused(!paused));
  motion.addEventListener('change', event => {
    if (event.matches && (button || refresh > 0)) setPaused(true, ' Reduced motion is preferred.');
  });
  if (button || refresh > 0) {
    if (paused) setPaused(true, ' Reduced motion is preferred.');
    else timer = setTimeout(rotate, interval);
  }
})();
