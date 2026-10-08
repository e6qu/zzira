(() => {
  // A portal field shown only for some answers appears once one of its
  // options is chosen; while hidden it is disabled, so it is neither required
  // nor sent.
  const form = document.querySelector('[data-service-request-form]');
  if (!form) return;
  const conditional = [...form.querySelectorAll('[data-condition-field]')];
  const cascading = [...form.querySelectorAll('[data-cascade-parent]')];
  const chosen = fieldID => [...form.querySelectorAll(`[name="field_${CSS.escape(fieldID)}"]`)]
    .flatMap(control => control.type === 'checkbox' ? (control.checked ? [control.value] : []) : (control.value ? [control.value] : []));
  const update = () => {
    for (const wrapper of conditional) {
      const wanted = wrapper.dataset.conditionOptions.split(' ');
      const shown = chosen(wrapper.dataset.conditionField).some(id => wanted.includes(id));
      wrapper.hidden = !shown;
      for (const control of wrapper.querySelectorAll('input, select, textarea')) control.disabled = !shown;
    }
    for (const detail of cascading) {
      const parent = document.getElementById(detail.dataset.cascadeParent);
      if (!parent) continue;
      let hasDetails = false;
      for (const group of detail.querySelectorAll('[data-parent-option]')) {
        const offered = group.dataset.parentOption === parent.value;
        group.hidden = !offered;
        group.disabled = !offered;
        hasDetails ||= offered;
        for (const option of group.querySelectorAll('option')) option.disabled = !offered;
      }
      // Changing the parent must not submit a detail from the previous branch.
      if (detail.selectedOptions[0]?.disabled) detail.value = '';
      detail.disabled = parent.disabled || !hasDetails;
    }
  };
  form.addEventListener('change', update);
  update();
})();
