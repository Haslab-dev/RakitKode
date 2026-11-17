//go:build !race

package brain

// Non-race build: the latency budget applies as documented (5 ms).
const panduPerfSlack = 1
