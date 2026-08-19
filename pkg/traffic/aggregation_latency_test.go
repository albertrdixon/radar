package traffic

import (
	"math"
	"testing"
)

// A source that measures latency without emitting L7 record types must still
// reach the aggregate. Gating on the record type alone leaves the raw flow showing
// a value while every aggregated latency field stays empty, so the graph's edge
// label renders nothing.
func TestAggregateFlowsRecordsLatencyFromAnySourceThatMeasuredIt(t *testing.T) {
	flows := []Flow{{
		Source:      Endpoint{Namespace: "demo", Name: "client"},
		Destination: Endpoint{Namespace: "demo", Name: "web"},
		Port:        80,
		LatencyNs:   2_000_000, // 2ms, and no L7Type: a metric-based source
	}}

	agg := AggregateFlows(flows)
	if len(agg) != 1 {
		t.Fatalf("expected 1 aggregated flow, got %d", len(agg))
	}
	if agg[0].LatencyP50Ms != 2 {
		t.Errorf("latencyP50Ms = %v, want 2 — a measured latency must reach the aggregate", agg[0].LatencyP50Ms)
	}
}

// Hubble's behaviour is unchanged: it sets LatencyNs only on responses, so a
// request-type flow contributes nothing either way.
func TestAggregateFlowsIgnoresFlowsWithNoMeasuredLatency(t *testing.T) {
	flows := []Flow{{
		Source:      Endpoint{Namespace: "demo", Name: "client"},
		Destination: Endpoint{Namespace: "demo", Name: "web"},
		Port:        80,
		L7Type:      "REQUEST",
		LatencyNs:   0,
	}}

	agg := AggregateFlows(flows)
	if len(agg) != 1 {
		t.Fatalf("expected 1 aggregated flow, got %d", len(agg))
	}
	if agg[0].LatencyP50Ms != 0 {
		t.Errorf("latencyP50Ms = %v, want 0", agg[0].LatencyP50Ms)
	}
}

// The per-path latency in Top Paths had the same gate as the edge latency, so
// fixing only the first left Top Paths empty for a metric-based source while the
// edge percentiles filled in.
func TestAggregateFlowsRecordsPerPathLatencyWithoutARecordType(t *testing.T) {
	flows := []Flow{{
		Source:      Endpoint{Namespace: "demo", Name: "client"},
		Destination: Endpoint{Namespace: "demo", Name: "web"},
		Port:        80,
		HTTPMethod:  "GET",
		HTTPPath:    "/",
		LatencyNs:   3_000_000, // 3ms, no L7Type
	}}

	agg := AggregateFlows(flows)
	if len(agg) != 1 || len(agg[0].TopHTTPPaths) != 1 {
		t.Fatalf("expected one aggregated flow with one path, got %+v", agg)
	}
	if got := agg[0].TopHTTPPaths[0].AvgMs; got != 3 {
		t.Errorf("path AvgMs = %v, want 3 — the same measurement the edge latency uses", got)
	}
}

// The graph reads directionUnknown off the aggregated edge, so this propagation is
// the only thing standing between the source marking a conversation unoriented and
// the arrowhead disappearing.
//
// Whether a conversation could be oriented is part of an edge's identity. A source
// that cannot orient some of its traffic reports it with port 0, which is also the
// port every flow carries when the port attribute is absent, so merging the two
// would produce one edge that has to answer a single question — does this have a
// direction — wrongly for half of what it contains. They stay separate instead.
func TestAggregateFlowsKeepsOrientedAndUnorientedTrafficApart(t *testing.T) {
	pair := func(unknown bool, sent int64) Flow {
		return Flow{
			Source:           Endpoint{Namespace: "demo", Name: "client"},
			Destination:      Endpoint{Namespace: "kube-system", Name: "coredns"},
			Port:             0,
			BytesSent:        sent,
			DirectionUnknown: unknown,
		}
	}

	allUnknown := AggregateFlows([]Flow{pair(true, 10), pair(true, 10)})
	if len(allUnknown) != 1 {
		t.Fatalf("expected 1 aggregated edge, got %d", len(allUnknown))
	}
	if !allUnknown[0].DirectionUnknown {
		t.Error("nothing established a direction, so the edge must not claim one")
	}

	oriented := AggregateFlows([]Flow{pair(false, 10), pair(false, 10)})
	if oriented[0].DirectionUnknown {
		t.Error("an edge whose contributors are all oriented must keep its arrowhead")
	}

	// The load-bearing case: one pair carrying both kinds at once.
	mixed := AggregateFlows([]Flow{pair(true, 7), pair(false, 300)})
	if len(mixed) != 2 {
		t.Fatalf("oriented and unoriented traffic must not merge into one edge, got %d edges", len(mixed))
	}
	byDirection := map[bool]AggregatedFlow{}
	for _, m := range mixed {
		byDirection[m.DirectionUnknown] = m
	}
	if got := byDirection[true].BytesSent; got != 7 {
		t.Errorf("unoriented edge carries the unoriented bytes: got %d, want 7", got)
	}
	if got := byDirection[false].BytesSent; got != 300 {
		t.Errorf("oriented edge carries the oriented bytes: got %d, want 300", got)
	}
}

// RoundRate, not a bare round: a 5xx rate below 0.5/s is a real failure and must
// not round away to no error at all, while a zero rate must never invent one.
func TestAggregateFlowsKeepsALowErrorRateVisible(t *testing.T) {
	edge := func(errorRate float64) AggregatedFlow {
		return AggregateFlows([]Flow{{
			Source:      Endpoint{Namespace: "demo", Name: "client"},
			Destination: Endpoint{Namespace: "demo", Name: "web"},
			ErrorRate:   errorRate,
		}})[0]
	}

	if got := edge(0.003).ErrorCount; got != 1 {
		t.Errorf("a real but slow failure must stay visible: got %d, want 1", got)
	}
	if got := edge(0).ErrorCount; got != 0 {
		t.Errorf("no errors must not become an error: got %d, want 0", got)
	}
	if got := edge(math.NaN()).ErrorCount; got != 0 {
		t.Errorf("NaN has no defined int64 conversion and must be rejected: got %d, want 0", got)
	}
	if got := edge(4.2).ErrorCount; got != 4 {
		t.Errorf("an ordinary rate still rounds: got %d, want 4", got)
	}
}
