package loadtest

// Scale returns a census with every kind's count multiplied by factor,
// preserving the ratios that drive topology edge-matching. Tests need the
// shape of a reported cluster without materializing its object count; a
// scaled census trips the same code paths (large-cluster guard, summary
// mode, owner collapsing) at a fraction of the cost.
//
// Counts floor at 1 for any kind the source census populates, so scaling
// down never silently drops a kind and changes the graph's topology.
func (c Census) Scale(factor float64) Census {
	scaled := make(Census, len(c))
	for kind, n := range c {
		if n <= 0 {
			continue
		}
		scaled[kind] = max(int(float64(n)*factor), 1)
	}
	return scaled
}
