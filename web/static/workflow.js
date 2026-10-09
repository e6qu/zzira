(() => {
  const namespace = 'http://www.w3.org/2000/svg';

  document.querySelectorAll('.workflow-map').forEach((map) => {
    const svg = map.querySelector('.workflow-routes');
    const editable = map.dataset.editable === 'true';
    const nodes = new Map(Array.from(map.querySelectorAll('.workflow-node')).map((node) => [node.dataset.statusId, node]));
    const edgeData = Array.from(map.querySelectorAll('.workflow-edge-data'));
    const state = map.querySelector('.workflow-save-state');
    let drawFrame = 0;
    let saveInFlight = false;
    const queuedSaves = new Map();
    const failedStatuses = new Set();
    const keyboardTimers = new Map();
    const confirmed = new Map(Array.from(nodes, ([id, node]) => [id, {
      x: parseFloat(node.style.left), y: parseFloat(node.style.top),
    }]));
    const mutationControls = Array.from(document.querySelectorAll('.workflow-publish button, .workflow-node form button, .workflow-inspector form button'))
      .filter(button => !button.disabled);
    const busy = () => saveInFlight || queuedSaves.size > 0 || keyboardTimers.size > 0;
    const updateBusy = () => {
      map.setAttribute('aria-busy', String(busy()));
      mutationControls.forEach(button => { button.disabled = busy(); });
    };
    document.addEventListener('submit', event => {
      if (busy() && event.target.closest('.workflow-publish, .workflow-node, .workflow-inspector')) event.preventDefault();
    });
    let stateTimer = 0;

    const svgElement = (name, attributes = {}) => {
      const element = document.createElementNS(namespace, name);
      Object.entries(attributes).forEach(([key, value]) => element.setAttribute(key, value));
      return element;
    };

    const scheduleDraw = () => {
      cancelAnimationFrame(drawFrame);
      drawFrame = requestAnimationFrame(drawRoutes);
    };

    const drawRoutes = () => {
      svg.replaceChildren();
      const definitions = svgElement('defs');
      const marker = svgElement('marker', { id: 'workflow-arrow', viewBox: '0 0 10 10', refX: '8', refY: '5', markerWidth: '6', markerHeight: '6', orient: 'auto-start-reverse' });
      marker.append(svgElement('path', { d: 'M 0 0 L 10 5 L 0 10 z', fill: 'context-stroke' }));
      definitions.append(marker);
      svg.append(definitions);
      const mapBox = map.getBoundingClientRect();

      edgeData.forEach((edge) => {
        const from = nodes.get(edge.dataset.from);
        const to = nodes.get(edge.dataset.to);
        if (!from || !to) return;
        const fromBox = from.getBoundingClientRect();
        const toBox = to.getBoundingClientRect();
        const fromCenter = { x: fromBox.left - mapBox.left + fromBox.width / 2, y: fromBox.top - mapBox.top + fromBox.height / 2 };
        const toCenter = { x: toBox.left - mapBox.left + toBox.width / 2, y: toBox.top - mapBox.top + toBox.height / 2 };
        let pathData = '';
        let loop = false;

        if (from === to) {
          loop = true;
          const right = fromBox.right - mapBox.left;
          const top = fromBox.top - mapBox.top;
          pathData = `M ${right - 28} ${top + 4} C ${right + 55} ${top - 55}, ${right + 55} ${top + 65}, ${right - 4} ${top + 52}`;
        } else {
          const goingRight = toCenter.x >= fromCenter.x;
          const startX = goingRight ? fromBox.right - mapBox.left : fromBox.left - mapBox.left;
          const endX = goingRight ? toBox.left - mapBox.left : toBox.right - mapBox.left;
          const startY = fromCenter.y;
          const endY = toCenter.y;
          const bend = Math.max(70, Math.abs(endX - startX) * 0.48);
          const firstControl = startX + (goingRight ? bend : -bend);
          const secondControl = endX - (goingRight ? bend : -bend);
          pathData = `M ${startX} ${startY} C ${firstControl} ${startY}, ${secondControl} ${endY}, ${endX} ${endY}`;
        }
        svg.append(svgElement('path', { d: pathData, class: `workflow-route${loop ? ' is-loop' : ''}`, 'marker-end': 'url(#workflow-arrow)' }));
      });
    };

    const showState = (message, error = false, temporary = true) => {
      clearTimeout(stateTimer);
      state.textContent = message;
      state.hidden = false;
      state.classList.toggle('is-error', error);
      if (!error && temporary) stateTimer = window.setTimeout(() => { state.hidden = true; }, 2200);
    };

    const revealDraftControls = () => {
      const controls = document.querySelector('.workflow-publish[data-layout-draft]');
      if (controls) controls.hidden = false;
      const badge = document.querySelector('.workflow-editor-header .lozenge-success');
      if (badge) {
        badge.classList.remove('lozenge-success');
        badge.classList.add('lozenge-current');
        badge.textContent = 'Draft changes';
      }
    };

    const failureMessage = () => {
      const names = Array.from(failedStatuses, id => nodes.get(id).querySelector('h3').textContent.trim());
      return `Position was not saved. Unsaved statuses: ${names.join(', ')}. Check your connection or access, then move those statuses again.`;
    };

    const persistQueuedPosition = async () => {
      if (saveInFlight || !queuedSaves.size) return;
      saveInFlight = true;
      updateBusy();
      while (queuedSaves.size) {
        const [id, save] = queuedSaves.entries().next().value;
        queuedSaves.delete(id);
        showState('Saving position…', false, false);
        const controller = new AbortController();
        const timeout = setTimeout(() => controller.abort(), 15000);
        try {
          const body = new URLSearchParams({ status: id, x: String(save.x), y: String(save.y) });
          const response = await fetch(map.dataset.layoutUrl, {
            method: 'POST', credentials: 'same-origin', redirect: 'error', signal: controller.signal,
            headers: { 'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8' }, body,
          });
          if (response.status !== 204) throw new Error('Position not acknowledged');
          confirmed.set(id, save);
          failedStatuses.delete(id);
          showState('Position saved to draft');
          revealDraftControls();
        } catch (_) {
          failedStatuses.add(id);
          // A newer queued edit owns the visible position until it is saved.
          if (!queuedSaves.has(id) && !keyboardTimers.has(nodes.get(id))) {
            const previous = confirmed.get(id);
            moveNode(nodes.get(id), previous.x, previous.y);
          }
          showState(failureMessage(), true);
        } finally {
          clearTimeout(timeout);
        }
      }
      saveInFlight = false;
      updateBusy();
      // Saving another status must not hide an earlier refusal.
      if (failedStatuses.size) showState(failureMessage(), true);
    };

    const savePosition = (node) => {
      // Coalesce each status independently; another status must never replace it.
      queuedSaves.set(node.dataset.statusId, {
        x: Math.round(parseFloat(node.style.left)), y: Math.round(parseFloat(node.style.top)),
      });
      updateBusy();
      void persistQueuedPosition();
    };

    const moveNode = (node, x, y) => {
      const maxX = Math.max(12, map.clientWidth - node.offsetWidth - 12);
      const maxY = Math.max(12, map.clientHeight - node.offsetHeight - 12);
      node.style.left = `${Math.min(maxX, Math.max(12, x))}px`;
      node.style.top = `${Math.min(maxY, Math.max(12, y))}px`;
      scheduleDraw();
    };

    if (editable) {
      nodes.forEach((node) => {
        const handle = node.querySelector('header');
        let drag = null;

        handle.addEventListener('pointerdown', (event) => {
          if (event.button !== 0 || !event.isPrimary || busy() || map.querySelector('.is-moving') || event.target.closest('button,a,input,select,textarea')) return;
          drag = { pointerId: event.pointerId, clientX: event.clientX, clientY: event.clientY, left: parseFloat(node.style.left), top: parseFloat(node.style.top) };
          handle.setPointerCapture(event.pointerId);
          node.classList.add('is-moving');
          node.focus();
          event.preventDefault();
        });
        handle.addEventListener('pointermove', (event) => {
          if (!drag || event.pointerId !== drag.pointerId) return;
          moveNode(node, drag.left + event.clientX - drag.clientX, drag.top + event.clientY - drag.clientY);
        });
        const finishDrag = (event, save) => {
          if (!drag || event.pointerId !== drag.pointerId) return;
          const original = drag;
          drag = null;
          node.classList.remove('is-moving');
          if (handle.hasPointerCapture(event.pointerId)) handle.releasePointerCapture(event.pointerId);
          if (!save) moveNode(node, original.left, original.top);
          else if (parseFloat(node.style.left) !== original.left || parseFloat(node.style.top) !== original.top) savePosition(node);
        };
        handle.addEventListener('pointerup', (event) => finishDrag(event, true));
        handle.addEventListener('pointercancel', (event) => finishDrag(event, false));
        handle.addEventListener('lostpointercapture', (event) => finishDrag(event, false));

        node.addEventListener('keydown', (event) => {
          if (event.target !== node || drag) return;
          const directions = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] };
          const direction = directions[event.key];
          if (!direction) return;
          const step = event.shiftKey ? 4 : 16;
          moveNode(node, parseFloat(node.style.left) + direction[0] * step, parseFloat(node.style.top) + direction[1] * step);
          clearTimeout(keyboardTimers.get(node));
          keyboardTimers.set(node, window.setTimeout(() => {
            keyboardTimers.delete(node);
            savePosition(node);
          }, 250));
          showState('Saving position…', false, false);
          updateBusy();
          event.preventDefault();
        });
      });
    }

    new ResizeObserver(scheduleDraw).observe(map);
    scheduleDraw();
  });
})();
