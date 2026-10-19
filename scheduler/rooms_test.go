package scheduler

import (
	"testing"

	"timetablex/types"
)

func roomOffering() *types.Offering {
	return &types.Offering{
		TheoryRoomIDs: []string{"A", "B"},
		TheoryRoomRaw: "A,B",
		LabRoomIDs:    []string{"A", "B"},
		LabRoomRaw:    "A,B",
	}
}

func TestPickRoomsPrefersSameRoom(t *testing.T) {
	s := NewScheduler(&types.Config{}, 1, 1, false)
	o := roomOffering()
	roomB := map[string]map[resourceKey]bool{}

	th, lab, ok := s.pickRooms(o, types.AllDays()[0], 1, 2, 3, roomB, true)
	if !ok || th != lab {
		t.Fatalf("expected same room, got theory=%s lab=%s ok=%v", th, lab, ok)
	}

	// Room A is busy during the theory block, B is busy during the lab block:
	// no common room exists, so different rooms are used.
	day := types.AllDays()[0]
	roomB["A"] = map[resourceKey]bool{{day: day, period: 1}: true}
	roomB["B"] = map[resourceKey]bool{{day: day, period: 3}: true}
	th, lab, ok = s.pickRooms(o, day, 1, 2, 3, roomB, false)
	if !ok || th != "B" || lab != "A" {
		t.Fatalf("expected theory=B lab=A, got theory=%s lab=%s ok=%v", th, lab, ok)
	}

	// Only B free for theory, but both free for lab: pick B for both
	roomB["B"] = nil
	roomB["A"] = map[resourceKey]bool{{day: day, period: 1}: true}
	th, lab, ok = s.pickRooms(o, day, 1, 2, 3, roomB, false)
	if !ok || th != "B" || lab != "B" {
		t.Fatalf("expected B/B, got theory=%s lab=%s ok=%v", th, lab, ok)
	}
}
