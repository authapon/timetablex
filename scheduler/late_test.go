package scheduler

import (
	"testing"

	"timetablex/types"
)

// Late periods should be shared between people rather than piled up on one person.
func TestLatePenaltySpreadsLoad(t *testing.T) {
	// two people with 2 late periods each, on 1 day each
	spread := 2 * latePenaltyDelta(0, 0, 2, 1, 2)
	// one person with 4 late periods over 2 days, the other with none
	piled := latePenaltyDelta(0, 0, 4, 2, 4)
	if spread >= piled {
		t.Fatalf("spreading late periods (%.0f) should cost less than piling them up (%.0f)", spread, piled)
	}

	// late classes on many days cost more than the same periods on one day
	oneDay := latePenaltyDelta(0, 0, 4, 1, 4)
	fourDays := latePenaltyDelta(0, 0, 4, 4, 4)
	if oneDay >= fourDays {
		t.Fatalf("one late day (%.0f) should cost less than four (%.0f)", oneDay, fourDays)
	}
}

// A person who already has late classes should be a worse choice for another late class
// than a person who has none.
func TestPositionScorePrefersPersonWithoutLateClasses(t *testing.T) {
	cfg := limitConfig(2)
	s := NewScheduler(cfg, 1, 1, false)
	u0 := unit{offering: cfg.Offerings[0], theoryPeriods: 4, totalPeriods: 4}
	u1 := unit{offering: cfg.Offerings[1], theoryPeriods: 4, totalPeriods: 4}

	// i0 already teaches periods 10-13 on Saturday; i1 has no late classes
	instB := map[string]map[resourceKey]bool{"i0": {}}
	for p := 10; p <= 13; p++ {
		instB["i0"][resourceKey{day: types.Saturday, period: p}] = true
	}
	grpB := map[string]map[resourceKey]bool{}

	late := s.positionScore(u0, types.Sunday, 10, instB, grpB)
	fresh := s.positionScore(u1, types.Sunday, 10, instB, grpB)
	if late <= fresh {
		t.Fatalf("late-loaded instructor (%.0f) should score worse than a fresh one (%.0f)", late, fresh)
	}
}

// An early slot must always score better than a late one for the same unit.
func TestPositionScorePrefersEarlyPeriods(t *testing.T) {
	cfg := limitConfig(1)
	s := NewScheduler(cfg, 1, 1, false)
	u := unit{offering: cfg.Offerings[0], theoryPeriods: 4, totalPeriods: 4}
	none := map[string]map[resourceKey]bool{}

	early := s.positionScore(u, types.Saturday, 1, none, none)
	late := s.positionScore(u, types.Saturday, 10, none, none)
	if early >= late {
		t.Fatalf("early (%.0f) should score better than late (%.0f)", early, late)
	}
}
