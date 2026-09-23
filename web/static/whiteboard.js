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
    const response = await fetch(`${canvas.dataset.save}/${encodeURIComponent(group.dataset.whiteboardObject)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'X-Requested-With': 'zzira' },
      body: body.toString(),
    });
    if (!response.ok) {
      say('That move could not be saved.');
      return false;
    }
    say(`${group.dataset.title} moved to ${group.dataset.x}, ${group.dataset.y}.`);
    return true;
  };

  let dragging = null;
  canvas.addEventListener('pointerdown', event => {
    const group = event.target.closest('[data-whiteboard-object]');
    if (!group) return;
    const point = canvasPoint(event);
    dragging = {
      group,
      offsetX: point.x - Number(group.dataset.x),
      offsetY: point.y - Number(group.dataset.y),
      from: { x: Number(group.dataset.x), y: Number(group.dataset.y) },
    };
    group.classList.add('wiki-canvas-object-dragging');
    canvas.setPointerCapture(event.pointerId);
    event.preventDefault();
  });

  canvas.addEventListener('pointermove', event => {
    if (!dragging) return;
    const point = canvasPoint(event);
    const objectWidth = Number(dragging.group.dataset.width);
    const objectHeight = Number(dragging.group.dataset.height);
    const x = Math.round(Math.min(Math.max(point.x - dragging.offsetX, 0), width - objectWidth));
    const y = Math.round(Math.min(Math.max(point.y - dragging.offsetY, 0), height - objectHeight));
    place(dragging.group, x, y);
  });

  const release = async event => {
    if (!dragging) return;
    const { group, from } = dragging;
    dragging = null;
    group.classList.remove('wiki-canvas-object-dragging');
    if (canvas.hasPointerCapture(event.pointerId)) canvas.releasePointerCapture(event.pointerId);
    if (group.dataset.x === String(from.x) && group.dataset.y === String(from.y)) return;
    if (!(await save(group))) place(group, from.x, from.y);
  };
  canvas.addEventListener('pointerup', release);
  canvas.addEventListener('pointercancel', release);
})();
