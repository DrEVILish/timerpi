/**
 * swatches.js — the colour picker (STATUS U30): ftl-themes' `.swatches`
 * fieldset (radio swatches with the theme's ring and check); the last
 * swatch opens the native colour picker for any other colour. Used by the
 * cue editor and the running order's alert cells.
 */

// Named presets. The first entry is "Default": '' (the server fills the
// alert defaults, or the theme for a row colour).
export const PRESETS = [
  ['Amber', '#ffaa00'],
  ['Orange', '#f97316'],
  ['Red', '#ff4444'],
  ['Pink', '#ec4899'],
  ['Purple', '#a855f7'],
  ['Blue', '#2f7cf6'],
  ['Green', '#16a34a'],
  ['White', '#ffffff'],
];

let seq = 0;

const norm = (v) => String(v || '').trim().toLowerCase();

/**
 * swatchPicker builds a `.swatches` fieldset.
 *   value         current colour ('' = default)
 *   defaultColor  what '' looks like (CSS colour; '' = the theme accent)
 *   defaultLabel  name of the default swatch ("Default", "Theme")
 *   label         accessible legend
 *   presets       [[title, '#rrggbb'], …] after the default (PRESETS)
 *   custom        false = no "another colour" input
 *   onChange(v)   called with '#rrggbb' or ''
 */
export function swatchPicker({ value = '', defaultColor = '', defaultLabel = 'Default', label = 'Colour', presets = PRESETS, custom: withCustom = true, onChange }) {
  const name = `tp-sw-${++seq}`;
  const fs = document.createElement('fieldset');
  fs.className = 'swatches tp-swatches';
  const legend = document.createElement('legend');
  legend.className = 'visually-hidden';
  legend.textContent = label;
  fs.appendChild(legend);

  const radios = [];
  const add = (title, v, swatch) => {
    const l = document.createElement('label');
    l.className = 'swatch';
    l.style.setProperty('--swatch', swatch);
    l.title = title;
    const r = document.createElement('input');
    r.type = 'radio';
    r.name = name;
    r.value = v;
    const sr = document.createElement('span');
    sr.className = 'visually-hidden';
    sr.textContent = title;
    l.append(r, sr);
    fs.appendChild(l);
    radios.push(r);
    return r;
  };
  add(defaultLabel, '', defaultColor || 'var(--accent)');
  // A preset equal to the default would be a second, identical swatch.
  for (const [title, hex] of presets) if (norm(hex) !== norm(defaultColor)) add(title, hex, hex);

  // Any other colour (UI #3): the last ftl swatch. Its label holds the
  // native picker (visually hidden by .swatch > input, opened by the label)
  // and a radio that carries the checked ring and ✓ while a custom colour
  // is chosen. Unchosen, the swatch shows a colour wheel.
  const wheel = 'conic-gradient(#ff4444, #ffaa00, #16a34a, #2f7cf6, #a855f7, #ff4444)';
  const customLabel = document.createElement('label');
  customLabel.className = 'swatch tp-swatch-custom';
  customLabel.title = 'Another colour';
  const custom = document.createElement('input');
  custom.type = 'color';
  custom.setAttribute('aria-label', `${label}: another colour`);
  const customRadio = document.createElement('input');
  customRadio.type = 'radio';
  customRadio.name = name;
  customRadio.tabIndex = -1;
  customRadio.setAttribute('aria-hidden', 'true');
  customLabel.append(custom, customRadio);
  if (withCustom) fs.appendChild(customLabel);

  const set = (v) => {
    // The default's own colour (a server-filled alert default) is the default swatch.
    const n = defaultColor && norm(v) === norm(defaultColor) ? '' : norm(v);
    let hit = false;
    for (const r of radios) { r.checked = norm(r.value) === n; hit = hit || r.checked; }
    const isCustom = !hit && /^#[0-9a-f]{6}$/.test(n);
    customRadio.checked = isCustom;
    customRadio.value = isCustom ? n : '';
    if (isCustom) custom.value = n;
    customLabel.style.setProperty('--swatch', isCustom ? n : wheel);
  };
  set(value);

  fs.addEventListener('change', (e) => {
    if (e.target === customRadio && !customRadio.value) return;
    const v = e.target === custom ? custom.value : e.target.value;
    set(v);
    onChange?.(v);
  });
  fs.setValue = set;
  return fs;
}
