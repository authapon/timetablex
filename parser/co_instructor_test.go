package parser

import (
	"testing"

	"timetablex/types"
)

func TestParseCommaOrXNone(t *testing.T) {
	for _, in := range []string{"x", "none", "None", " NONE ", ""} {
		if got := parseCommaOrX(in); len(got) != 0 {
			t.Errorf("parseCommaOrX(%q) = %v, want no entries", in, got)
		}
	}
	if got := parseCommaOrX("i1,i2"); len(got) != 2 {
		t.Errorf("expected 2 co-instructors, got %v", got)
	}
}

func TestPredefinedAssignmentCoInstructorNone(t *testing.T) {
	for _, co := range []string{"none", "x"} {
		result := &types.PredefinedData{}
		line := "A C1 2 0 G1 mo 1 0 R1 x I1 " + co
		if perr := parsePredefinedA(result, line, 1); perr != nil {
			t.Fatalf("co=%s: %v", co, perr.Message)
		}
		if got := result.Assignments[0].CoInstructorIDs; len(got) != 0 {
			t.Errorf("co=%s: expected no co-instructors, got %v", co, got)
		}
	}
}
