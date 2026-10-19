package scheduler

import (
	"fmt"
	"testing"

	"timetablex/types"
)

// limitConfig builds a Saturday/Sunday-only group with n offerings of 4 theory periods each.
func limitConfig(n int) *types.Config {
	cfg := &types.Config{
		Instructors: map[string]*types.Instructor{},
		Groups:      map[string]*types.Group{"g1": {ID: "g1", TermType: types.Special, Name: "G1"}},
		Rooms:       map[string]*types.Room{},
		Courses:     map[string]*types.Course{},
		Breaks:      &types.Break{},
	}
	for i := 0; i < n; i++ {
		inst := fmt.Sprintf("i%d", i)
		course := fmt.Sprintf("c%d", i)
		cfg.Instructors[inst] = &types.Instructor{ID: inst, Name: inst}
		cfg.Courses[course] = &types.Course{ID: course, Name: course}
		cfg.Offerings = append(cfg.Offerings, &types.Offering{
			CourseID: course, TheoryPeriods: 4, GroupIDs: []string{"g1"}, GroupIDRaw: "g1",
			MainInstructorID: inst, CoInstructorRaw: "x", TheoryRoomRaw: "x", LabRoomRaw: "x",
		})
	}
	return cfg
}

func lastPeriod(sch *types.Schedule) int {
	last := 0
	for _, a := range sch.Assignments {
		if end := a.TheoryStart + a.Offering.TheoryPeriods - 1; end > last {
			last = end
		}
	}
	return last
}

func TestScheduleStaysWithinPreferredPeriod(t *testing.T) {
	// 16 periods over Sat+Sun fit within periods 1-9
	sch, err := NewScheduler(limitConfig(4), 1, 50, false).Schedule()
	if err != nil {
		t.Fatal(err)
	}
	if got := lastPeriod(sch); got > types.PreferredMaxPeriod {
		t.Fatalf("expected classes within period %d, last period is %d", types.PreferredMaxPeriod, got)
	}
}

func TestScheduleExtendsPastPreferredPeriodOnlyWhenNeeded(t *testing.T) {
	// 24 periods over Sat+Sun cannot fit in 1-9 (18 slots), so periods up to 12 are needed
	sch, err := NewScheduler(limitConfig(6), 1, 200, false).Schedule()
	if err != nil {
		t.Fatal(err)
	}
	got := lastPeriod(sch)
	if got <= types.PreferredMaxPeriod || got > 13 {
		t.Fatalf("expected last period within 10-13, got %d", got)
	}
}
