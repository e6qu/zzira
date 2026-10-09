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
  // Recheck after the response too: someone may start using a gadget while
  // the request is in flight. Open detail tables are part of that interaction.
  const interacting = grid => document.hidden ||
    document.querySelector('[data-dashboard-editing]') ||
    grid.contains(document.activeElement) || grid.querySelector('details[open]');
  async function refresh(manual) {
    const grid = document.getElementById('dashboard-grid');
    if (!grid || !button || !status || busy) return;
    if (!manual && interacting(grid)) {
      status.textContent = 'Automatic refresh paused while you use this dashboard.';
      schedule();
      return;
    }
    busy = true;
    button.disabled = true;
    grid.setAttribute('aria-busy', 'true');
    status.textContent = 'Refreshing dashboard…';
    const controller = new AbortController();
    const deadline = setTimeout(() => controller.abort(), 15000);
    try {
      const response = await fetch(grid.dataset.contentUrl, { cache: 'no-store', redirect: 'error', signal: controller.signal });
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
      if (!manual && interacting(grid)) {
        status.textContent = 'Automatic refresh paused while you use this dashboard.';
        return;
      }
      grid.replaceWith(updated);
      status.textContent = 'Updated just now.';
    } catch (_) {
      status.textContent = controller.signal.aborted
        ? 'Refresh took too long. Showing the last loaded results. Try Refresh again.'
        : 'Could not refresh. Check your connection or sign in again. Showing the last loaded results.';
    } finally {
      clearTimeout(deadline);
      grid.removeAttribute('aria-busy');
      busy = false;
      button.disabled = false;
      schedule();
    }
  }
  button?.addEventListener('click', () => refresh(true));
  window.addEventListener('pageshow', event => { if (event.persisted) location.reload(); });
  schedule();
})();
