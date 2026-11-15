// Layout geometry shared by pointer, phone and keyboard editing. No DOM or I/O.
export function canvasRows(layout) {
  const ext = (layout.widgets || []).reduce((n, w) => Math.max(n, (w.y | 0) + (w.h | 0)), 0);
  return Math.max(1, Math.min(48, Math.max(layout.rows | 0 || (layout.orientation === 'portrait' ? 16 : 8), ext)));
}

export function clampTile(layout, tile) {
  const rows = Math.max(1, layout.rows | 0 || canvasRows(layout));
  tile.x = Math.max(0, Math.min(11, tile.x | 0));
  tile.w = Math.max(1, Math.min(12, tile.w | 0));
  if (tile.x + tile.w > 12) tile.w = 12 - tile.x;
  tile.h = Math.max(1, Math.min(rows, tile.h | 0));
  tile.y = Math.max(0, Math.min(rows - tile.h, tile.y | 0));
  return tile;
}

export function overlaps(a, b) {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

// Apply a complete edit, including neighbour swaps/trims, or leave everything
// unchanged. Return changed tile IDs, [] for no change, or null if refused.
export function editTile(layout, id, change) {
  const original = (layout.widgets || []).find((w) => w.id === id);
  if (!original) return null;
  const next = { ...original };
  for (const field of ['x', 'y', 'w', 'h']) {
    if (field in change) next[field] = change[field];
  }
  const proposed = { ...layout, widgets: layout.widgets.map((w) => w === original ? next : w) };
  clampTile(proposed, next);
  const others = layout.widgets.filter((w) => w !== original);
  const hits = others.filter((w) => overlaps(next, w));
  const plan = [[original, next]];
  if (hits.length && next.w === original.w && next.h === original.h) {
    const other = hits[0];
    const target = { ...other, x: original.x, y: original.y };
    if (hits.length !== 1 || target.x + target.w > 12 || target.y + target.h > canvasRows(proposed) ||
        overlaps(target, next) || others.some((w) => w !== other && overlaps(target, w))) return null;
    plan.push([other, target]);
  } else {
    for (const other of hits) {
      const target = { ...other };
      if (next.x + next.w > original.x + original.w && other.x >= original.x + original.w) {
        target.x = next.x + next.w;
        target.w -= target.x - other.x;
      } else if (next.y + next.h > original.y + original.h && other.y >= original.y + original.h) {
        target.y = next.y + next.h;
        target.h -= target.y - other.y;
      }
      if (target.w < 1 || target.h < 1 || overlaps(target, next)) return null;
      plan.push([other, target]);
    }
  }
  const changed = [];
  for (const [tile, target] of plan) {
    if (['x', 'y', 'w', 'h'].some((field) => tile[field] !== target[field])) {
      Object.assign(tile, target);
      changed.push(tile.id);
    }
  }
  return changed;
}
