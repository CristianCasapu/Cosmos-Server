// Fork addition: the skin's accent from the two colours chosen under
// Configuration > Theme. The colours are kept in hue; their lightness is moved
// until each use is readable on the skin's surfaces in each mode:
//   text and icons in the accent   4.5:1 on the page, a card and a raised card
//   white text on the accent fill  4.5:1 on both ends of the gradient
//   the focus ring and lines       3:1 on the page
// Pure functions, used by components/skinAccent.jsx and tested with node --test.

// the skin's surfaces (client/public/skin/theme.css), the ones accent text sits on
export const SURFACES = {
  dark: ['#0a0f1d', '#121a2d', '#172138', '#18223a'],
  light: ['#f2f5fa', '#ffffff', '#f6f8fb'],
};

// the names written by tokensCss and accepted again by mode.js from storage
export const TOKEN_NAMES = [
  '--ct-accent', '--ct-accent-2', '--ct-accent-glow', '--ct-accent-2-glow', '--ct-gradient', '--ct-accent-fill',
  '--ct-focus', '--ct-accent-text', '--ct-accent-soft', '--ct-accent-soft-border',
];

const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));

// "rgba(64, 132, 255, 1)", "rgb(…)", "#4084ff", "#48f" -> { r, g, b } (0..255), or null
export const parseColour = (text) => {
  if (typeof text !== 'string') return null;
  const s = text.trim().toLowerCase();
  if (s.length > 64) return null;
  let m = /^#([0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})$/.exec(s);
  if (m) {
    let h = m[1];
    if (h.length === 3) h = h.split('').map((c) => c + c).join('');
    return { r: parseInt(h.slice(0, 2), 16), g: parseInt(h.slice(2, 4), 16), b: parseInt(h.slice(4, 6), 16) };
  }
  m = /^rgba?\(\s*([\d.]+)\s*[, ]\s*([\d.]+)\s*[, ]\s*([\d.]+)\s*(?:[,/]\s*([\d.]+%?)\s*)?\)$/.exec(s);
  if (m) {
    const [r, g, b] = [m[1], m[2], m[3]].map(Number);
    if ([r, g, b].some((v) => !Number.isFinite(v) || v > 255)) return null;
    return { r: Math.round(r), g: Math.round(g), b: Math.round(b) };
  }
  return null;
};

export const hex = ({ r, g, b }) => '#' + [r, g, b].map((v) => Math.round(clamp(v, 0, 255)).toString(16).padStart(2, '0')).join('');

const channel = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
export const luminance = (c) => 0.2126 * channel(c.r) + 0.7152 * channel(c.g) + 0.0722 * channel(c.b);
export const contrast = (a, b) => {
  const la = luminance(a), lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
};

const toHsl = ({ r, g, b }) => {
  r /= 255; g /= 255; b /= 255;
  const max = Math.max(r, g, b), min = Math.min(r, g, b);
  const l = (max + min) / 2;
  if (max === min) return { h: 0, s: 0, l };
  const d = max - min;
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
  let h;
  if (max === r) h = (g - b) / d + (g < b ? 6 : 0);
  else if (max === g) h = (b - r) / d + 2;
  else h = (r - g) / d + 4;
  return { h: h / 6, s, l };
};
const toRgb = ({ h, s, l }) => {
  if (s === 0) return { r: l * 255, g: l * 255, b: l * 255 };
  const hue = (p, q, t) => {
    if (t < 0) t += 1;
    if (t > 1) t -= 1;
    if (t < 1 / 6) return p + (q - p) * 6 * t;
    if (t < 1 / 2) return q;
    if (t < 2 / 3) return p + (q - p) * (2 / 3 - t) * 6;
    return p;
  };
  const q = l < 0.5 ? l * (1 + s) : l + s - l * s;
  const p = 2 * l - q;
  return { r: hue(p, q, h + 1 / 3) * 255, g: hue(p, q, h) * 255, b: hue(p, q, h - 1 / 3) * 255 };
};
const round = (c) => ({ r: Math.round(c.r), g: Math.round(c.g), b: Math.round(c.b) });

