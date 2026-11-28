// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

// CollectorStampKey is the resource attribute a collector managed by
// Sluicio puts on the data it carries: its own instance id, set by the
// first collector on the way (later ones leave it), so Sluicio can tell
// which collector a metric, log or span came through.
//
// It is taken out of the resource attributes at ingest and kept in a
// column of its own (CollectorId). Left in the map it would be part of
// every series' identity: a counter that reaches the cell alternately
// through two load-balanced gateways would split into two series that
// each rise across the whole window, and a rate or increase summed over
// them would overcount. Matchers, facets and series never see it.
const CollectorStampKey = "sluicio.collector.id"

// takeCollectorStamp removes the stamp from a resource's attributes and
// returns it ("" when the data was not stamped).
func takeCollectorStamp(resourceAttrs map[string]string) string {
	id, ok := resourceAttrs[CollectorStampKey]
	if !ok {
		return ""
	}
	delete(resourceAttrs, CollectorStampKey)
	return id
}
