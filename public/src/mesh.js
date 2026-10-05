/**
 * TimerPi client mesh (ported from the Capacitimer P2P display mesh)
 *
 * Manages a show connection for a browser client:
 *   - WebSocket to the TimerPi server (authoritative while reachable)
 *   - WebRTC data-channel full mesh with every peer of the show (LAN-direct)
 *   - Deterministic master election (earliest joinedAt wins; peerId tiebreak)
 *   - Master executes commands locally while the server is unreachable and
 *     pushes state back on reconnect via POST /api/shows/:id/sync
 *     (PROTOCOL.md snapshot JSON shape; last-writer-wins by updatedAt)
 *
 * Signaling goes through the Go WS hub: {"t":"signal","to":…} out,
 * {"t":"signal","from":…,"data":…} in; peer lists arrive as {"t":"peers",…}.
 *
 * Frames the app cares about but the mesh doesn't own (oob, schedule,
 * message, display, timer, cue) are forwarded to `onFrame`.
 */

import {
  applyCommand, tickZeroCrossing, activeCue,
} from './engine.js';

const SIGNALING_CHANNEL = 'timerpi';

/**
 * Screen name for THIS browser window (F1, owner refinement 2026-10-05):
 * `?screen=` URL param wins (a persistent override — kiosks/session
 * restore reopen the same URL and reclaim the identity), then the
 * window-scoped sessionStorage name (reload in the same window keeps its
 * identity; a fresh window is a NEW screen). Deliberately NOT localStorage:
 * multiple display windows on one browser must show as SEPARATE screens,
 * not group under one shared name.
 */
export function screenName() {
  let q = '';
  try { q = new URL(location.href).searchParams.get('screen') || ''; } catch { /* */ }
  if (q) { try { sessionStorage.setItem('tp.screen', q); } catch { /* */ } return q; }
  let n = '';
  try { n = sessionStorage.getItem('tp.screen') || ''; } catch { /* */ }
  if (!n) {
    n = 'Screen-' + ((crypto.randomUUID?.() || Math.random().toString(36)).replace(/-/g, '').slice(0, 4).toUpperCase());
    try { sessionStorage.setItem('tp.screen', n); } catch { /* */ }
  }
  return n;
}

export class Mesh {
  constructor({ showId, role = 'display', screen = '', onSnapshot, onStatusChange, onFrame, onLog, onMerged }) {
    this.showId = String(showId || '');
    this.role = role;
    this.peerId = (crypto.randomUUID ? crypto.randomUUID() : randId());
    this.screen = role === 'controls' ? '' : screen; // F1: displays self-register
    this.joinedAt = null;
    this.snap = null;                 // last adopted snapshot (own working copy)
    this.clockOffset = 0;             // serverTime - Date.now()
    this.wsStatus = 'connecting';     // connecting | online | offline | badshow
    this.peers = new Map();           // peerId -> {peerId, role, joinedAt}
    this.connections = new Map();     // peerId -> {pc, dc}
    this.masterId = null;
    this.onSnapshot = onSnapshot || (() => {});
    this.onStatusChange = onStatusChange || (() => {});
    this.onFrame = onFrame || (() => {});
    this.onLog = onLog || (() => {});
    this.onMerged = onMerged || (() => {});
    this._ws = null;
    this._wsBackoff = 800;
    this._wsReconnectTimer = null;
    this._heartbeatTimer = null;
    this._pingTimer = null;
    this._zeroTimer = null;
    this._closed = false;
    this._deleted = false; // operator "delete session" — stand down until reload
    this._advancedOffline = false;
  }

  /* --------------------------- public API --------------------------- */

