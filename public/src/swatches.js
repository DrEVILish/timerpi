/**
 * swatches.js — the colour picker (STATUS U30): ftl-themes' `.swatches`
 * fieldset (radio swatches with the theme's ring and check) plus a native
 * <input type="color"> for any other colour (core sizes it). Used by the
 * cue details panel and the running order's alert cells.
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
  for (const [title, hex] of presets) add(title, hex, hex);

  // Any other colour: the native picker, styled by core.
  const custom = document.createElement('input');
  custom.type = 'color';
  custom.className = 'tp-swatch-custom';
  custom.title = 'Another colour';
  custom.setAttribute('aria-label', `${label}: another colour`);
  if (withCustom) fs.appendChild(custom);

  const set = (v) => {
    const n = norm(v);
    let hit = false;
    for (const r of radios) { r.checked = norm(r.value) === n; hit = hit || r.checked; }
    custom.classList.toggle('is-selected', !hit && n !== '');
    if (/^#[0-9a-f]{6}$/.test(n)) custom.value = n;
  };
  set(value);

  fs.addEventListener('change', (e) => {
    const v = e.target === custom ? custom.value : e.target.value;
    set(v);
    onChange?.(v);
  });
  fs.setValue = set;
  return fs;
}
