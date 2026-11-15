import { screenName } from './mesh.js';
import { applyTheme } from './theme.js';

const browserEffects = () => ({
  root: document.documentElement, body: document.body, location,
  screenName, applyTheme, now: Date.now,
});

// Shared display policy above Mesh. Renderers keep their own DOM, schedule,
// poll and reload behavior; transport, trust and offline commands stay in Mesh.
export function createDisplaySession({ role = 'display', editable = false, preview = false, onNavigate = () => {}, browser = browserEffects() } = {}) {
  const isDisplay = role === 'display';
  return {
    screen: role === 'controls' || preview ? '' : browser.screenName(),
    handle(frame, mesh) {
      switch (frame.t) {
        case 'screen-look':
          if (isDisplay && !editable) {
            if (frame.rotation && !browser.root.dataset.deviceOrient) browser.root.dataset.rotate = String(frame.rotation);
            else delete browser.root.dataset.rotate;
            if (frame.kind) browser.body.dataset.kind = frame.kind;
          }
          return 'handled';
        case 'display':
          if (frame.theme && frame.theme !== browser.root.getAttribute('data-theme')) browser.applyTheme(frame.theme);
          return 'handled';
        case 'screen-board':
          if (isDisplay && !editable) steerLayout(frame, browser.location, onNavigate);
          return 'handled';
        case 'message':
          return updateMessage(frame, mesh) ? 'message' : 'handled';
        case 'timer':
        case 'cue':
          return updateRuntime(frame, mesh, browser.now) ? 'runtime' : 'handled';
        default:
          return null;
      }
    },
  };
}

function steerLayout(frame, target, onNavigate) {
  const url = new URL(target.href);
  if (url.searchParams.get('edit') === '1') return;
  const board = Number(frame.boardId) || 0;
  if (board > 0) {
    url.searchParams.set('view', 'board');
    url.searchParams.set('board', String(board));
    url.searchParams.delete('tpl');
  } else if (frame.template) {
    url.searchParams.set('view', 'board');
    url.searchParams.set('tpl', frame.template);
    url.searchParams.delete('board');
  } else {
    url.searchParams.delete('view');
    url.searchParams.delete('board');
    url.searchParams.delete('tpl');
  }
  if (url.toString() !== target.href) {
    onNavigate();
    target.replace(url.toString());
  }
}

function updateMessage(frame, mesh) {
  if (!mesh?.snap || !frame.message) return false;
  const list = mesh.snap.messages.filter((message) => message.id !== frame.message.id);
  if (frame.message.shownAt || frame.message.text) list.push(frame.message);
  mesh.snap.messages = list;
  mesh.snap.updatedAt = Math.max(mesh.snap.updatedAt, frame.updatedAt || 0) + 1;
  return true;
}

function updateRuntime(frame, mesh, now) {
  if (!frame.runtime || !mesh?.snap || (frame.updatedAt ?? 0) < mesh.snap.updatedAt) return false;
  Object.assign(mesh.snap.runtime, frame.runtime);
  mesh.snap.updatedAt = frame.updatedAt ?? mesh.snap.updatedAt;
  if (frame.serverTime) mesh.clockOffset = frame.serverTime - now();
  return true;
}
