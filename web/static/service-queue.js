// A queue's select-all box selects or clears every request in the queue, and
// shows when only some are selected.
(() => {
  const all = document.querySelector('[data-bulk-all]');
  const boxes = () => [...document.querySelectorAll('[data-bulk-request]')];
  if (!all) return;
  const sync = () => {
    const checked = boxes().filter((box) => box.checked).length;
    all.checked = checked > 0 && checked === boxes().length;
    all.indeterminate = checked > 0 && checked < boxes().length;
  };
  all.addEventListener('change', () => {
    boxes().forEach((box) => { box.checked = all.checked; });
    sync();
  });
  boxes().forEach((box) => box.addEventListener('change', sync));
})();
