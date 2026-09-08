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
    let queuedSave = null;
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

    const showState = (message, error = false) => {
      clearTimeout(stateTimer);
      state.textContent = message;
      state.hidden = false;
      state.classList.toggle('is-error', error);
      if (!error) stateTimer = window.setTimeout(() => { state.hidden = true; }, 2200);
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

    const persistQueuedPosition = async () => {
      if (saveInFlight || !queuedSave) return;
      saveInFlight = true;
      while (queuedSave) {
        const save = queuedSave;
        queuedSave = null;
        showState('Saving position…');
        try {
          const body = new URLSearchParams({ status: save.status, x: String(save.x), y: String(save.y) });
          const response = await fetch(map.dataset.layoutUrl, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8' }, body });
          if (!response.ok) throw new Error(await response.text());
          showState('Position saved to draft');
          revealDraftControls();
        } catch (error) {
          showState(error.message || 'Position was not saved', true);
        }
      }
      saveInFlight = false;
    };

    const savePosition = (node) => {
      queuedSave = { status: node.dataset.statusId, x: Math.round(parseFloat(node.style.left)), y: Math.round(parseFloat(node.style.top)) };
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
        let keyboardTimer = 0;

        handle.addEventListener('pointerdown', (event) => {
          if (event.button !== 0 || event.target.closest('button,a,input,select,textarea')) return;
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
          if (save) savePosition(node);
          else moveNode(node, original.left, original.top);
        };
        handle.addEventListener('pointerup', (event) => finishDrag(event, true));
        handle.addEventListener('pointercancel', (event) => finishDrag(event, false));

        node.addEventListener('keydown', (event) => {
          const directions = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] };
          const direction = directions[event.key];
          if (!direction) return;
          const step = event.shiftKey ? 4 : 16;
          moveNode(node, parseFloat(node.style.left) + direction[0] * step, parseFloat(node.style.top) + direction[1] * step);
          clearTimeout(keyboardTimer);
          keyboardTimer = window.setTimeout(() => savePosition(node), 250);
          event.preventDefault();
        });
      });
    }

    new ResizeObserver(scheduleDraw).observe(map);
    scheduleDraw();
  });
})();
