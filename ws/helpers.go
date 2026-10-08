// package ws — helpers.go: small shared utilities (wire shapes, tiny stdlib
// shims kept local so call sites stay one word).
package ws

import "sort"

// wire() renders a peers list for JSON frames as [{peerId,role,joinedAt}].
// F1: named displays also carry {screen} — the operator screens panel
// reads live presence from the peers frames.
func (p peerViews) wire() any {
	out := make([]map[string]any, len(p))
	for i, pv := range p {
		m := map[string]any{"peerId": pv.PeerID, "role": pv.Role, "joinedAt": pv.JoinedAt, "trusted": pv.Trusted}
		if pv.Screen != "" {
			m["screen"] = pv.Screen
		}
		out[i] = m
	}
	return out
}

// sortPeers orders by joinedAt (master-election input: earliest wins).
func sortPeers(p peerViews) {
	sort.Slice(p, func(i, j int) bool { return p[i].JoinedAt < p[j].JoinedAt })
}
