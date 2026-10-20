package validator

import (
	"strings"
	"testing"

	"timetablex/types"
)

// A co-instructor is only busy during the lab block of their own class, so being the main
// instructor of another class in a period that is only the other class's *theory* block of a
// class where they are co-instructor is not a conflict (and vice versa).
func TestPredefinedCoInstructorOnlyConflictsInOwnLabBlock(t *testing.T) {
	config := &types.Config{
		Instructors: map[string]*types.Instructor{"Chon": {ID: "Chon"}, "Tiny": {ID: "Tiny"}},
		Groups:      map[string]*types.Group{"g1": {ID: "g1"}, "g2": {ID: "g2"}},
		Rooms:       map[string]*types.Room{},
		Courses:     map[string]*types.Course{"a": {ID: "a"}, "b": {ID: "b"}},
		Breaks:      &types.Break{},
	}
	data := &types.PredefinedData{Assignments: []*types.PredefinedAssignment{
		// Chon is main, lab-only class in period 1-2
		{LineNumber: 1, CourseID: "a", LabPeriods: 2, GroupIDs: []string{"g1"}, Day: types.Thursday,
			LabStart: 1, MainInstructorID: "Chon", TheoryRoomID: "x", LabRoomID: "x"},
		// Chon is co-instructor of a class whose theory block is periods 1-2 and lab 3-5
		{LineNumber: 2, CourseID: "b", TheoryPeriods: 2, LabPeriods: 3, GroupIDs: []string{"g2"}, Day: types.Thursday,
			TheoryStart: 1, LabStart: 3, MainInstructorID: "Tiny", CoInstructorIDs: []string{"Chon"},
			TheoryRoomID: "x", LabRoomID: "x"},
	}}

	for _, e := range ValidatePredefined(config, data) {
		if strings.Contains(e.Message, "double-booked") {
			t.Errorf("unexpected conflict: %v", e)
		}
	}

	// Moving Chon's own class into the co-instructor's lab block is a real conflict
	data.Assignments[0].LabStart = 3
	found := false
	for _, e := range ValidatePredefined(config, data) {
		if strings.Contains(e.Message, "double-booked") {
			found = true
		}
	}
	if !found {
		t.Error("expected a double-booking when Chon's class overlaps the lab block he co-teaches")
	}
}