// the colour, lighter (up) or darker (down) in steps of 1 %, until it has at
// least `need` against every colour of `against`; the colour itself when it has
export const reach = (colour, against, need, direction) => {
  const hsl = toHsl(colour);
  for (let i = 0; i <= 100; i++) {
    const l = clamp(hsl.l + (direction === 'up' ? i : -i) / 100, 0, 1);
    const c = round(toRgb({ ...hsl, l }));
    if (against.every((a) => contrast(c, a) >= need)) return c;
    if (l === 0 || l === 1) break;
  }
  return direction === 'up' ? { r: 255, g: 255, b: 255 } : { r: 0, g: 0, b: 0 };
};

const WHITE = { r: 255, g: 255, b: 255 };
const DEFAULT_SECONDARY = { r: 14, g: 150, b: 184 };
const rgbList = (c) => `${c.r}, ${c.g}, ${c.b}`;

// The tokens for a primary and a secondary colour, or null when the primary is
// not a colour (then the skin keeps its own). { common: {}, dark: {}, light: {} }
export const accentTokens = (primaryText, secondaryText) => {
  const primary = parseColour(primaryText);
  if (!primary) return null;
  const secondary = parseColour(secondaryText) || DEFAULT_SECONDARY;
  const parse = (h) => parseColour(h);
  const dark = SURFACES.dark.map(parse);
  const light = SURFACES.light.map(parse);

  // white text on both ends of the fill (a little above 4.5 for anti-aliasing)
  const fillA = reach(primary, [WHITE], 4.6, 'down');
  const fillB = reach(secondary, [WHITE], 4.6, 'down');

  const perMode = (surfaces, direction, softAlpha, edgeAlpha) => {
    const text = reach(primary, surfaces, 4.6, direction);
    const ring = reach(primary, [surfaces[0]], 3.1, direction);
    return {
      '--ct-focus': hex(ring),
      '--ct-accent-text': hex(text),
      '--ct-accent-soft': `rgba(${rgbList(primary)}, ${softAlpha})`,
      '--ct-accent-soft-border': `rgba(${rgbList(text)}, ${edgeAlpha})`,
    };
  };

  return {
    common: {
      '--ct-accent': hex(primary),
      '--ct-accent-2': hex(secondary),
      '--ct-accent-glow': rgbList(primary),
      '--ct-accent-2-glow': rgbList(secondary),
      '--ct-gradient': `linear-gradient(135deg, ${hex(primary)}, ${hex(secondary)})`,
      '--ct-accent-fill': `linear-gradient(135deg, ${hex(fillA)}, ${hex(fillB)})`,
    },
    dark: perMode(dark, 'up', 0.14, 0.4),
    light: perMode(light, 'down', 0.09, 0.35),
  };
};

// a value as these functions write it: colours, numbers, a gradient; nothing else
const SAFE_VALUE = /^[#(),.%\s0-9a-z-]{1,200}$/i;
export const safeTokens = (block) => {
  const out = {};
  if (!block || typeof block !== 'object') return out;
  for (const name of TOKEN_NAMES) {
    const value = block[name];
    if (typeof value === 'string' && SAFE_VALUE.test(value) && !/url|expression|var\(/i.test(value)) out[name] = value;
  }
  return out;
};

const declarations = (block) => Object.entries(safeTokens(block)).map(([k, v]) => `${k}: ${v};`).join(' ');

// The style sheet that puts the tokens over the skin's own (selectors one step
// more specific than the skin's, so the order of the two does not matter).
export const tokensCss = (tokens) => {
  if (!tokens) return '';
  return `:root:root { ${declarations(tokens.common)} }\n` +
    `:root:root[data-ct-mode="dark"] { ${declarations(tokens.dark)} }\n` +
    `:root:root[data-ct-mode="light"] { ${declarations(tokens.light)} }\n`;
};
