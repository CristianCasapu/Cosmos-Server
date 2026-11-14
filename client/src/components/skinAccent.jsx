// Fork addition: the skin takes the colours chosen under Configuration > Theme.
// The tokens come from utils/skinAccent.js (adjusted for readability); they go
// into one style element, and a copy is kept in this browser so that
// client/public/skin/mode.js can apply it before the next page paints (and on
// the sign-in pages, which may not read the server's colours).
import { useEffect } from 'react';
import { accentTokens, tokensCss } from '../utils/skinAccent';

const STYLE_ID = 'ct-accent-style';
const STORE_KEY = 'ct-accent';

export const applyAccent = (primary, secondary) => {
  const tokens = accentTokens(primary, secondary);
  let style = document.getElementById(STYLE_ID);
  if (!tokens) {
    if (style) style.remove();
    try { window.localStorage.removeItem(STORE_KEY); } catch (e) { /* storage blocked */ }
    return;
  }
  if (!style) {
    style = document.createElement('style');
    style.id = STYLE_ID;
    document.head.appendChild(style);
  }
  style.textContent = tokensCss(tokens);
  try { window.localStorage.setItem(STORE_KEY, JSON.stringify({ v: 1, tokens })); } catch (e) { /* storage blocked */ }
};

// primary / secondary: undefined while the server's colours are not known (a
// visitor on the sign-in page): then what mode.js applied stays.
const SkinAccent = ({ primary, secondary }) => {
  useEffect(() => {
    if (primary === undefined || primary === null) return;
    applyAccent(primary, secondary);
  }, [primary, secondary]);
  return null;
};

export default SkinAccent;
