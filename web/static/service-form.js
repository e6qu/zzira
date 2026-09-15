(() => {
  // A portal field shown only for some answers appears once one of its
  // options is chosen; while hidden it is disabled, so it is neither required
  // nor sent.
  const form = document.querySelector('[data-service-request-form]');
  if (!form) return;
  const conditional = [...form.querySelectorAll('[data-condition-field]')];
  if (!conditional.length) return;
  const chosen = fieldID => [...form.querySelectorAll(`[name="field_${CSS.escape(fieldID)}"]`)]
    .flatMap(control => control.type === 'checkbox' ? (control.checked ? [control.value] : []) : (control.value ? [control.value] : []));
  const update = () => {
    for (const wrapper of conditional) {
      const wanted = wrapper.dataset.conditionOptions.split(' ');
      const shown = chosen(wrapper.dataset.conditionField).some(id => wanted.includes(id));
      wrapper.hidden = !shown;
      for (const control of wrapper.querySelectorAll('input, select, textarea')) control.disabled = !shown;
    }
  };
  form.addEventListener('change', update);
  update();
})();
