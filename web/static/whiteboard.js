(() => {
  // An object on the canvas is dragged where it belongs, and the move is
  // saved the moment it is let go. Everything the drag can do is also on the
  // form below the canvas, which is what a keyboard uses.
  const canvas = document.querySelector('[data-whiteboard-canvas]');
  if (!canvas || !window.PointerEvent) return;
  const status = document.querySelector('[data-whiteboard-status]');
  const say = message => { if (status) status.textContent = message; };
  const width = 1400;
  const height = 800;
  let saving = false;

  // canvasPoint reads where a pointer is in the canvas's own coordinates,
  // whatever size the viewport draws it at.
  const canvasPoint = event => {
    const box = canvas.getBoundingClientRect();
    return {
      x: ((event.clientX - box.left) / box.width) * width,
      y: ((event.clientY - box.top) / box.height) * height,
    };
  };

  const place = (group, x, y) => {
    group.dataset.x = String(x);
    group.dataset.y = String(y);
    for (const child of group.querySelectorAll('rect, foreignObject')) {
      child.setAttribute('x', String(x));
      child.setAttribute('y', String(y));
    }
    // A connector drawn to this object follows it while it moves.
    for (const line of canvas.querySelectorAll(`[data-from="${CSS.escape(group.dataset.whiteboardObject)}"]`)) {
      line.setAttribute('x1', String(x + Number(group.dataset.width) / 2));
      line.setAttribute('y1', String(y + Number(group.dataset.height) / 2));
    }
    for (const line of canvas.querySelectorAll(`[data-to="${CSS.escape(group.dataset.whiteboardObject)}"]`)) {
      line.setAttribute('x2', String(x + Number(group.dataset.width) / 2));
      line.setAttribute('y2', String(y + Number(group.dataset.height) / 2));
    }
  };

  const save = async group => {
    const body = new URLSearchParams({
      type: group.dataset.type,
      title: group.dataset.title,
      body: group.dataset.body,
      color: group.dataset.color,
      x: group.dataset.x,
      y: group.dataset.y,
      width: group.dataset.width,
      height: group.dataset.height,
    });
    const path = `${canvas.dataset.save}/${encodeURIComponent(group.dataset.whiteboardObject)}`;
    const form = Array.from(document.querySelectorAll('.wiki-whiteboard-object-list form'))
      .find(form => new URL(form.action).pathname === path);
    const controls = form ? Array.from(form.closest('article').querySelectorAll('input, select, textarea, button'))
      .filter(control => !control.disabled) : [];
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 15000);
    // Editing or deleting this object must wait for its pending move.
    controls.forEach(control => { control.disabled = true; });
    saving = true;
    canvas.setAttribute('aria-busy', 'true');
    say(`Saving ${group.dataset.title || 'object'}…`);
    try {
      const response = await fetch(path, {
        method: 'POST',
        redirect: 'error',
        signal: controller.signal,
        headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'X-Requested-With': 'zzira' },
        body: body.toString(),
      });
      // A successful mutation acknowledges the save without fetching a page.
      // A sign-in page must never be mistaken for a saved move.
      if (response.status !== 204) throw new Error('Move not acknowledged');
      if (form) {
        form.elements.namedItem('x').value = group.dataset.x;
        form.elements.namedItem('y').value = group.dataset.y;
      }
      say(`${group.dataset.title || 'Object'} moved to ${group.dataset.x}, ${group.dataset.y}.`);
      return true;
    } catch (_) {
      say('That move could not be saved. Check your connection or access, then try again.');
      return false;
    } finally {
      clearTimeout(timeout);
      controls.forEach(control => { control.disabled = false; });
      saving = false;
      canvas.removeAttribute('aria-busy');
    }
  };

  let dragging = null;
  canvas.addEventListener('pointerdown', event => {
    if (event.button !== 0 || !event.isPrimary || dragging) return;
    const group = event.target.closest('[data-whiteboard-object]');
    if (!group) return;
    // A second drag could otherwise race the save and its rollback.
    if (saving) return;
    const point = canvasPoint(event);
    dragging = {
      group,
      pointerId: event.pointerId,
      offsetX: point.x - Number(group.dataset.x),
      offsetY: point.y - Number(group.dataset.y),
      from: { x: Number(group.dataset.x), y: Number(group.dataset.y) },
    };
    group.classList.add('wiki-canvas-object-dragging');
    canvas.setPointerCapture(event.pointerId);
    event.preventDefault();
  });

  canvas.addEventListener('pointermove', event => {
    if (!dragging || event.pointerId !== dragging.pointerId) return;
    const point = canvasPoint(event);
    const objectWidth = Number(dragging.group.dataset.width);
    const objectHeight = Number(dragging.group.dataset.height);
    const x = Math.round(Math.min(Math.max(point.x - dragging.offsetX, 0), Math.max(0, width - objectWidth)));
    const y = Math.round(Math.min(Math.max(point.y - dragging.offsetY, 0), Math.max(0, height - objectHeight)));
    place(dragging.group, x, y);
  });

  const release = async (event, canceled = false) => {
    if (!dragging || event.pointerId !== dragging.pointerId) return;
    const { group, from } = dragging;
    dragging = null;
    group.classList.remove('wiki-canvas-object-dragging');
    if (canvas.hasPointerCapture(event.pointerId)) canvas.releasePointerCapture(event.pointerId);
    if (canceled) {
      place(group, from.x, from.y);
      say('Move canceled.');
      return;
    }
    if (group.dataset.x === String(from.x) && group.dataset.y === String(from.y)) return;
    if (!(await save(group))) place(group, from.x, from.y);
  };
  canvas.addEventListener('pointerup', release);
  canvas.addEventListener('pointercancel', event => release(event, true));
  canvas.addEventListener('lostpointercapture', event => release(event, true));
})();
