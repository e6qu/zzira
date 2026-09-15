// Connect's JavaScript API for app pages shown inside this site. An app page
// loads it from the site; every call is a message to the site page holding
// the app's frame, which answers only within what the app and the person
// using it may do.
(function () {
  if (window.AP) return;
  var host = new URLSearchParams(window.location.search).get('xdm_e');
  if (!host && document.currentScript) host = document.currentScript.src;
  var hostOrigin;
  try {
    hostOrigin = new URL(host).origin;
  } catch (error) {
    return;
  }
  // Answers and listeners are kept in maps, so names that arrive in messages
  // can never reach object properties.
  var pending = new Map();
  var listeners = new Map();
  var next = 0;
  var call = function (method, args) {
    return new Promise(function (resolve, reject) {
      next += 1;
      var id = 'ap-' + next;
      pending.set(id, { resolve: resolve, reject: reject });
      window.parent.postMessage({ zziraConnect: 'call', id: id, method: method, args: args || [] }, hostOrigin);
    });
  };
  window.addEventListener('message', function (event) {
    var data = event.data;
    if (event.origin !== hostOrigin || event.source !== window.parent || !data) return;
    if (data.zziraConnect === 'result' && pending.has(data.id)) {
      var entry = pending.get(data.id);
      pending.delete(data.id);
      if (data.error) entry.reject(new Error(data.error));
      else entry.resolve(data.result);
    } else if (data.zziraConnect === 'event' && typeof data.name === 'string') {
      (listeners.get(data.name) || []).slice().forEach(function (listener) { listener(data.payload); });
    }
  });
  var withCallback = function (promise, callback) {
    if (typeof callback === 'function') promise.then(callback, function () {});
    return promise;
  };
  var request = function (options) {
    if (typeof options === 'string') options = { url: options };
    options = options || {};
    var data = options.data;
    if (data !== undefined && typeof data !== 'string') data = JSON.stringify(data);
    var promise = call('request', [{ url: options.url, type: options.type || 'GET', data: data || '', contentType: options.contentType || '' }]).then(function (response) {
      var xhr = {
        status: response.status,
        responseText: response.body,
        getResponseHeader: function (name) { return String(name).toLowerCase() === 'content-type' ? response.contentType : null; },
      };
      if (response.status >= 400) {
        if (typeof options.error === 'function') options.error(xhr, 'error', response.body);
        var failure = new Error('The request answered ' + response.status);
        failure.xhr = xhr;
        failure.err = response.body;
        throw failure;
      }
      if (typeof options.success === 'function') options.success(response.body, 'success', xhr);
      return { body: response.body, xhr: xhr };
    });
    if (typeof options.success === 'function' || typeof options.error === 'function') promise.catch(function () {});
    return promise;
  };
  var AP = {
    resize: function (width, height) { call('resize', [String(width || ''), String(height || '')]); },
    sizeToParent: function () { call('sizeToParent'); },
    request: request,
    context: {
      getContext: function (callback) { return withCallback(call('getContext'), callback); },
    },
    events: {
      on: function (name, listener) { listeners.set(name, (listeners.get(name) || []).concat([listener])); },
      once: function (name, listener) {
        var wrapped = function (payload) { AP.events.off(name, wrapped); listener(payload); };
        AP.events.on(name, wrapped);
      },
      off: function (name, listener) { listeners.set(name, (listeners.get(name) || []).filter(function (existing) { return existing !== listener; })); },
    },
    jira: {
      setDashboardItemTitle: function (title) { call('setDashboardItemTitle', [String(title)]); },
      openDashboardItemEditor: function () { call('openDashboardItemEditor'); },
      isDashboardItemEditable: function (callback) { return withCallback(call('isDashboardItemEditable'), callback); },
    },
  };
  // Older apps ask for parts of the API by name.
  AP.require = function (names, callback) {
    var parts = new Map([['request', AP.request], ['events', AP.events], ['jira', AP.jira], ['context', AP.context]]);
    var list = Array.isArray(names) ? names : [names];
    if (typeof callback === 'function') callback.apply(null, list.map(function (name) { return parts.get(name); }));
  };
  window.AP = AP;
})();