  start() {
    this._wsConnect();
    // Owner bug report: after an outage nothing reconnected. Rejoin the
    // INSTANT the network returns or the tab is seen again — never wait
    // out a throttled background timer.
    this._onOnline = () => this._reconnectNow();
    this._onVisible = () => {
      if (document.visibilityState === 'visible') this._reconnectNow();
    };
    window.addEventListener('online', this._onOnline);
    document.addEventListener('visibilitychange', this._onVisible);
    this._heartbeatTimer = setInterval(() => this._heartbeat(), 3000);
    // App-level 20 s ping (PROTOCOL §hub / CONTRACT-UI §5): keeps the
    // documented channel alive and re-anchors clockOffset via `pong`.
    this._pingTimer = setInterval(() => {
      if (this.serverOnline()) {
        try { this._ws.send(JSON.stringify({ t: 'ping' })); } catch { /* raced a close */ }
      }
    }, 20000);
  }

  _reconnectNow() {
    if (this._closed || this.serverOnline()) return;
    this._wsBackoff = 800;
    clearTimeout(this._wsReconnectTimer);
    this._wsConnect();
  }

  stop() {
    this._closed = true;
    if (this._onOnline) window.removeEventListener('online', this._onOnline);
    if (this._onVisible) document.removeEventListener('visibilitychange', this._onVisible);
    clearInterval(this._heartbeatTimer);
    clearInterval(this._pingTimer);
    clearInterval(this._zeroTimer);
    clearTimeout(this._wsReconnectTimer);
    try { this._ws?.close(); } catch { /* */ }
    for (const { pc } of this.connections.values()) { try { pc.close(); } catch { /* */ } }
    this.connections.clear();
  }

  isMaster() { return this.masterId === this.peerId; }

  serverOnline() { return this._ws?.readyState === 1; }

  /** peers with a live data channel right now */
  openPeerIds() {
    return [...this.connections.entries()]
      .filter(([, c]) => c.dc?.readyState === 'open')
      .map(([id]) => id);
  }

  now() { return Date.now() + this.clockOffset; }

  sendCommand(action, args = {}) {
    if (this.serverOnline()) {
      this._ws.send(JSON.stringify({ t: 'cmd', action, args }));
      return;
    }
    if (this.isMaster()) {
      this._applyLocalCommand(action, args);
      return;
    }
    const openIds = this.openPeerIds();
    const masterDc = this.masterId && this.connections.get(this.masterId)?.dc;
    if (masterDc?.readyState === 'open') {
      masterDc.send(JSON.stringify({ t: 'mesh-cmd', action, args, from: this.peerId }));
      return;
    }
    // No reachable master channel: re-run the deterministic earliest-joinedAt
    // election LOCALLY among the peers we can actually still reach (open
    // data channels) plus ourselves — stale map entries (dead channels from
    // the server's registry) must not wedge commands. Fully alone → crown
    // ourselves: the show keeps running from wherever it stands (offline
    // master contract); reconnect resyncs through the sync push.
    if (openIds.length) {
      const candidates = [{ peerId: this.peerId, joinedAt: this.joinedAt ?? Infinity }];
      for (const id of openIds) {
        const p = this.peers.get(id);
        if (p) candidates.push({ peerId: id, joinedAt: p.joinedAt });
      }
      let best = candidates[0];
      for (const c of candidates) {
        if (!best || c.joinedAt < best.joinedAt
            || (c.joinedAt === best.joinedAt && c.peerId < best.peerId)) best = c;
      }
      if (best.peerId === this.peerId) {
        this.masterId = this.peerId;
        this._becomeMaster();
        this._applyLocalCommand(action, args);
        return;
      }
      const bestDc = this.connections.get(best.peerId)?.dc;
      if (bestDc?.readyState === 'open') {
        bestDc.send(JSON.stringify({ t: 'mesh-cmd', action, args, from: this.peerId }));
        return;
      }
      // unreachable best: fall through to the alone-case below
    }
    if (this.snap) {
      this.masterId = this.peerId;
      this._becomeMaster();
      this._applyLocalCommand(action, args);
    }
  }

  /* --------------------------- WebSocket ---------------------------- */

