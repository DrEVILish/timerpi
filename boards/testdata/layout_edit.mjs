import assert from 'node:assert/strict';
import { test } from 'node:test';
import { editTile, canvasRows } from '../../public/src/layout-edit.js';

const tile = (id, x, y, w = 6, h = 4) => ({ id, type: 'notice', x, y, w, h });
const board = (...widgets) => ({ rows: 8, widgets });

test('tile height stays within the server limit even on a tall canvas', () => {
  const layout = { rows: 48, widgets: [tile('a', 0, 0, 6, 12)] };
  assert.deepEqual(editTile(layout, 'a', { h: 13 }), []);
  assert.equal(layout.widgets[0].h, 12);
});

test('moving onto one tile swaps both without replacing their data', () => {
  const a = tile('a', 0, 0), b = tile('b', 6, 0);
  const layout = board(a, b);
  assert.deepEqual(editTile(layout, 'a', { x: 6 }), ['a', 'b']);
  assert.equal(a.x, 6);
  assert.equal(b.x, 0);
  assert.equal(layout.widgets[0], a);
  assert.deepEqual(editTile(layout, 'a', { x: 6 }), []);
});

test('growing right trims the neighbour', () => {
  const layout = board(tile('a', 0, 0), tile('b', 6, 0));
  assert.deepEqual(editTile(layout, 'a', { w: 8 }), ['a', 'b']);
  assert.equal(layout.widgets[1].x, 8);
  assert.equal(layout.widgets[1].w, 4);
});

test('growing down trims the neighbour', () => {
  const layout = board(tile('a', 0, 0), tile('b', 0, 4));
  assert.deepEqual(editTile(layout, 'a', { h: 6 }), ['a', 'b']);
  assert.equal(layout.widgets[1].y, 6);
  assert.equal(layout.widgets[1].h, 2);
});

test('a refused resize leaves every tile unchanged', () => {
  const layout = board(tile('a', 0, 0), tile('b', 6, 0, 6, 2), tile('c', 6, 2, 1, 2));
  const before = structuredClone(layout);
  assert.equal(editTile(layout, 'a', { w: 8 }), null);
  assert.deepEqual(layout, before);
});

test('a move overlapping multiple tiles is refused atomically', () => {
  const layout = board(tile('a', 0, 0), tile('b', 6, 0, 6, 2), tile('c', 6, 2, 6, 2));
  const before = structuredClone(layout);
  assert.equal(editTile(layout, 'a', { x: 6 }), null);
  assert.deepEqual(layout, before);
  assert.equal(editTile(layout, 'missing', { x: 1 }), null);
  assert.deepEqual(layout, before);
});

test('edits stay inside the canvas and leave the other version alone', () => {
  const layout = board(tile('a', 0, 0));
  layout.alt = { orientation: 'portrait', widgets: [tile('p', 0, 0)] };
  const other = structuredClone(layout.alt);
  assert.deepEqual(editTile(layout, 'a', { x: 90, y: 90 }), ['a']);
  assert.equal(layout.widgets[0].x, 11);
  assert.equal(layout.widgets[0].w, 1);
  assert.equal(layout.widgets[0].y, 4);
  assert.deepEqual(layout.alt, other);
  assert.equal(canvasRows({ orientation: 'portrait', widgets: [] }), 16);
  assert.equal(canvasRows({ widgets: [tile('low', 0, 80)] }), 48);
});
