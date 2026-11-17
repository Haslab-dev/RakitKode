//go:build race

package brain

// The 5ms BriefFor latency budget is defined for the normal (non-race)
// build. The race detector instruments every memory access, multiplying
// numeric kernel cost several-fold; relax the budget proportionally there
// instead of making the shipped budget loose.
const panduPerfSlack = 4
