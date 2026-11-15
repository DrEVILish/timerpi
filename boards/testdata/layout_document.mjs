import assert from 'node:assert/strict';
import { test } from 'node:test';
import { LayoutDocument } from '../../public/src/layout-document.js';

const version = (orientation, id) => ({ v: 1, rows: 8, orientation, widgets: [{ id, type: 'notice', x: 0, y: 0, w: 4, h: 2 }] });
const both = () => ({ ...version('landscape', 'l'), alt: version('portrait', 'p') });
const deferred = () => { let resolve; const promise = new Promise((r) => { resolve = r; }); return { promise, resolve }; };

test('portrait edits save both versions with landscape first', async () => {
  let sent;
  const draft = new LayoutDocument(both(), 'portrait', async (body) => { sent = body; });
  draft.layout.widgets[0].x = 3;
  await draft.save();
  assert.equal(sent.orientation, 'landscape');
  assert.equal(sent.widgets[0].x, 0);
  assert.equal(sent.alt.orientation, 'portrait');
  assert.equal(sent.alt.widgets[0].x, 3);
});

test('reset changes one version; template replacement changes both; rollback restores accepted state', async () => {
  const draft = new LayoutDocument(both(), 'portrait', async () => {});
  draft.replaceVersion(version('landscape', 'reset'));
  await draft.save();
  draft.replace({ ...version('landscape', 'template'), alt: version('portrait', 'template-p') });
  assert.equal(draft.layout.widgets[0].id, 'template-p');
  assert.equal(draft.rollback().widgets[0].id, 'reset');
  assert.equal(draft.layout.orientation, 'portrait');
});

test('accepted state is the sent snapshot, not edits made while it was saving', async () => {
  const gate = deferred();
  const draft = new LayoutDocument(both(), 'landscape', async () => gate.promise);
  draft.layout.rows = 10;
  const saving = draft.save();
  draft.layout.rows = 12;
  gate.resolve();
  await saving;
  assert.equal(draft.layout.rows, 12);
  assert.equal(draft.rollback().rows, 10);
});

test('writes run in order, and a failure cannot replace the accepted document', async () => {
  const gate = deferred(), started = deferred(), sent = [];
  let fail = true;
  const draft = new LayoutDocument(both(), 'landscape', async (body) => {
    sent.push(body);
    if (sent.length === 1) { started.resolve(); await gate.promise; }
    else if (fail) throw new Error('disk full');
  });
  draft.layout.rows = 10;
  const first = draft.save();
  await started.promise;
  draft.layout.rows = 12;
  const second = draft.save();
  const rejected = assert.rejects(second, /disk full/);
  await Promise.resolve();
  assert.equal(sent.length, 1);
  assert.equal(draft.saving, true);
  gate.resolve();
  await first;
  await rejected;
  assert.equal(draft.saving, false);
  assert.equal(draft.rollback().rows, 10);
  fail = false;
  draft.layout.rows = 14;
  await draft.save();
  assert.equal(draft.rollback().rows, 14);
});
