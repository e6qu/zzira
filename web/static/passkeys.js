(() => {
  // Registering a security key and answering a sign-in with one: the page
  // asks the site for the challenge, hands it to the browser's credentials
  // API, and posts back exactly what the key answered. Nothing here decides
  // whether an answer is good; the site does that.
  const fromBase64URL = value => Uint8Array.from(atob(value.replace(/-/g, '+').replace(/_/g, '/')), c => c.charCodeAt(0));
  const toBase64URL = buffer => btoa(String.fromCharCode(...new Uint8Array(buffer))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');

  const say = (element, message, kind) => {
    if (!element) return;
    element.textContent = message;
    element.hidden = !message;
    element.className = kind === 'error' ? 'form-error' : 'success-banner';
  };

  const register = async (button) => {
    const status = document.querySelector('[data-passkey-status]');
    const label = document.querySelector('[data-passkey-label]');
    if (!window.PublicKeyCredential) {
      say(status, 'This browser cannot use security keys.', 'error');
      return;
    }
    button.disabled = true;
    try {
      const response = await fetch('/profile/passkeys/options', { method: 'POST', headers: { 'X-Requested-With': 'zzira' } });
      if (!response.ok) throw new Error(await response.text());
      const options = await response.json();
      const created = await navigator.credentials.create({
        publicKey: {
          ...options,
          challenge: fromBase64URL(options.challenge),
          user: { ...options.user, id: fromBase64URL(options.user.id) },
          excludeCredentials: (options.excludeCredentials || []).map(credential => ({ ...credential, id: fromBase64URL(credential.id) })),
        },
      });
      const kept = await fetch('/profile/passkeys', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'zzira' },
        body: JSON.stringify({
          rawId: toBase64URL(created.rawId),
          label: label && label.value ? label.value : '',
          response: {
            clientDataJSON: toBase64URL(created.response.clientDataJSON),
            attestationObject: toBase64URL(created.response.attestationObject),
          },
        }),
      });
      if (!kept.ok) throw new Error(await kept.text());
      location.reload();
    } catch (error) {
      say(status, error && error.message ? error.message : 'That key could not be registered.', 'error');
      button.disabled = false;
    }
  };

  const signIn = async (button) => {
    const status = document.querySelector('[data-passkey-signin-status]');
    if (!window.PublicKeyCredential) {
      say(status, 'This browser cannot use security keys. Use your authenticator app.', 'error');
      return;
    }
    button.disabled = true;
    try {
      const response = await fetch('/login/verify/passkey/options', { method: 'POST', headers: { 'X-Requested-With': 'zzira' } });
      if (!response.ok) throw new Error(await response.text());
      const options = await response.json();
      const answered = await navigator.credentials.get({
        publicKey: {
          ...options,
          challenge: fromBase64URL(options.challenge),
          allowCredentials: (options.allowCredentials || []).map(credential => ({ ...credential, id: fromBase64URL(credential.id) })),
        },
      });
      const finished = await fetch('/login/verify/passkey', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'zzira' },
        body: JSON.stringify({
          rawId: toBase64URL(answered.rawId),
          response: {
            clientDataJSON: toBase64URL(answered.response.clientDataJSON),
            authenticatorData: toBase64URL(answered.response.authenticatorData),
            signature: toBase64URL(answered.response.signature),
          },
        }),
      });
      if (!finished.ok) throw new Error(await finished.text());
      location.assign('/');
    } catch (error) {
      say(status, error && error.message ? error.message : 'That key could not answer the sign-in.', 'error');
      button.disabled = false;
    }
  };

  document.addEventListener('click', event => {
    const registerButton = event.target.closest('[data-passkey-register]');
    if (registerButton) {
      event.preventDefault();
      void register(registerButton);
      return;
    }
    const signInButton = event.target.closest('[data-passkey-signin]');
    if (signInButton) {
      event.preventDefault();
      void signIn(signInButton);
    }
  });
})();
