// Stealth patches — runs at document_start in MAIN world before any page JS

(function () {
  'use strict';

  // 1. navigator.webdriver → undefined (belt-and-suspenders with --disable-blink-features flag)
  Object.defineProperty(navigator, 'webdriver', {
    get: () => undefined,
    configurable: true,
  });

  // 2. navigator.plugins — inject realistic PDF plugin
  const fakePDF = {
    name: 'Chrome PDF Viewer',
    description: 'Portable Document Format',
    filename: 'internal-pdf-viewer',
    length: 1,
    0: { type: 'application/pdf', suffixes: 'pdf', description: 'Portable Document Format' },
    item: function (i) { return this[i] || null; },
    namedItem: function (name) {
      for (let i = 0; i < this.length; i++) {
        if (this[i] && this[i].type === name) return this[i];
      }
      return null;
    },
  };
  Object.setPrototypeOf(fakePDF, Plugin.prototype);
  Object.setPrototypeOf(fakePDF[0], MimeType.prototype);

  const fakePluginArray = {
    0: fakePDF,
    length: 1,
    item: function (i) { return this[i] || null; },
    namedItem: function (name) {
      for (let i = 0; i < this.length; i++) {
        if (this[i] && this[i].name === name) return this[i];
      }
      return null;
    },
    refresh: function () {},
  };
  Object.setPrototypeOf(fakePluginArray, PluginArray.prototype);

  Object.defineProperty(navigator, 'plugins', {
    get: () => fakePluginArray,
    configurable: true,
  });

  // 3. navigator.languages
  Object.defineProperty(navigator, 'languages', {
    get: () => ['en-US', 'en'],
    configurable: true,
  });

  // 4. Patch chrome.runtime.sendMessage to not throw in non-extension contexts
  if (window.chrome && window.chrome.runtime) {
    const origSendMessage = window.chrome.runtime.sendMessage;
    window.chrome.runtime.sendMessage = function () {
      try {
        return origSendMessage.apply(this, arguments);
      } catch (e) {
        // Silently swallow — expected in non-extension pages
      }
    };
  }

  // 5. Permissions.query for notifications → "prompt"
  const origQuery = Permissions.prototype.query;
  Permissions.prototype.query = function (desc) {
    if (desc && desc.name === 'notifications') {
      return Promise.resolve({ state: 'prompt', onchange: null });
    }
    return origQuery.call(this, desc);
  };
})();
