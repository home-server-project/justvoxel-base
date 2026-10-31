(() => {
  'use strict';
  const request = document.querySelector('form[action="/login"] input[name="oauth_request"]')?.value;
  if (!/^jvoar_[A-Za-z0-9_-]{43}$/.test(request || '')) return;
  // A cross-site OAuth navigation does not send existing SameSite=Strict cookies.
  // Check again from the normal same-origin login document before prompting the user to sign in.
  fetch('/api/session-info', {credentials: 'same-origin', cache: 'no-store', headers: {Accept: 'application/json'}})
    .then(response => {
      if (response.ok && !response.redirected) location.replace('/oauth/authorize?request=' + encodeURIComponent(request));
    })
    .catch(() => {}); // The normal login form remains usable if session lookup fails.
})();
