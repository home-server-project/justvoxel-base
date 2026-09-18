(() => {
  const storageGroup = document.querySelector('[data-nav-group="storage"]');
  storageGroup?.querySelector('.nav-trigger')?.setAttribute('aria-current', 'page');
  document.querySelector('a[href="/settings/storage-provision"]')?.setAttribute('aria-current', 'page');

  const form = document.querySelector('[data-exact-confirmation]');
  if (!form) return;

  const expected = form.dataset.exactConfirmation || '';
  const input = form.querySelector('#storage-confirmation-input');
  const submit = form.querySelector('#storage-provision-apply');
  if (!input || !submit) return;

  const update = () => {
    const matches = input.value === expected;
    submit.disabled = !matches;
    input.setAttribute('aria-invalid', matches || input.value === '' ? 'false' : 'true');
  };

  input.addEventListener('input', update);
  update();
})();