  _wsConnect() {
    if (this._closed || this._deleted) return;
    clearTimeout(this._wsReconnectTimer);
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    try { this._ws?.close(); } catch { /* */ }
    const ws = new WebSocket(`${proto}://${location.host}/ws`);
    this._ws = ws;
    const fail = () => this._wsDown(ws);

    // Reconnect-loop watchdog (owner bug report 2026-10-05): with the
    // network down, browsers can hold a socket in CONNECTING for minutes
    // without firing close/error — nothing retried, nothing rejoined.
    // After 6 s, kill it so the backoff loop keeps ticking.
    const handshake = setTimeout(() => {
      if (ws.readyState === WebSocket.CONNECTING) {
        try { ws.close(); } catch { /* already gone */ }
        fail();
      }
    }, 6000);

    ws.onopen = () => {
      clearTimeout(handshake);
      // C2 drill fix: re-stamping joinedAt on EVERY reconnect (Date.now())
      // made the "earliest join wins" election unstable across server
      // restarts — the original master would rejoin with a LATER stamp than
      // a peer that never dropped, and lose the crown on rejoin. The
      // session's first join stamp is the stable identity; reuse it.
      ws.send(JSON.stringify({
        v: 1, t: 'join', role: this.role, show: this.showId,
        peerId: this.peerId, joinedAt: this.joinedAt ?? Date.now(),
        ...(this.screen ? { screen: this.screen } : {}),
        // A1: controls joins present the auth token issued by /api/login
        // (stored in localStorage). Display/mesh roles send none.
        // Per-show passphrase: every role carries the show token if the
        // browser unlocked this show (routes/showauth.go issues it).
        ...(this.role === 'controls' && localStorage.getItem('tp.atoken')
          ? { authToken: localStorage.getItem('tp.atoken') } : {}),
        ...((localStorage.getItem('tp.show.' + this.showId) || '')
          ? { showToken: localStorage.getItem('tp.show.' + this.showId) } : {}),
      }));
    };
    ws.onclose = fail;
    ws.onerror = fail;

    ws.onmessage = (event) => {
      let m;
      try { m = JSON.parse(event.data); } catch { return; }
      switch (m.t) {
        case 'joined': {
          this.joinedAt = m.you?.joinedAt ?? Date.now();
          this.clockOffset = (m.snapshot?.serverTime ?? m.serverTime ?? 0)
            ? (m.snapshot?.serverTime ?? m.serverTime) - Date.now() : this.clockOffset;
          this._adopt(m.snapshot);
          this._mergePeers(m.peers);
          this._setStatus('online');
          this._pruneStaleConnections();
          this._connectToAllKnown();
          this._reElect();
          this._postReconnectSync();
          break;
        }
        case 'screen-rename': {
          // F1: the operator renamed THIS screen — adopt the new identity
          // and rejoin so the registry + hub sessions line up.
          const to = String(m.name || '').trim();
          if (to && to !== this.screen) {
            this.screen = to;
            try { sessionStorage.setItem('tp.screen', to); } catch { /* */ }
            // URL identity outranks storage on next boot — a pinned
            // ?screen= tab would revert to its old name on reload and
            // stop following the operator's assignments.
            try {
              const u = new URL(location.href);
              if (u.searchParams.has('screen')) {
                u.searchParams.set('screen', to);
                history.replaceState(history.state, '', u);
              }
            } catch { /* */ }
            try { this._ws?.close(); } catch { /* */ }
            this._wsConnect();
          }
          break;
        }
        case 'state':
          if (m.snapshot) this._adopt(m.snapshot);
          this.onFrame(m);
          break;
        case 'peers':
          this._mergePeers(m.peers);
          this._connectToAllKnown();
          this._reElect();
          this.onFrame(m);
          break;
        case 'signal':
          this._handleSignal(m.from, m.data);
          break;
        case 'pong':
          if (m.serverTime) this.clockOffset = m.serverTime - Date.now();
          break;
        case 'err':
          this.onLog('error', m.message || 'server error');
          if (/session deleted/i.test(m.message || '')) {
            // Operator deleted this session (gallery/panel action): the
            // display returns to its LAUNCH state — the /d/ ready surface
            // re-registers it in the waiting room, ready to be captured by
            // any show (owner rule: disconnect ⇒ launch state). Auto-
            // reconnect here would undo the delete, and a hydrated page
            // keeps showing a show that disowned it.
            this._deleted = true;
            try { this._ws.close(); } catch { /* */ }
            if (this.role === 'screen' || this.role === 'display') {
              try { location.href = '/d/?screen=' + encodeURIComponent(screenName()); } catch { location.href = '/d/'; }
            }
            return;
          }
          if (/unknown (show|session code)/i.test(m.message || '')) {
            this._setStatus('badshow');
            try { this._ws.close(); } catch { /* */ }
          }
          if (m.role === undefined && /operator password required/.test(m.message || '')
              && this.role === 'controls' && !/\/login/.test(location.pathname)) {
            // A1: our controls join was refused (password set elsewhere or
            // token stale) — send the tab to the login page and come back.
            try { this._ws.close(); } catch { /* */ }
            location.href = '/login?next=' + encodeURIComponent(location.pathname);
          }
          break;
        default:
          // oob | timer | cue | schedule | message | display … app-owned frames
          this.onFrame(m);
      }
    };
  }

