import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createDisplaySession } from '../../public/src/display-session.js';

function fixture(options = {}) {
  const themes = [], hops = [];
  let names = 0, navigations = 0;
  const browser = {
    root: { dataset: {}, getAttribute: () => 'blue-future' },
    body: { dataset: {} },
    location: {
      href: 'http://venue/d/ROOM?screen=Stage&key=secret&orient=portrait&rotate=90',
      replace(url) { hops.push(url); this.href = url; },
    },
    applyTheme: (theme) => themes.push(theme),
    screenName: () => { names++; return 'Stage'; },
    now: () => 1000,
  };
  const session = createDisplaySession({ ...options, browser, onNavigate: () => navigations++ });
  return { session, browser, themes, hops, names: () => names, navigations: () => navigations };
}

test('assignment moves between event layout, built-in and plain timer without losing identity', () => {
  const f = fixture();
  assert.equal(f.session.screen, 'Stage');
  assert.equal(f.names(), 1);
  assert.equal(f.session.handle({ t: 'screen-board', boardId: 9, template: 'ignored' }), 'handled');
  let url = new URL(f.browser.location.href);
  assert.equal(url.searchParams.get('board'), '9');
  assert.equal(url.searchParams.get('view'), 'board');
  assert.equal(url.searchParams.has('tpl'), false);
  assert.equal(url.searchParams.get('screen'), 'Stage');
  assert.equal(url.searchParams.get('key'), 'secret');
  assert.equal(url.searchParams.get('orient'), 'portrait');
  assert.equal(url.searchParams.get('rotate'), '90');
  f.session.handle({ t: 'screen-board', boardId: 9 });
  assert.equal(f.hops.length, 1);
  f.session.handle({ t: 'screen-board', template: 'main' });
  url = new URL(f.browser.location.href);
  assert.equal(url.searchParams.get('tpl'), 'main');
  assert.equal(url.searchParams.has('board'), false);
  f.session.handle({ t: 'screen-board' });
  url = new URL(f.browser.location.href);
  assert.equal(url.searchParams.has('view'), false);
  assert.equal(url.searchParams.has('tpl'), false);
  assert.equal(url.searchParams.has('board'), false);
  assert.equal(f.navigations(), 3);
});

test('operator and preview sessions never register screen identity', () => {
  for (const options of [{ role: 'controls' }, { preview: true }]) {
    const f = fixture(options);
    assert.equal(f.session.screen, '');
    assert.equal(f.names(), 0);
  }
  const controls = fixture({ role: 'controls' });
  controls.session.handle({ t: 'screen-board', boardId: 7 });
  controls.session.handle({ t: 'screen-look', rotation: 90 });
  assert.deepEqual(controls.hops, []);
  assert.equal(controls.browser.root.dataset.rotate, undefined);
  controls.session.handle({ t: 'display', theme: 'other' });
  assert.deepEqual(controls.themes, ['other']);
});

test('editing sessions retain their drafts and mounting', () => {
  const f = fixture({ editable: true });
  f.browser.root.dataset.rotate = '180';
  f.session.handle({ t: 'screen-board', boardId: 7 });
  f.session.handle({ t: 'screen-look', rotation: 90, kind: 'presenter' });
  assert.deepEqual(f.hops, []);
  assert.equal(f.browser.root.dataset.rotate, '180');
  assert.equal(f.browser.body.dataset.kind, undefined);
  const queryEdit = fixture();
  queryEdit.browser.location.href += '&edit=1';
  queryEdit.session.handle({ t: 'screen-board', template: 'main' });
  assert.deepEqual(queryEdit.hops, []);
});

test('mounted and handheld screens apply the same orientation rule', () => {
  const f = fixture();
  f.session.handle({ t: 'screen-look', rotation: 90, kind: 'presenter' });
  assert.equal(f.browser.root.dataset.rotate, '90');
  assert.equal(f.browser.body.dataset.kind, 'presenter');
  f.session.handle({ t: 'screen-look', rotation: 0 });
  assert.equal(f.browser.root.dataset.rotate, undefined);
  f.browser.root.dataset.deviceOrient = 'portrait';
  f.browser.root.dataset.rotate = '180';
  f.session.handle({ t: 'screen-look', rotation: 90 });
  assert.equal(f.browser.root.dataset.rotate, undefined);
});

test('theme pushes repaint only a changed nonempty theme', () => {
  const f = fixture();
  f.session.handle({ t: 'display', theme: 'blue-future' });
  f.session.handle({ t: 'display', theme: '' });
  f.session.handle({ t: 'display', theme: 'other' });
  assert.deepEqual(f.themes, ['other']);
});

test('message and runtime deltas preserve snapshot and clock semantics', () => {
  const f = fixture();
  const mesh = { snap: { updatedAt: 10, runtime: { rate: 1, dayStartTS: 1 }, messages: [{ id: 1, text: 'old' }, { id: 2, text: 'keep' }] }, clockOffset: 0 };
  assert.equal(f.session.handle({ t: 'message', message: { id: 1, text: 'new', shownAt: 1 }, updatedAt: 12 }, mesh), 'message');
  assert.equal(mesh.snap.updatedAt, 13);
  assert.deepEqual(mesh.snap.messages, [{ id: 2, text: 'keep' }, { id: 1, text: 'new', shownAt: 1 }]);
  f.session.handle({ t: 'message', message: { id: 1, text: '', shownAt: 0 } }, mesh);
  assert.deepEqual(mesh.snap.messages, [{ id: 2, text: 'keep' }]);
  assert.equal(f.session.handle({ t: 'timer', runtime: { rate: 2 }, updatedAt: 1, serverTime: 5000 }, mesh), 'handled');
  assert.equal(mesh.snap.runtime.rate, 1);
  assert.equal(mesh.clockOffset, 0);
  assert.equal(f.session.handle({ t: 'cue', runtime: { rate: 2 }, updatedAt: 20, serverTime: 5000 }, mesh), 'runtime');
  assert.equal(mesh.snap.runtime.rate, 2);
  assert.equal(mesh.snap.runtime.dayStartTS, 1);
  assert.equal(mesh.clockOffset, 4000);
  assert.equal(mesh.snap.updatedAt, 20);
  assert.equal(f.session.handle({ t: 'poll' }, mesh), null);
  assert.equal(f.session.handle({ t: 'message', message: { id: 1 } }), 'handled');
});
