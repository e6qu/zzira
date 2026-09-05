(() => {
  let busy = false;
  let timer;
  const status = document.getElementById('dashboard-refresh-status');
  const button = document.querySelector('[data-dashboard-refresh]');
  const schedule = () => {
    clearTimeout(timer);
    const ms = Number(document.getElementById('dashboard-grid')?.dataset.refresh || 0);
    if (ms > 0) timer = setTimeout(() => refresh(false), ms);
  };
  async function refresh(manual) {
    const grid = document.getElementById('dashboard-grid');
    if (!grid || busy) return;
    if (!manual && (document.hidden || document.querySelector('[data-dashboard-editing]') || grid.contains(document.activeElement))) { schedule(); return; }
    busy = true;
    button.disabled = true;
    try {
      const response = await fetch(grid.dataset.contentUrl, { cache: 'no-store', redirect: 'error' });
      if (response.status === 403 || response.status === 404 || response.status === 401) {
        grid.replaceChildren();
        status.textContent = 'This dashboard is no longer available. Return to Dashboards to choose another.';
        grid.dataset.refresh = '0';
        return;
      }
      if (!response.ok) throw new Error('refresh');
      const doc = new DOMParser().parseFromString(await response.text(), 'text/html');
      const updated = doc.getElementById('dashboard-grid');
      if (!updated) throw new Error('refresh');
      grid.replaceWith(updated);
      status.textContent = 'Updated just now.';
    } catch (_) {
      status.textContent = 'Could not refresh. Check your connection or sign in again. Showing the last loaded results.';
    } finally {
      busy = false;
      button.disabled = false;
      schedule();
    }
  }
  button?.addEventListener('click', () => refresh(true));
  window.addEventListener('pageshow', event => { if (event.persisted) location.reload(); });
  schedule();
})();