  _wsDown(ws) {
    if (this._ws !== ws || this._closed) return;
    this._ws = null; // detach: the paired close/error of THIS socket must not re-penalize
    this._setStatus('offline');
    clearTimeout(this._wsReconnectTimer);
    this._wsBackoff = Math.min(this._wsBackoff * 1.5, 8000);
    this._wsReconnectTimer = setTimeout(() => this._wsConnect(), this._wsBackoff);
  }

  /** Called after (re)joining: push our state up if we advanced it offline. */
  async _postReconnectSync() {
    if (!this.serverOnline() || !this.isMaster() || !this._advancedOffline) return;
    const sentTombstones = Array.isArray(this.snap?.tombstones)
      ? this.snap.tombstones.map(t => t.id)
      : [];
    try {
      const res = await fetch(`/api/shows/${this.showId}/sync`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(this._syncPayload()),
      });
      const out = await res.json();
      // Convergence first: a refusal (stale) still carries the server's
      // snapshot — adopt it so the retry below pushes reconciled state.
      if (out.snapshot) this._adopt(out.snapshot);
      if (!res.ok || !out.ok) {
        // NOT landed: keep _advancedOffline set — the heartbeat retries.
        // Clearing it here silently stranded offline edits on a 401/409.
        this.onLog('warn', `offline push refused (${res.status}) — will retry`);
        return;
      }
      this._advancedOffline = false;
      if (out.ok) {
        // The merge applied our deletes — drop the acknowledged tombstones
        // (adopt unioned them back; anything the server rejected stays).
        if (this.snap && Array.isArray(this.snap.tombstones)) {
          const sent = new Set(sentTombstones);
          this.snap.tombstones = this.snap.tombstones.filter(t => !sent.has(t.id));
        }
      }
      // Report remote changes so the operator sees the merge.
      if (out.ok && out.merged && Number(out.mergedCues) > 0) {
        this.onMerged(Number(out.mergedCues));
      }
      this.onLog('info', 'mesh state pushed to server');
    } catch (err) {
      this.onLog('error', `sync failed: ${err}`);
    }
  }

  /** PROTOCOL snapshot shape (what POST /api/shows/:id/sync accepts) + the
   *  OFFLINE-EDIT additive fields: per-cue updatedAt rides each cue object,
   *  offline deletes ride `tombstones` (cap 200). Bodies without either fall
   *  back to whole-snapshot LWW server-side. */
  _syncPayload() {
    const s = this.snap;
    return {
      updatedAt: s.updatedAt,
      show: s.show ? { ...s.show } : undefined,
      runtime: { ...s.runtime },
      cues: s.cues.map(c => ({ ...c })),
      messages: s.messages.map(m => ({ ...m })),
      tombstones: Array.isArray(s.tombstones) ? s.tombstones.map(t => ({ ...t })) : [],
    };
  }

  /* ------------------------------ WebRTC ---------------------------- */

  _signal(to, data) {
    if (this.serverOnline()) {
      this._ws.send(JSON.stringify({ t: 'signal', to, data }));
    }
  }

  /** Drop peer entries that never opened (pc stuck `new`/`connecting`,
      half-negotiated before the outage) so re-join can re-offer them. */
  _pruneStaleConnections() {
    for (const [peerId, entry] of this.connections) {
      if (entry.dc?.readyState === 'open') continue;
      try { entry.pc?.close(); } catch { /* already gone */ }
      this.connections.delete(peerId);
    }
  }

  _connectToAllKnown() {
    for (const peerId of this.peers.keys()) {
      if (peerId !== this.peerId) this._connect(peerId);
    }
  }

  _connect(peerId) {
    if (this.connections.has(peerId)) return;
    if (!this.joinedAt || this.joinedAt === Infinity) return;
    const known = this.peers.get(peerId);
    if (!known) return;

    const pc = new RTCPeerConnection({ iceServers: [], iceCandidatePoolSize: 0 });
    const entry = { pc, dc: null, peerId };
    this.connections.set(peerId, entry);

    pc.onicecandidate = (e) => {
      if (e.candidate) this._signal(peerId, { candidate: e.candidate.toJSON() });
    };
    pc.onconnectionstatechange = () => {
      if (['failed', 'closed', 'disconnected'].includes(pc.connectionState)) {
        this.connections.delete(peerId);
        this._reElect();
      }
    };

    // The newer joiner initiates toward older peers (older peer answers).
    if (known.joinedAt <= this.joinedAt) {
      const dc = pc.createDataChannel(SIGNALING_CHANNEL);
      entry.dc = dc;
      this._attachDataChannel(dc, peerId);
      pc.onnegotiationneeded = async () => {
        await pc.setLocalDescription(await pc.createOffer());
        this._signal(peerId, { description: pc.localDescription.toJSON?.() ?? pc.localDescription });
      };
    } else {
      pc.ondatachannel = (e) => {
        entry.dc = e.channel;
        this._attachDataChannel(e.channel, peerId);
      };
    }
  }

  async _handleSignal(fromId, data) {
    if (!data) return;
    // Glare avoidance: the older peer is the polite side (answers).
    if (data.description) {
      let entry = this.connections.get(fromId);
      if (!entry) {
        if (data.description.type !== 'offer') return;
        const pc = new RTCPeerConnection({ iceServers: [], iceCandidatePoolSize: 0 });
        entry = { pc, dc: null, peerId: fromId };
        this.connections.set(fromId, entry);
        pc.onicecandidate = (e) => {
          if (e.candidate) this._signal(fromId, { candidate: e.candidate.toJSON() });
        };
        pc.ondatachannel = (e) => {
          entry.dc = e.channel;
          this._attachDataChannel(e.channel, fromId);
        };
        pc.onconnectionstatechange = () => {
          if (['failed', 'closed', 'disconnected'].includes(pc.connectionState)) {
            this.connections.delete(fromId);
            this._reElect();
          }
        };
      }
      try {
        await entry.pc.setRemoteDescription(data.description);
        if (data.description.type === 'offer') {
          await entry.pc.setLocalDescription(await entry.pc.createAnswer());
          this._signal(fromId, { description: entry.pc.localDescription });
        }
      } catch (err) {
        this.onLog('error', `webrtc: ${err}`);
      }
    } else if (data.candidate) {
      try { await this.connections.get(fromId)?.pc.addIceCandidate(data.candidate); }
      catch { /* candidate for a gathering-transient */ }
    }
  }

  _attachDataChannel(dc, peerId) {
    dc.onopen = () => {
      dc.send(JSON.stringify({
        t: 'hi', meta: { peerId: this.peerId, role: this.role, joinedAt: this.joinedAt },
        snap: this.snap, now: this.now(),
      }));
      this._reElect();
    };
    dc.onclose = () => {
      this.connections.delete(peerId);
      this._reElect();
    };
    dc.onmessage = (event) => {
      let m;
      try { m = JSON.parse(event.data); } catch { return; }
      switch (m.t) {
        case 'hi':
        case 'hi-ack': {
          const known = this.peers.get(peerId) || { peerId };
          this.peers.set(peerId, {
            ...known, peerId,
            role: m.meta?.role ?? known.role,
            joinedAt: m.meta?.joinedAt ?? known.joinedAt,
          });
          if (m.snap) this._adopt({ ...m.snap, serverTime: m.now });
          if (m.t === 'hi' && dc.readyState === 'open') {
            dc.send(JSON.stringify({
              t: 'hi-ack', meta: { peerId: this.peerId, role: this.role, joinedAt: this.joinedAt },
              snap: this.snap, now: this.now(),
            }));
          }
          this._reElect();
          break;
        }
        case 'mesh-state':
          if (m.snap) this._adopt({ ...m.snap, serverTime: m.now });
          break;
        case 'mesh-cmd':
          if (this.isMaster()) this._applyLocalCommand(m.action, m.args || {});
          break;
        default: break;
      }
    };
  }

  /* --------------------------- master logic ------------------------- */

  _reElect() {
    const candidates = [
      { peerId: this.peerId, role: this.role, joinedAt: this.joinedAt ?? Infinity },
      // C2 drill fix: a live-but-still-handshaking channel is also valid
      // determinism input. Gating on openPeerIds() alone let TWO tabs
      // crown themselves while their channels were 'connecting' (or if the
      // browser gathers no candidates, c2 sandbox note) — each ran as
      // mesh master after a server death, splitting the ledger. We count
      // every pc that isn't provably dead; its joinedAt comes from the
      // hub's peers list or a 'hi' meta exchange, never guessed.
      ...[...this.connections.entries()]
        .filter(([, e]) => {
          const st = e.pc?.connectionState;
          return st && !['failed', 'closed', 'disconnected'].includes(st);
        })
        .map(([id]) => this.peerInfo(id)),
    ];
    let best = null;
    for (const c of candidates) {
      if (c.joinedAt === undefined || c.joinedAt === Infinity) continue;
      if (!best || c.joinedAt < best.joinedAt || (c.joinedAt === best.joinedAt && c.peerId < best.peerId)) {
        best = c;
      }
    }
    const newMaster = best?.peerId ?? this.peerId;
    if (newMaster !== this.masterId) {
      const wasMaster = this.isMaster();
      this.masterId = newMaster;
      if (this.isMaster() && !wasMaster) this._becomeMaster();
      else if (!this.isMaster() && wasMaster) this._resignMaster();
      this.onStatusChange();
    }
  }

  peerInfo(peerId) {
    if (peerId === this.peerId) {
      return { peerId: this.peerId, role: this.role, joinedAt: this.joinedAt ?? Infinity };
    }
    const known = this.peers.get(peerId) || {};
    return { peerId, role: known.role || '?', joinedAt: known.joinedAt ?? Infinity };
  }

  _becomeMaster() {
    clearInterval(this._zeroTimer);
    if (!this.snap) return;
    this.onLog('info', 'acting as mesh master');
    this._meshBroadcast({ t: 'mesh-state', snap: this.snap, now: this.now() });
    this._zeroTimer = setInterval(() => this._zeroTick(), 250);
  }

  _resignMaster() {
    clearInterval(this._zeroTimer);
    this.onLog('info', 'master handed off');
  }

  _zeroTick() {
    if (!this.isMaster() || this.serverOnline() || !this.snap) return;
    if (tickZeroCrossing(this.snap, this.now())) {
      this._advancedOffline = true; // zero marched past us off-grid: push on reconnect (REVIEW-3 D1)
      this._meshBroadcast({ t: 'mesh-state', snap: this.snap, now: this.now() });
      this.onSnapshot();
    }
  }

  _applyLocalCommand(action, args) {
    if (!this.snap) return;
    const applied = applyCommand(this.snap, action, args, this.now()) === 'applied';
    if (applied) {
      this._advancedOffline = true;
      this._meshBroadcast({ t: 'mesh-state', snap: this.snap, now: this.now() });
      this.onSnapshot();
    }
  }

  _heartbeat() {
    if (!this.isMaster() || !this.snap) return;
    if (this.serverOnline()) {
      // Server up: the hub already fans state to every tab — a mesh-state
      // flood on top re-renders all peers (equal stamps pass the stale
      // guard) for nothing. The master only re-pushes offline advances
      // whose reconnect sync was refused.
      if (this._advancedOffline) this._postReconnectSync();
      return;
    }
    // Master heartbeat (offline): keeps late adopters and drift in check.
    this._meshBroadcast({ t: 'mesh-state', snap: this.snap, now: this.now() });
  }

  _meshBroadcast(message) {
    for (const { dc } of this.connections.values()) {
      if (dc?.readyState === 'open') {
        try { dc.send(JSON.stringify(message)); } catch { /* */ }
      }
    }
  }

  /* ------------------------ state adoption -------------------------- */

  _adopt(incoming) {
    if (!incoming) return;
    // Guard against cross-show WS glare. Agent L: joins carry the CODE, so
    // compare on show.code (snapshot always ships it post-migration); the
    // numeric show.id stays the snapshot's bookkeeping value and no longer
    // matches this.showId (the join identity).
    if (incoming.show && this.showId && incoming.show.code &&
        String(incoming.show.code) !== String(this.showId)) return;
    if (this.snap && (incoming.updatedAt ?? 0) < this.snap.updatedAt) return; // stale

    const fresh = !this.snap;
    this.snap = {
      updatedAt: incoming.updatedAt || 0,
      serverTime: incoming.serverTime || 0,
      show: incoming.show ? { ...incoming.show } : { id: Number(this.showId) || this.showId, title: this.snap?.show?.title || '' },
      runtime: { ...incoming.runtime },
      cues: (incoming.cues || []).map(c => ({ ...c })),
      messages: (incoming.messages || []).map(m => ({ ...m })),
      // Offline-delete tombstones propagate through the mesh with the state
      // (server snapshots never carry them). Adopt UNIONs them with the ones
      // already held: on rejoin _adopt runs BEFORE the push, so replacing
      // would drop unsynced deletes. The push clears what the server
      // acknowledged (see _postReconnectSync). Cap 200, newest wins per id.
      tombstones: unionTombstones(this.snap?.tombstones, incoming.tombstones),
    };
    if (incoming.serverTime) this.clockOffset = incoming.serverTime - Date.now();
    this.onSnapshot(fresh);
    this.onStatusChange();
  }

  _mergePeers(list) {
    for (const p of list || []) {
      if (!p || p.peerId === this.peerId) continue;
      const known = this.peers.get(p.peerId) || {};
      this.peers.set(p.peerId, { ...known, ...p });
    }
    this.onStatusChange();
  }

  _setStatus(status) {
    if (this.wsStatus === status) return;
    if (status === 'online') this._wsBackoff = 800;
    this.wsStatus = status;
    this.onStatusChange();
  }
}

function randId() {
  return 'p-' + Math.random().toString(36).slice(2) + Date.now().toString(36);
}

/**
 * Union two tombstone sets (existing hold + incoming state): newest stamp
 * wins per id, malformed entries dropped, capped at 200 (OFFLINE-EDIT.md).
 */
function unionTombstones(a, b) {
  const byId = new Map();
  for (const list of [a, b]) {
    if (!Array.isArray(list)) continue;
    for (const t of list) {
      if (!t || typeof t.id !== 'number' || t.deleted !== true) continue;
      const cur = byId.get(t.id);
      const ts = t.updatedAt || 0;
      if (!cur || ts > (cur.updatedAt || 0)) {
        byId.set(t.id, { id: t.id, deleted: true, updatedAt: ts });
      }
    }
  }
  return [...byId.values()]
    .sort((x, y) => (y.updatedAt || 0) - (x.updatedAt || 0))
    .slice(0, 200);
}
