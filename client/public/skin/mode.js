/* Cosmos skin: light/dark switch.
   Cosmos chooses its palette from the browser's prefers-color-scheme when the
   page loads and offers no setting for it. This script answers that query
   itself when a mode has been chosen, and adds a small button to change it.
   The choice is kept in this browser (localStorage); a change reloads the
   page, because Cosmos only reads the mode at load. */
(function () {
  'use strict';

  var KEY = 'ct-mode';
  var realMatchMedia = window.matchMedia ? window.matchMedia.bind(window) : null;
  var root = document.documentElement;

  var stored = null;
  try { stored = window.localStorage.getItem(KEY); } catch (e) { /* storage blocked: stay automatic */ }
  var mode = stored === 'light' || stored === 'dark' ? stored : 'auto';

  function systemMode() {
    return realMatchMedia && realMatchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }

  var resolved = mode === 'auto' ? systemMode() : mode;
  root.setAttribute('data-ct-mode', resolved);

  if (realMatchMedia && mode !== 'auto') {
    // Only colour-scheme queries are answered here; everything else (the
    // layout's width breakpoints, for instance) goes to the browser untouched.
    window.matchMedia = function (query) {
      var asked = /prefers-color-scheme\s*:\s*(dark|light)/i.exec(String(query));
      if (!asked) return realMatchMedia(query);
      var noop = function () {};
      return {
        matches: asked[1].toLowerCase() === resolved,
        media: String(query),
        onchange: null,
        addListener: noop,
        removeListener: noop,
        addEventListener: noop,
        removeEventListener: noop,
        dispatchEvent: function () { return false; }
      };
    };
  } else if (realMatchMedia) {
    // Automatic: keep the skin's colours in step if the system flips while open.
    var mq = realMatchMedia('(prefers-color-scheme: dark)');
    var follow = function () { root.setAttribute('data-ct-mode', systemMode()); };
    if (mq.addEventListener) mq.addEventListener('change', follow);
    else if (mq.addListener) mq.addListener(follow);
  }

  var SVG_OPEN = '<svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" ' +
    'stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">';
  var ICON = {
    dark: SVG_OPEN + '<path d="M20.5 14.2A8.5 8.5 0 1 1 9.8 3.5a6.8 6.8 0 0 0 10.7 10.7z"/></svg>',
    light: SVG_OPEN + '<circle cx="12" cy="12" r="4.2"/><path d="M12 2.5v2.2M12 19.3v2.2M2.5 12h2.2M19.3 12h2.2' +
      'M5.3 5.3l1.6 1.6M17.1 17.1l1.6 1.6M5.3 18.7l1.6-1.6M17.1 6.9l1.6-1.6"/></svg>'
  };
  var NAME = { dark: 'întunecată', light: 'luminoasă' };

  function choose(next) {
    try {
      if (next === 'auto') window.localStorage.removeItem(KEY);
      else window.localStorage.setItem(KEY, next);
    } catch (e) { return; }
    window.location.reload();
  }

  function addButton() {
    if (document.getElementById('ct-mode-toggle')) return;
    var other = resolved === 'dark' ? 'light' : 'dark';
    var button = document.createElement('button');
    button.id = 'ct-mode-toggle';
    button.type = 'button';
    button.className = 'ct-mode-toggle' + (mode === 'auto' ? ' ct-mode-auto' : '');
    button.innerHTML = ICON[resolved];
    button.setAttribute('aria-label', 'Schimbă pe tema ' + NAME[other]);
    button.title = 'Temă ' + NAME[resolved] + (mode === 'auto' ? ' (automat, după sistem)' : '') +
      '\nClic: tema ' + NAME[other] +
      (mode === 'auto' ? '' : '\nClic dreapta: automat, după sistem');
    button.addEventListener('click', function () { choose(other); });
    button.addEventListener('contextmenu', function (event) {
      event.preventDefault();
      if (mode !== 'auto') choose('auto');
    });
    document.body.appendChild(button);
  }

  if (document.body) addButton();
  else document.addEventListener('DOMContentLoaded', addButton);
})();
