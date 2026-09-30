// Package diff generates simple unified diffs between two strings.
package diff

import (
	"fmt"
	"strings"

	"github.com/aymanbagabas/go-udiff"
)

// Generate renders a unified diff between old and new content.
func Generate(oldContent, newContent string) string {
	if oldContent == newContent {
		return ""
	}
	diff := udiff.Unified("a/"+"file", "b/"+"file", oldContent, newContent)
	return diff
}

// GenerateLabels renders a unified diff with custom file labels.
func GenerateLabels(labelOld, labelNew, oldContent, newContent string) string {
	if oldContent == newContent {
		return ""
	}
	return udiff.Unified(labelOld, labelNew, oldContent, newContent)
}

// CountChanges returns the added and removed line counts of a unified diff.
func CountChanges(d string) (additions, removals int) {
	for line := range strings.SplitSeq(d, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			additions++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			removals++
		}
	}
	return additions, removals
}

// Summary renders a compact "+a -r" change summary.
func Summary(additions, removals int) string {
	return fmt.Sprintf("+%d -%d", additions, removals)
}
