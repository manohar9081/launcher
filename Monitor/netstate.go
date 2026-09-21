// Shared, thread-safe state of current network activity, served to the
// dashboard (mirrors monitor/netstate.py).
package monitor

import (
	"sync"
)

// DirectionOf is the heuristic connection direction; documented as best-effort.
// Mirrors Python: int() conversion failure -> "outbound".
func DirectionOf(localPort, remotePort any) string {
	lp, lpOK := ToInt(localPort)
	rp, rpOK := ToInt(remotePort)
	if !lpOK || !rpOK {
		return "outbound"
	}
	if (rp != 0 && rp < 1024) || ServiceName(rp) != "" {
		return "outbound"
	}
	if (lp != 0 && lp < 1024) || ServiceName(lp) != "" {
		return "inbound"
	}
	if lp >= 49152 && rp >= 49152 {
		return "outbound"
	}
	return "outbound"
}

// Rate is a traffic rate entry: {"in", "out", "total_in", "total_out"}.
type Rate struct {
	In       float64 `json:"in"`
	Out      float64 `json:"out"`
	TotalIn  int64   `json:"total_in"`
	TotalOut int64   `json:"total_out"`
}

// HistoryPoint is one traffic-history sample: {"ts", "in", "out"}.
type HistoryPoint struct {
	Ts  float64 `json:"ts"`
	In  float64 `json:"in"`
	Out float64 `json:"out"`
}

// NetState holds the latest connection table + per-app traffic rates +
// interface totals history.
type NetState struct {
	mu          sync.Mutex
	connections []map[string]any
	appRates    map[string]Rate
	ifaces      map[string]Rate
	history     []HistoryPoint
	historyLen  int
	updatedAt   float64
}

// NewNetState creates the shared network state.
func NewNetState(historyLen int) *NetState {
	return &NetState{
		appRates:   map[string]Rate{},
		ifaces:     map[string]Rate{},
		historyLen: historyLen,
	}
}

// UpdateConnections replaces the connection table.
func (n *NetState) UpdateConnections(conns []map[string]any) {
	n.mu.Lock()
	n.connections = conns
	n.updatedAt = Now()
	n.mu.Unlock()
}

// UpdateTraffic stores per-app rates / per-interface rates; when either
// total is non-zero a history sample is appended (mirrors Python's
// "if total_in is not None or total_out is not None").
func (n *NetState) UpdateTraffic(appRates, ifaces map[string]Rate, totalIn, totalOut float64) {
	n.mu.Lock()
	if appRates != nil {
		n.appRates = appRates
	}
	if ifaces != nil {
		n.ifaces = ifaces
	}
	n.history = append(n.history, HistoryPoint{
		Ts:  Now(),
		In:  totalIn,
		Out: totalOut,
	})
	if len(n.history) > n.historyLen {
		n.history = n.history[len(n.history)-n.historyLen:]
	}
	n.mu.Unlock()
}

// UpdatedAt returns the timestamp of the last connection update.
func (n *NetState) UpdatedAt() float64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.updatedAt
}

// Snapshot returns the enriched view served to the dashboard.
func (n *NetState) Snapshot() map[string]any {
	n.mu.Lock()
	conns := make([]map[string]any, len(n.connections))
	copy(conns, n.connections)
	rates := make(map[string]Rate, len(n.appRates))
	for k, v := range n.appRates {
		rates[k] = v
	}
	ifaces := make(map[string]Rate, len(n.ifaces))
	for k, v := range n.ifaces {
		ifaces[k] = v
	}
	hist := make([]HistoryPoint, len(n.history))
	copy(hist, n.history)
	updatedAt := n.updatedAt
	n.mu.Unlock()

	appConnections := map[string]int{}
	for _, conn := range conns {
		app, ok := conn["app"].(string)
		if !ok || app == "" {
			app = "Unknown"
		}
		appConnections[app]++
	}

	enriched := make([]map[string]any, 0, len(conns))
	seenHosts := map[string]string{}
	for _, c := range conns {
		if len(enriched) >= 600 {
			break
		}
		remoteIP, _ := c["remote_ip"].(string)
		host, seen := seenHosts[remoteIP]
		if !seen {
			host = rdns.Get(remoteIP)
			seenHosts[remoteIP] = host
		}
		row := make(map[string]any, len(c)+4)
		for k, v := range c {
			row[k] = v
		}
		row["scope"] = ClassifyIP(remoteIP)
		row["service"] = ServiceName(IntOr(c["remote_port"], 0))
		if row["service"] == "" {
			row["service"] = ServiceName(IntOr(c["local_port"], 0))
		}
		row["direction"] = DirectionOf(c["local_port"], c["remote_port"])
		if host != "" {
			row["host"] = host
		}
		enriched = append(enriched, row)
	}
	return map[string]any{
		"connections":     enriched,
		"app_rates":       rates,
		"app_connections": appConnections,
		"ifaces":          ifaces,
		"history":         hist,
		"updated_at":      updatedAt,
	}
}
