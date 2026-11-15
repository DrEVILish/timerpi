import { canvasRows } from './layout-edit.js';

// Own both orientation versions and the last accepted document. Writes are
// serialized so an earlier, slower request cannot overwrite a newer edit.
export class LayoutDocument {
  #document;
  #orientation;
  #persist;
  #accepted;
  #pending;
  #saves = 0;
  constructor(document, orientation, persist) {
    this.#orientation = orientation;
    this.#persist = persist;
    this.replace(document);
    this.#accepted = structuredClone(this.#document);
    this.#pending = Promise.resolve();
  }

  get layout() {
    return (this.#document.orientation || 'landscape') === this.#orientation
      ? this.#document : (this.#document.alt || this.#document);
  }

  get saving() { return this.#saves > 0; }

  replace(document) {
    this.#document = structuredClone(document);
    return this.layout;
  }

  replaceVersion(version) {
    const next = { ...structuredClone(version), orientation: this.#orientation };
    delete next.alt;
    if (this.layout === this.#document) this.#document = { ...next, alt: this.#document.alt };
    else this.#document.alt = next;
    return this.layout;
  }

  save() {
    const captured = structuredClone(this.#document);
    const current = this.layout;
    const cur = { v: 1, rows: canvasRows(current), orientation: this.#orientation, widgets: current.widgets };
    const o = current === this.#document ? this.#document.alt : this.#document;
    const other = o && o.widgets?.length
      ? { v: 1, rows: o.rows, orientation: this.#orientation === 'portrait' ? 'landscape' : 'portrait', widgets: o.widgets } : null;
    const animation = { anim: current.anim || 'fade', animMS: Number(current.animMS) || 400 };
    const body = structuredClone(this.#orientation === 'landscape' || !other
      ? { ...cur, ...animation, ...(other ? { alt: other } : {}) }
      : { ...other, ...animation, alt: cur });
    this.#saves++;
    this.#pending = this.#pending.catch(() => {}).then(async () => {
      await this.#persist(body);
      this.#accepted = captured;
    }).finally(() => { this.#saves--; });
    return this.#pending;
  }

  rollback() {
    return this.replace(this.#accepted);
  }
}
