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
    element.className = kind === 'error' ? 'form-error' : 'field-hint';
  };

  // Bound network waits, including reading the body, without shortening the
  // browser's separate security-key prompt and its server-issued timeout.
  const request = async (url, init = {}, expectedStatus = 200) => {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 15000);
    try {
      const response = await fetch(url, {
        ...init, method: 'POST', redirect: 'error', signal: controller.signal,
        headers: { 'X-Requested-With': 'zzira', ...init.headers },
      });
      if (!response.ok) {
        const message = response.headers.get('Content-Type')?.startsWith('text/plain')
          ? (await response.text()).trim() : '';
        throw new Error(message || 'The site could not complete that request. Please try again.');
      }
      if (response.status !== expectedStatus || !response.headers.get('Content-Type')?.startsWith('application/json')) {
        throw new Error('The site did not confirm that request. Please try again.');
      }
      return await response.json();
    } catch (error) {
      if (controller.signal.aborted) throw new Error('The site took too long to respond. Please try again.');
      if (error instanceof TypeError) throw new Error('The site could not be reached. Check your connection and try again.');
      if (error instanceof SyntaxError) throw new Error('The site did not confirm that request. Please try again.');
      throw error;
    } finally {
      clearTimeout(timer);
    }
  };

  const failure = (error, fallback) => {
    if (error?.name === 'NotAllowedError' || error?.name === 'AbortError') {
      return 'The security-key request was canceled or timed out. Please try again when your key is ready.';
    }
    return error?.message || fallback;
  };

  const register = async (button) => {
    const status = document.querySelector('[data-passkey-status]');
    const label = document.querySelector('[data-passkey-label]');
    if (!window.PublicKeyCredential) {
      say(status, 'This browser cannot use security keys.', 'error');
      return;
    }
    if (button.disabled) return;
    button.disabled = true;
    const keyLabel = label?.value || '';
    if (label) label.disabled = true;
    say(status, 'Preparing your security key…');
    try {
      const options = await request('/profile/passkeys/options');
      say(status, 'Follow your browser’s instructions to register your security key.');
      const created = await navigator.credentials.create({
        publicKey: {
          ...options,
          challenge: fromBase64URL(options.challenge),
          user: { ...options.user, id: fromBase64URL(options.user.id) },
          excludeCredentials: (options.excludeCredentials || []).map(credential => ({ ...credential, id: fromBase64URL(credential.id) })),
        },
      });
      if (!created) throw new DOMException('No security key selected', 'NotAllowedError');
      say(status, 'Saving your security key…');
      const kept = await request('/profile/passkeys', {
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          rawId: toBase64URL(created.rawId),
          label: keyLabel,
          response: {
            clientDataJSON: toBase64URL(created.response.clientDataJSON),
            attestationObject: toBase64URL(created.response.attestationObject),
          },
        }),
      }, 201);
      if (kept?.registered !== true) throw new Error('The site did not confirm registration. Please try again.');
      location.reload();
    } catch (error) {
      say(status, failure(error, 'That key could not be registered. Please try again.'), 'error');
      button.disabled = false;
      if (label) label.disabled = false;
    }
  };

  const signIn = async (button) => {
    const status = document.querySelector('[data-passkey-signin-status]');
    if (!window.PublicKeyCredential) {
      say(status, 'This browser cannot use security keys. Try a browser that supports your key.', 'error');
      return;
    }
    if (button.disabled) return;
    button.disabled = true;
    say(status, 'Preparing your security key…');
    try {
      const options = await request('/login/verify/passkey/options');
      say(status, 'Follow your browser’s instructions to use your security key.');
      const answered = await navigator.credentials.get({
        publicKey: {
          ...options,
          challenge: fromBase64URL(options.challenge),
          allowCredentials: (options.allowCredentials || []).map(credential => ({ ...credential, id: fromBase64URL(credential.id) })),
        },
      });
      if (!answered) throw new DOMException('No security key selected', 'NotAllowedError');
      say(status, 'Finishing sign-in…');
      const finished = await request('/login/verify/passkey', {
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          rawId: toBase64URL(answered.rawId),
          response: {
            clientDataJSON: toBase64URL(answered.response.clientDataJSON),
            authenticatorData: toBase64URL(answered.response.authenticatorData),
            signature: toBase64URL(answered.response.signature),
          },
        }),
      });
      if (finished?.signedIn !== true) throw new Error('The site did not confirm sign-in. Please try again.');
      location.assign('/');
    } catch (error) {
      say(status, failure(error, 'That key could not answer the sign-in. Please try again.'), 'error');
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
