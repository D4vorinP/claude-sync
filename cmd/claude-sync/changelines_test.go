package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintChangeLinesNamesEachFileUnderItsHeading(t *testing.T) {
	var out bytes.Buffer
	printChangeLines(&out, "Files pushed to R2:", []string{"added   a.json", "deleted b.json"})

	got := out.String()
	for _, want := range []string{"Files pushed to R2:", "  added   a.json\n", "  deleted b.json\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q lacks %q", got, want)
		}
	}
}

func TestPrintChangeLinesPrintsNothingWithoutFiles(t *testing.T) {
	var out bytes.Buffer
	printChangeLines(&out, "Files pushed to R2:", nil)
	if out.Len() != 0 {
		t.Errorf("printed %q for no files", out.String())
	}
}
