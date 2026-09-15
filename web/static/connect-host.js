// The site's side of Connect's JavaScript API. A message from an app is
// answered only when it comes from a frame this page placed, from that app's
// own origin, and a request it asks for goes through the site, which keeps it
// within the app's scopes and the permissions of the person using it.
(() => {
  if (window.zziraConnectHost) return;
  window.zziraConnectHost = true;
  const frames = () => [...document.querySelectorAll('iframe[data-app-module]')];
  const originOf = (frame) => {
    try {
      return new URL(frame.dataset.appBase).origin;
    } catch {
      return null;
    }
  };
  const send = (frame, message) => {
    const origin = originOf(frame);
    if (origin && frame.contentWindow) frame.contentWindow.postMessage(message, origin);
  };
  const emit = (frame, name, payload) => send(frame, { zziraConnect: 'event', name, payload });
  const gadgetFrame = (control) => control.closest('.dashboard-gadget')?.querySelector('iframe[data-app-module]');
  const methods = {
    resize: (frame, [, height]) => {
      const pixels = parseInt(height, 10);
      if (Number.isFinite(pixels)) frame.style.height = `${Math.min(Math.max(pixels, 40), 4000)}px`;
    },
    sizeToParent: (frame) => {
      frame.style.height = `${Math.max(frame.parentElement.clientHeight, 200)}px`;
    },
    getContext: (frame) => {
      const jira = {};
      if (frame.dataset.dashboardId) {
        jira.dashboard = { id: frame.dataset.dashboardId };
        jira.dashboardItem = { id: frame.dataset.dashboardItemId, key: frame.dataset.dashboardItemKey };
      }
      return { jira };
    },
    setDashboardItemTitle: (frame, [title]) => {
      const heading = frame.closest('.dashboard-gadget')?.querySelector('h2');
      if (heading && typeof title === 'string' && title.trim()) heading.textContent = title.trim().slice(0, 255);
    },
    isDashboardItemEditable: (frame) => frame.dataset.editable === 'true',
    openDashboardItemEditor: (frame) => {
      if (frame.dataset.editable === 'true') emit(frame, 'jira_dashboard_item_edit', {});
    },
    request: async (frame, [options]) => {
      const response = await fetch(`/app-modules/${encodeURIComponent(frame.dataset.appModule)}/request`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify(options && typeof options === 'object' ? options : {}),
      });
      if (!response.ok) throw new Error((await response.text()).trim() || `The site refused the request (${response.status}).`);
      return response.json();
    },
  };
  window.addEventListener('message', async (event) => {
    const data = event.data;
    if (!data || data.zziraConnect !== 'call' || typeof data.method !== 'string' || !Object.hasOwn(methods, data.method)) return;
    const frame = frames().find((candidate) => candidate.contentWindow === event.source);
    if (!frame || event.origin !== originOf(frame)) return;
    try {
      const result = await methods[data.method](frame, Array.isArray(data.args) ? data.args : []);
      send(frame, { zziraConnect: 'result', id: data.id, result: result === undefined ? null : result });
    } catch (error) {
      send(frame, { zziraConnect: 'result', id: data.id, error: error.message || 'The call failed.' });
    }
  });
  // Dashboard items: Configure asks a configurable item to show its settings,
  // and Refresh reloads an item with a newly signed frame.
  document.addEventListener('click', (event) => {
    const configure = event.target.closest('[data-app-configure]');
    const refresh = event.target.closest('[data-app-refresh]');
    const frame = configure ? gadgetFrame(configure) : refresh ? gadgetFrame(refresh) : null;
    if (!frame) return;
    if (configure) emit(frame, 'jira_dashboard_item_edit', {});
    else frame.src = frame.getAttribute('src');
  });
})();
