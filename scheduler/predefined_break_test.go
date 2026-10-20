package scheduler

import (
	"testing"

	"timetablex/types"
)

func breakConfig() *types.Config {
	return &types.Config{
		Instructors: map[string]*types.Instructor{
			"Gorn": {ID: "Gorn", Name: "Gorn"},
			"Moo":  {ID: "Moo", Name: "Moo"},
			"Kai":  {ID: "Kai", Name: "Kai"},
		},
		Groups:  map[string]*types.Group{"g1": {ID: "g1", TermType: types.Normal, Name: "G1"}, "g2": {ID: "g2", TermType: types.Normal, Name: "G2"}},
		Rooms:   map[string]*types.Room{},
		Courses: map[string]*types.Course{},
		Breaks:  &types.Break{Periods: []int{5, 6}},
		InstructorUnavailable: []*types.InstructorUnavailable{
			// Gorn is booked (with a visible message) in periods 6-10 on Friday
			{InstructorID: "Gorn", Day: types.Friday, StartPeriod: 6, EndPeriod: 10, RoomID: "none", Mode: types.ModeMessage, Message: "class"},
		},
	}
}

// Lunch is chosen per instructor and per day: another person's pre-defined class in period 5
// must not push everybody to period 6, and a person's own booking in period 6 must not
// leave them with a lunch in period 6.
func TestPredefinedOnlyScheduleChoosesLunchPerPerson(t *testing.T) {
	s := NewScheduler(breakConfig(), 1, 1, false)
	s.SetPredefinedAssignments([]*types.PredefinedAssignment{
		// Moo teaches (group g1) on Tuesday periods 3-5 -> Moo cannot take period 5 on Tuesday
		{CourseID: "c", TheoryPeriods: 3, GroupIDs: []string{"g1"}, Day: types.Tuesday, TheoryStart: 3,
			MainInstructorID: "Moo", TheoryRoomID: "x", LabRoomID: "x"},
	})

	sch := s.PredefinedOnlySchedule()

	if got := sch.InstructorLunchBreak["Gorn"][types.Friday]; got != 5 {
		t.Errorf("Gorn is booked 6-10 on Friday, lunch should be period 5, got %d", got)
	}
	if got := sch.InstructorLunchBreak["Moo"][types.Tuesday]; got != 6 {
		t.Errorf("Moo teaches period 5 on Tuesday, lunch should be period 6, got %d", got)
	}
	// Moo's class must not affect the other people on the same day
	if got := sch.InstructorLunchBreak["Kai"][types.Tuesday]; got != 5 {
		t.Errorf("Kai has no class on Tuesday, lunch should be period 5, got %d", got)
	}
	if got := sch.GroupLunchBreak["g2"][types.Tuesday]; got != 5 {
		t.Errorf("group g2 has no class on Tuesday, lunch should be period 5, got %d", got)
	}
	if got := sch.GroupLunchBreak["g1"][types.Tuesday]; got != 6 {
		t.Errorf("group g1 has class in period 5 on Tuesday, lunch should be period 6, got %d", got)
	}
	if got := sch.InstructorBreakPriority["Gorn"][types.Friday]; got != 1 {
		t.Errorf("period 5 is the first-priority break, got priority %d", got)
	}
}

// A co-instructor is busy only during the lab block of the class.
func TestInstructorHasPredefinedAtCoInstructorLabOnly(t *testing.T) {
	s := NewScheduler(breakConfig(), 1, 1, false)
	s.SetPredefinedAssignments([]*types.PredefinedAssignment{
		{CourseID: "c", TheoryPeriods: 2, LabPeriods: 3, GroupIDs: []string{"g1"}, Day: types.Monday,
			TheoryStart: 1, LabStart: 3, MainInstructorID: "Moo", CoInstructorIDs: []string{"Kai"}},
	})
	if s.instructorHasPredefinedAt("Kai", types.Monday, 2) {
		t.Error("co-instructor does not teach during the theory block")
	}
	if !s.instructorHasPredefinedAt("Kai", types.Monday, 4) {
		t.Error("co-instructor teaches during the lab block")
	}
	if !s.instructorHasPredefinedAt("Moo", types.Monday, 2) {
		t.Error("main instructor teaches during the theory block")
	}
}
