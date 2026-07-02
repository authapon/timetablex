// Package scheduler implements the scheduling algorithm.
package scheduler

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"

	"timetablex/types"
)

// Scheduler holds the scheduling state.
type Scheduler struct {
	config                *types.Config
	rng                   *rand.Rand
	attempts              int
	verbose               bool
	predefinedAssignments []*types.PredefinedAssignment
}

// NewScheduler creates a new scheduler.
func NewScheduler(config *types.Config, seed int64, attempts int, verbose bool) *Scheduler {
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	return &Scheduler{
		config:                config,
		rng:                   rand.New(rand.NewSource(seed)),
		attempts:              attempts,
		verbose:               verbose,
		predefinedAssignments: nil,
	}
}

// SetPredefinedAssignments sets the pre-defined assignments from config.txt.
// These assignments' resources will be pre-booked so the scheduler avoids conflicts.
func (s *Scheduler) SetPredefinedAssignments(assignments []*types.PredefinedAssignment) {
	s.predefinedAssignments = assignments
}

type resourceKey struct {
	day    types.Day
	period int
}

type innerAssignment struct {
	offering    *types.Offering
	day         types.Day
	theoryStart int
	labStart    int
	theoryRoom  string
	labRoom     string
}

// unit represents a scheduling unit with saturation degree info
type unit struct {
	offering         *types.Offering
	idx              int
	domainSize       int // estimated number of valid positions
	theoryPeriods    int
	labPeriods       int
	totalPeriods     int
}

// Schedule runs the scheduling algorithm and returns a schedule.
func (s *Scheduler) Schedule() (*types.Schedule, error) {
	s.log("Starting scheduling (backtracking construction)...")

	lunchBreak := make(map[types.Day]int)
	if len(s.config.Breaks.Periods) > 0 {
		lp := s.config.Breaks.Periods[0]
		for _, day := range types.AllDays() {
			lunchBreak[day] = lp
		}
	}

	s.log("  Pre-booking %d predefined assignments from config.txt...", len(s.predefinedAssignments))

	// Build units with saturation degree info
	units := make([]unit, len(s.config.Offerings))
	for i, o := range s.config.Offerings {
		total := o.TheoryPeriods + o.LabPeriods
		u := unit{
			offering:      o,
			idx:           i,
			theoryPeriods: o.TheoryPeriods,
			labPeriods:    o.LabPeriods,
			totalPeriods:  total,
			domainSize:    s.estimateDomainSize(o, total, lunchBreak),
		}
		units[i] = u
	}

	// Try different lunch breaks (ordered by priority)
	for _, lpVal := range s.config.Breaks.Periods {
		// Skip this break period if any predefined assignment conflicts with it
		if s.hasPredefinedBreakConflict(lpVal) {
			s.log("  Skipping lunch break at period %d (conflicts with predefined assignment)", lpVal)
			continue
		}

		lunchBreak := make(map[types.Day]int)
		for _, day := range types.AllDays() {
			lunchBreak[day] = lpVal
		}

		s.log("  Trying lunch break at period %d", lpVal)

		maxBacktracks := len(units) * 50 // Limit total backtracks per attempt
		for restart := 0; restart < s.attempts; restart++ {
			if restart%10 == 0 {
				s.log("  Attempt %d (lunch=%d)...", restart+1, lpVal)
			}

			s.rng.Shuffle(len(units), func(i, j int) { units[i], units[j] = units[j], units[i] })
			sort.SliceStable(units, func(i, j int) bool {
				return units[i].domainSize < units[j].domainSize
			})

			assignments, ok := s.backtrackSchedule(units, lunchBreak, maxBacktracks)
			if ok && len(assignments) == len(units) {
				s.log("  Found feasible solution on attempt %d with lunch at period %d", restart+1, lpVal)

				sch := &types.Schedule{
					LunchBreakDay: lunchBreak,
					Config:        s.config,
				}
				for _, a := range assignments {
					sch.Assignments = append(sch.Assignments, &types.Assignment{
						Offering:     a.offering,
						Day:          a.day,
						TheoryStart:  a.theoryStart,
						LabStart:     a.labStart,
						TheoryRoomID: a.theoryRoom,
						LabRoomID:    a.labRoom,
					})
				}

				s.log("Phase 2: Simulated Annealing")
				s.simulatedAnnealing(sch)
				return sch, nil
			}
		}
	}

	return nil, fmt.Errorf("unable to find feasible schedule after %d attempts", s.attempts)
}

// estimateDomainSize counts how many (day, start) positions pass static constraints
// for this offering.
func (s *Scheduler) estimateDomainSize(o *types.Offering, total int, lunchBreak map[types.Day]int) int {
	count := 0
	for _, day := range types.AllDays() {
		if !s.isDayValidForGroups(o.GroupIDs, day) {
			continue
		}
		maxStart := types.MaxPeriodsPerDay - total + 1
		if maxStart < 1 {
			continue
		}
		lp := lunchBreak[day]
		for start := 1; start <= maxStart; start++ {
			if lp > 0 && start <= lp && lp <= start+total-1 {
				continue
			}
			// Check static constraints quickly
			ok := true
			for p := start; p < start+total; p++ {
				isTheory := p < start+o.TheoryPeriods
				for _, gid := range o.GroupIDs {
					if s.isGroupUnavailable(gid, day, p) {
						ok = false
						break
					}
				}
				if !ok {
					break
				}
				if isTheory && o.MainInstructorID != "x" {
					if s.isInstructorUnavailable(o.MainInstructorID, day, p) ||
						s.isInstructorUnavailableMain(o.MainInstructorID, day, p) ||
						s.isInstructorNoLate(o.MainInstructorID, day, start, total) {
						ok = false
						break
					}
				}
				if !isTheory {
					if o.MainInstructorID != "x" {
						if s.isInstructorUnavailable(o.MainInstructorID, day, p) ||
							s.isInstructorUnavailableMain(o.MainInstructorID, day, p) ||
							s.isInstructorNoLate(o.MainInstructorID, day, start, total) {
							ok = false
							break
						}
					}
					for _, coID := range o.CoInstructorIDs {
						if s.isInstructorUnavailable(coID, day, p) {
							ok = false
							break
						}
					}
				}
				if !ok {
					break
				}
			}
			if ok {
				count++
			}
		}
	}
	return count
}

// pos stores a (day, start) pair with assigned rooms
type pos struct {
	day        types.Day
	start      int
	theoryRoom string
	labRoom    string
}

// backtrackSchedule attempts to schedule all units using backtracking search.
// It generates positions dynamically (considering current resource state)
// and backtracks when a unit can't be placed. maxBacktracks limits the total
// number of backtrack operations to bound runtime.
func (s *Scheduler) backtrackSchedule(units []unit, lunchBreak map[types.Day]int, maxBacktracks int) ([]innerAssignment, bool) {
	n := len(units)
	if n == 0 {
		return nil, true
	}

	instB := make(map[string]map[resourceKey]bool)
	grpB := make(map[string]map[resourceKey]bool)
	roomB := make(map[string]map[resourceKey]bool)

	// Pre-book resources from predefined assignments (config.txt)
	for _, pa := range s.predefinedAssignments {
		totalPeriods := pa.TheoryPeriods + pa.LabPeriods
		if totalPeriods == 0 {
			continue
		}
		// Determine the correct start period (use TheoryStart; if 0, use LabStart)
		startPeriod := pa.TheoryStart
		if startPeriod == 0 {
			startPeriod = pa.LabStart
		}
		for p := startPeriod; p < startPeriod+totalPeriods; p++ {
			rk := resourceKey{day: pa.Day, period: p}
			isLab := pa.LabStart > 0 && p >= pa.LabStart
			isTheory := pa.TheoryStart > 0 && p >= pa.TheoryStart && p < pa.TheoryStart+pa.TheoryPeriods

			// Book main instructor
			if pa.MainInstructorID != "x" {
				if instB[pa.MainInstructorID] == nil {
					instB[pa.MainInstructorID] = make(map[resourceKey]bool)
				}
				instB[pa.MainInstructorID][rk] = true
			}

			// Book co-instructors (lab periods only)
			if isLab {
				for _, coID := range pa.CoInstructorIDs {
					if instB[coID] == nil {
						instB[coID] = make(map[resourceKey]bool)
					}
					instB[coID][rk] = true
				}
			}

			// Book groups
			for _, gid := range pa.GroupIDs {
				if grpB[gid] == nil {
					grpB[gid] = make(map[resourceKey]bool)
				}
				grpB[gid][rk] = true
			}

			// Book room (only if not "$" or "x")
			var room string
			if isTheory {
				room = pa.TheoryRoomID
			} else if isLab {
				room = pa.LabRoomID
			}
			if room != "" && room != "x" && room != "$" {
				if roomB[room] == nil {
					roomB[room] = make(map[resourceKey]bool)
				}
				roomB[room][rk] = true
			}
		}
	}

	result := make([]innerAssignment, 0, n)
	totalBacktracks := 0

	// Stack-based iterative backtracking
	type state struct {
		unitIdx   int
		posIndex  int
		positions []pos
	}

	stack := make([]state, 0, n+1)

	// Push initial unit
	curPositions := s.genPositions(units[0], lunchBreak, instB, grpB, roomB)
	stack = append(stack, state{unitIdx: 0, posIndex: 0, positions: curPositions})

	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		u := units[top.unitIdx]

		// Try positions from posIndex onwards
		found := false
		for top.posIndex < len(top.positions) {
			p := top.positions[top.posIndex]
			top.posIndex++

			// Verify room availability now (resources may have changed)
			theoryRoom := p.theoryRoom
			labRoom := p.labRoom

			if u.theoryPeriods > 0 && u.offering.TheoryRoomRaw != "x" && len(u.offering.TheoryRoomIDs) > 0 {
				foundRoom := false
				for _, tr := range u.offering.TheoryRoomIDs {
					avail := true
					for pp := p.start; pp < p.start+u.theoryPeriods; pp++ {
						rk := resourceKey{day: p.day, period: pp}
						if roomB[tr] != nil && roomB[tr][rk] {
							avail = false
							break
						}
						if s.isRoomReservedByInstructorUnavailable(tr, p.day, pp) {
							avail = false
							break
						}
						if s.isRoomReservedByGroupsUnavailable(tr, p.day, pp) {
							avail = false
							break
						}
					}
					if avail {
						theoryRoom = tr
						foundRoom = true
						break
					}
				}
				if !foundRoom {
					continue
				}
			}
			if u.labPeriods > 0 && u.offering.LabRoomRaw != "x" && len(u.offering.LabRoomIDs) > 0 {
				foundRoom := false
				for _, lr := range u.offering.LabRoomIDs {
					avail := true
					for pp := p.start + u.theoryPeriods; pp < p.start+u.totalPeriods; pp++ {
						rk := resourceKey{day: p.day, period: pp}
						if roomB[lr] != nil && roomB[lr][rk] {
							avail = false
							break
						}
						if s.isRoomReservedByInstructorUnavailable(lr, p.day, pp) {
							avail = false
							break
						}
						if s.isRoomReservedByGroupsUnavailable(lr, p.day, pp) {
							avail = false
							break
						}
					}
					if avail {
						labRoom = lr
						foundRoom = true
						break
					}
				}
				if !foundRoom {
					continue
				}
			}

			if !s.checkConstraints(u.offering, p.day, p.start, u.totalPeriods, instB, grpB) {
				continue
			}

			// Valid! Book resources
			a := innerAssignment{
				offering:    u.offering,
				day:         p.day,
				theoryStart: p.start,
				labStart:    p.start + u.theoryPeriods,
				theoryRoom:  theoryRoom,
				labRoom:     labRoom,
			}
			s.bookResources(a, u.offering, u.totalPeriods, instB, grpB, roomB)
			result = append(result, a)

			if top.unitIdx+1 >= n {
				// Brute-force verify all assignments against each other
				if s.verifySchedule(result) {
					return result, true
				}
				// Invalid solution - backtrack and try another combination
				found = false
				break
			}

			nextPositions := s.genPositions(units[top.unitIdx+1], lunchBreak, instB, grpB, roomB)
			stack = append(stack, state{unitIdx: top.unitIdx + 1, posIndex: 0, positions: nextPositions})
			found = true
			break
		}

		if !found {
			totalBacktracks++
			if totalBacktracks > maxBacktracks {
				return nil, false
			}
			// Backtrack: undo this unit's assignment and pop from stack
			if len(result) > 0 {
				last := result[len(result)-1]
				s.unbookResources(last, last.offering,
					last.offering.TheoryPeriods+last.offering.LabPeriods,
					instB, grpB, roomB)
				result = result[:len(result)-1]
			}
			stack = stack[:len(stack)-1]
		}
	}

	return nil, false
}

// genPositions generates valid (day, start) positions for an offering
// considering CURRENT resource state (room booking, double booking).
// Returns positions sorted by heuristic quality.
func (s *Scheduler) genPositions(
	u unit,
	lunchBreak map[types.Day]int,
	instB map[string]map[resourceKey]bool,
	grpB map[string]map[resourceKey]bool,
	roomB map[string]map[resourceKey]bool,
) []pos {
	o := u.offering
	total := u.totalPeriods
	var positions []pos

	for _, day := range types.AllDays() {
		if !s.isDayValidForGroups(o.GroupIDs, day) {
			continue
		}
		maxStart := types.MaxPeriodsPerDay - total + 1
		if maxStart < 1 {
			continue
		}
		lp := lunchBreak[day]
		for start := 1; start <= maxStart; start++ {
			if lp > 0 && start <= lp && lp <= start+total-1 {
				continue
			}

			// Quick static constraint check
			valid := true
			for p := start; p < start+total; p++ {
				isTheory := p < start+u.theoryPeriods
				for _, gid := range o.GroupIDs {
					if s.isGroupUnavailable(gid, day, p) {
						valid = false
						break
					}
				}
				if !valid {
					break
				}
				if isTheory && o.MainInstructorID != "x" {
					if s.isInstructorUnavailable(o.MainInstructorID, day, p) ||
						s.isInstructorUnavailableMain(o.MainInstructorID, day, p) ||
						s.isInstructorNoLate(o.MainInstructorID, day, start, total) {
						valid = false
						break
					}
				}
				if !isTheory {
					if o.MainInstructorID != "x" {
						if s.isInstructorUnavailable(o.MainInstructorID, day, p) ||
							s.isInstructorUnavailableMain(o.MainInstructorID, day, p) ||
							s.isInstructorNoLate(o.MainInstructorID, day, start, total) {
							valid = false
							break
						}
					}
					for _, coID := range o.CoInstructorIDs {
						if s.isInstructorUnavailable(coID, day, p) {
							valid = false
							break
						}
					}
				}
				if !valid {
					break
				}
				// Check double booking vs current resources
				rk := resourceKey{day: day, period: p}
				for _, gid := range o.GroupIDs {
					if grpB[gid] != nil && grpB[gid][rk] {
						valid = false
						break
					}
				}
				if !valid {
					break
				}
				if o.MainInstructorID != "x" {
					if instB[o.MainInstructorID] != nil && instB[o.MainInstructorID][rk] {
						valid = false
						break
					}
				}
				if !isTheory {
					for _, coID := range o.CoInstructorIDs {
						if instB[coID] != nil && instB[coID][rk] {
							valid = false
							break
						}
					}
				}
				if !valid {
					break
				}
			}
			if !valid {
				continue
			}

			// Pick first available rooms
			theoryRoom := "x"
			labRoom := "x"

			if u.theoryPeriods > 0 && o.TheoryRoomRaw != "x" && len(o.TheoryRoomIDs) > 0 {
				for _, tr := range o.TheoryRoomIDs {
					avail := true
					for pp := start; pp < start+u.theoryPeriods; pp++ {
						rk := resourceKey{day: day, period: pp}
						if roomB[tr] != nil && roomB[tr][rk] {
							avail = false
							break
						}
						if s.isRoomReservedByInstructorUnavailable(tr, day, pp) {
							avail = false
							break
						}
						if s.isRoomReservedByGroupsUnavailable(tr, day, pp) {
							avail = false
							break
						}
					}
					if avail {
						theoryRoom = tr
						break
					}
				}
				if theoryRoom == "x" {
					continue // no room
				}
			}

			if u.labPeriods > 0 && o.LabRoomRaw != "x" && len(o.LabRoomIDs) > 0 {
				for _, lr := range o.LabRoomIDs {
					avail := true
					for pp := start + u.theoryPeriods; pp < start+total; pp++ {
						rk := resourceKey{day: day, period: pp}
						if roomB[lr] != nil && roomB[lr][rk] {
							avail = false
							break
						}
						if s.isRoomReservedByInstructorUnavailable(lr, day, pp) {
							avail = false
							break
						}
						if s.isRoomReservedByGroupsUnavailable(lr, day, pp) {
							avail = false
							break
						}
					}
					if avail {
						labRoom = lr
						break
					}
				}
				if labRoom == "x" {
					continue // no room
				}
			}

			positions = append(positions, pos{
				day:        day,
				start:      start,
				theoryRoom: theoryRoom,
				labRoom:    labRoom,
			})
		}
	}

	// Shuffle for randomness
	s.rng.Shuffle(len(positions), func(i, j int) { positions[i], positions[j] = positions[j], positions[i] })

	return positions
}

func (s *Scheduler) checkConstraints(
	o *types.Offering,
	day types.Day,
	start int,
	total int,
	instB map[string]map[resourceKey]bool,
	grpB map[string]map[resourceKey]bool,
) bool {
	end := start + total - 1
	for p := start; p <= end; p++ {
		rk := resourceKey{day: day, period: p}
		isTheory := p < start+o.TheoryPeriods
		for _, gid := range o.GroupIDs {
			if grpB[gid] != nil && grpB[gid][rk] {
				return false
			}
		}
		if o.MainInstructorID != "x" {
			if instB[o.MainInstructorID] != nil && instB[o.MainInstructorID][rk] {
				return false
			}
		}
		if !isTheory {
			for _, coID := range o.CoInstructorIDs {
				if instB[coID] != nil && instB[coID][rk] {
					return false
				}
			}
		}
	}
	return true
}

func (s *Scheduler) bookResources(
	a innerAssignment, o *types.Offering, total int,
	instB, grpB, roomB map[string]map[resourceKey]bool,
) {
	for p := a.theoryStart; p < a.theoryStart+total; p++ {
		rk := resourceKey{day: a.day, period: p}
		isLab := p >= a.labStart
		if o.MainInstructorID != "x" {
			if instB[o.MainInstructorID] == nil {
				instB[o.MainInstructorID] = make(map[resourceKey]bool)
			}
			instB[o.MainInstructorID][rk] = true
		}
		if isLab {
			for _, coID := range o.CoInstructorIDs {
				if instB[coID] == nil {
					instB[coID] = make(map[resourceKey]bool)
				}
				instB[coID][rk] = true
			}
		}
		for _, gid := range o.GroupIDs {
			if grpB[gid] == nil {
				grpB[gid] = make(map[resourceKey]bool)
			}
			grpB[gid][rk] = true
		}
		rm := a.theoryRoom
		if isLab && a.labRoom != "x" {
			rm = a.labRoom
		}
		if rm != "x" {
			if roomB[rm] == nil {
				roomB[rm] = make(map[resourceKey]bool)
			}
			roomB[rm][rk] = true
		}
	}
}

func (s *Scheduler) unbookResources(
	a innerAssignment, o *types.Offering, total int,
	instB, grpB, roomB map[string]map[resourceKey]bool,
) {
	for p := a.theoryStart; p < a.theoryStart+total; p++ {
		rk := resourceKey{day: a.day, period: p}
		isLab := p >= a.labStart
		if o.MainInstructorID != "x" {
			if m, ok := instB[o.MainInstructorID]; ok {
				delete(m, rk)
			}
		}
		if isLab {
			for _, coID := range o.CoInstructorIDs {
				if m, ok := instB[coID]; ok {
					delete(m, rk)
				}
			}
		}
		for _, gid := range o.GroupIDs {
			if m, ok := grpB[gid]; ok {
				delete(m, rk)
			}
		}
		rm := a.theoryRoom
		if isLab && a.labRoom != "x" {
			rm = a.labRoom
		}
		if rm != "x" {
			if m, ok := roomB[rm]; ok {
				delete(m, rk)
			}
		}
	}
}

// verifySchedule checks that the solution satisfies all hard constraints.
func (s *Scheduler) verifySchedule(assignments []innerAssignment) bool {
	// HC-1: No double-booking of instructors
	for i, a := range assignments {
		for j, b := range assignments {
			if i == j || a.day != b.day {
				continue
			}
			aOff := a.offering
			bOff := b.offering
			for p := 1; p <= types.MaxPeriodsPerDay; p++ {
				aCovers := false
				bCovers := false
				if a.theoryStart > 0 && p >= a.theoryStart && p < a.theoryStart+aOff.TheoryPeriods {
					aCovers = true
				}
				if a.labStart > 0 && p >= a.labStart && p < a.labStart+aOff.LabPeriods {
					aCovers = true
				}
				if b.theoryStart > 0 && p >= b.theoryStart && p < b.theoryStart+bOff.TheoryPeriods {
					bCovers = true
				}
				if b.labStart > 0 && p >= b.labStart && p < b.labStart+bOff.LabPeriods {
					bCovers = true
				}
				if !aCovers || !bCovers {
					continue
				}

				aIsLab := a.labStart > 0 && p >= a.labStart
				bIsLab := b.labStart > 0 && p >= b.labStart

				// Check main instructors
				if aOff.MainInstructorID != "x" && bOff.MainInstructorID != "x" &&
					aOff.MainInstructorID == bOff.MainInstructorID {
					return false
				}
				// Check main vs co
				if aOff.MainInstructorID != "x" && bIsLab {
					for _, coID := range bOff.CoInstructorIDs {
						if aOff.MainInstructorID == coID {
							return false
						}
					}
				}
				if bOff.MainInstructorID != "x" && aIsLab {
					for _, coID := range aOff.CoInstructorIDs {
						if bOff.MainInstructorID == coID {
							return false
						}
					}
				}
				// Check co vs co
				if aIsLab && bIsLab {
					for _, coA := range aOff.CoInstructorIDs {
						for _, coB := range bOff.CoInstructorIDs {
							if coA == coB {
								return false
							}
						}
					}
				}
			}
		}
	}

	// HC-2: No double-booking of groups
	for i, a := range assignments {
		for j, b := range assignments {
			if i == j || a.day != b.day {
				continue
			}
			aOff := a.offering
			bOff := b.offering
			for p := 1; p <= types.MaxPeriodsPerDay; p++ {
				aCovers := false
				bCovers := false
				if a.theoryStart > 0 && p >= a.theoryStart && p < a.theoryStart+aOff.TheoryPeriods {
					aCovers = true
				}
				if a.labStart > 0 && p >= a.labStart && p < a.labStart+aOff.LabPeriods {
					aCovers = true
				}
				if b.theoryStart > 0 && p >= b.theoryStart && p < b.theoryStart+bOff.TheoryPeriods {
					bCovers = true
				}
				if b.labStart > 0 && p >= b.labStart && p < b.labStart+bOff.LabPeriods {
					bCovers = true
				}
				if !aCovers || !bCovers {
					continue
				}
				for _, gidA := range aOff.GroupIDs {
					for _, gidB := range bOff.GroupIDs {
						if gidA == gidB {
							return false
						}
					}
				}
			}
		}
	}

	return true
}

// ==========================================
// Constraint checking functions
// ==========================================

func (s *Scheduler) isDayValidForGroups(groupIDs []string, day types.Day) bool {
	for _, gid := range groupIDs {
		if grp, ok := s.config.Groups[gid]; ok {
			if grp.TermType == types.Normal && (day == types.Saturday || day == types.Sunday) {
				return false
			}
			if grp.TermType == types.Special && (day != types.Saturday && day != types.Sunday) {
				return false
			}
		}
	}
	return true
}

func (s *Scheduler) isGroupUnavailable(groupID string, day types.Day, period int) bool {
	for _, gu := range s.config.GroupsUnavailable {
		if gu.GroupID == groupID && gu.Day == day &&
			period >= gu.StartPeriod && period <= gu.EndPeriod {
			return true
		}
	}
	return false
}

func (s *Scheduler) isRoomReservedByGroupsUnavailable(roomID string, day types.Day, period int) bool {
	for _, gu := range s.config.GroupsUnavailable {
		if gu.RoomID == "" || gu.RoomID == "none" {
			continue
		}
		if gu.RoomID == roomID && gu.Day == day && period >= gu.StartPeriod && period <= gu.EndPeriod {
			return true
		}
	}
	return false
}

func (s *Scheduler) isRoomReservedByInstructorUnavailable(roomID string, day types.Day, period int) bool {
	for _, iu := range s.config.InstructorUnavailable {
		if iu.RoomID == "" || iu.RoomID == "none" {
			continue
		}
		if iu.RoomID == roomID && iu.Day == day && period >= iu.StartPeriod && period <= iu.EndPeriod {
			return true
		}
	}
	return false
}

func (s *Scheduler) isInstructorUnavailable(instructorID string, day types.Day, period int) bool {
	for _, iu := range s.config.InstructorUnavailable {
		if iu.InstructorID == instructorID && iu.Day == day &&
			period >= iu.StartPeriod && period <= iu.EndPeriod {
			return true
		}
	}
	return false
}

func (s *Scheduler) isInstructorUnavailableMain(instructorID string, day types.Day, period int) bool {
	for _, iu := range s.config.InstructorUnavailableMain {
		if iu.InstructorID == instructorID && iu.Day == day &&
			period >= iu.StartPeriod && period <= iu.EndPeriod {
			return true
		}
	}
	return false
}

func (s *Scheduler) isInstructorNoLate(instructorID string, day types.Day, startPeriod int, totalPeriods int) bool {
	for _, inl := range s.config.InstructorNoLate {
		if inl.InstructorID == instructorID && inl.Day == day &&
			startPeriod+totalPeriods-1 >= inl.PeriodThreshold {
			return true
		}
	}
	return false
}

// ==========================================
// Simulated Annealing (Phase 2)
// ==========================================

func (s *Scheduler) simulatedAnnealing(schedule *types.Schedule) {
	t := 100.0
	tMin := 0.1
	alpha := 0.98
	maxIter := 2000
	stagnationLimit := 500
	stagnation := 0

	bestCost := s.calculateCost(schedule)
	bestAssign := copyAssignments(schedule.Assignments)
	s.log("  Initial cost: %.2f", bestCost)

	for iter := 0; iter < maxIter && t > tMin; iter++ {
		newAssign, ok := s.randomShift(schedule)
		if !ok {
			stagnation++
			if stagnation >= stagnationLimit {
				break
			}
			continue
		}

		oldAssign := schedule.Assignments
		schedule.Assignments = newAssign
		newCost := s.calculateCost(schedule)

		delta := newCost - bestCost

		if delta < 0 || s.rng.Float64() < math.Exp(-delta/t) {
			if newCost < bestCost {
				bestCost = newCost
				bestAssign = copyAssignments(newAssign)
				stagnation = 0
			}
		} else {
			schedule.Assignments = oldAssign
			stagnation++
		}

		t *= alpha
	}

	schedule.Assignments = bestAssign
	s.log("  Final cost: %.2f", bestCost)
}

func copyAssignments(a []*types.Assignment) []*types.Assignment {
	r := make([]*types.Assignment, len(a))
	for i, v := range a {
		cp := *v
		r[i] = &cp
	}
	return r
}

func (s *Scheduler) randomShift(schedule *types.Schedule) ([]*types.Assignment, bool) {
	ass := schedule.Assignments
	if len(ass) == 0 {
		return nil, false
	}

	idx := s.rng.Intn(len(ass))
	a := ass[idx]
	o := a.Offering
	totalPeriods := o.TheoryPeriods + o.LabPeriods

	rest := make([]*types.Assignment, 0, len(ass)-1)
	for i, v := range ass {
		if i != idx {
			rest = append(rest, v)
		}
	}

	instB := make(map[string]map[resourceKey]bool)
	grpB := make(map[string]map[resourceKey]bool)
	roomB := make(map[string]map[resourceKey]bool)

	// Pre-book predefined assignments' resources so SA doesn't create conflicts
	for _, pa := range s.predefinedAssignments {
		totalPeriods := pa.TheoryPeriods + pa.LabPeriods
		if totalPeriods == 0 {
			continue
		}
		startPeriod := pa.TheoryStart
		if startPeriod == 0 {
			startPeriod = pa.LabStart
		}
		for p := startPeriod; p < startPeriod+totalPeriods; p++ {
			rk := resourceKey{day: pa.Day, period: p}
			isLab := pa.LabStart > 0 && p >= pa.LabStart
			isTheory := pa.TheoryStart > 0 && p >= pa.TheoryStart && p < pa.TheoryStart+pa.TheoryPeriods

			if pa.MainInstructorID != "x" {
				if instB[pa.MainInstructorID] == nil {
					instB[pa.MainInstructorID] = make(map[resourceKey]bool)
				}
				instB[pa.MainInstructorID][rk] = true
			}
			if isLab {
				for _, coID := range pa.CoInstructorIDs {
					if instB[coID] == nil {
						instB[coID] = make(map[resourceKey]bool)
					}
					instB[coID][rk] = true
				}
			}
			for _, gid := range pa.GroupIDs {
				if grpB[gid] == nil {
					grpB[gid] = make(map[resourceKey]bool)
				}
				grpB[gid][rk] = true
			}
			var rm string
			if isTheory {
				rm = pa.TheoryRoomID
			} else if isLab {
				rm = pa.LabRoomID
			}
			if rm != "" && rm != "x" && rm != "$" {
				if roomB[rm] == nil {
					roomB[rm] = make(map[resourceKey]bool)
				}
				roomB[rm][rk] = true
			}
		}
	}

	for _, ea := range rest {
		for p := 1; p <= types.MaxPeriodsPerDay; p++ {
			if !ea.ContainsPeriod(ea.Day, p) {
				continue
			}
			rk := resourceKey{day: ea.Day, period: p}
			isLab := ea.LabStart > 0 && p >= ea.LabStart

			if ea.Offering.MainInstructorID != "x" {
				if instB[ea.Offering.MainInstructorID] == nil {
					instB[ea.Offering.MainInstructorID] = make(map[resourceKey]bool)
				}
				instB[ea.Offering.MainInstructorID][rk] = true
			}
			if isLab {
				for _, coID := range ea.Offering.CoInstructorIDs {
					if instB[coID] == nil {
						instB[coID] = make(map[resourceKey]bool)
					}
					instB[coID][rk] = true
				}
			}
			for _, gid := range ea.Offering.GroupIDs {
				if grpB[gid] == nil {
					grpB[gid] = make(map[resourceKey]bool)
				}
				grpB[gid][rk] = true
			}
			rm := ""
			if ea.TheoryStart > 0 && p >= ea.TheoryStart && p < ea.TheoryStart+ea.Offering.TheoryPeriods {
				rm = ea.TheoryRoomID
			} else if ea.LabStart > 0 && p >= ea.LabStart {
				rm = ea.LabRoomID
			}
			if rm != "" && rm != "x" {
				if roomB[rm] == nil {
					roomB[rm] = make(map[resourceKey]bool)
				}
				roomB[rm][rk] = true
			}
		}
	}

	days := types.AllDays()
	s.rng.Shuffle(len(days), func(i, j int) { days[i], days[j] = days[j], days[i] })

	for _, day := range days {
		if !s.isDayValidForGroups(o.GroupIDs, day) {
			continue
		}

		maxStart := types.MaxPeriodsPerDay - totalPeriods + 1
		if maxStart < 1 {
			continue
		}

		starts := make([]int, maxStart)
		for i := 0; i < maxStart; i++ {
			starts[i] = i + 1
		}
		s.rng.Shuffle(len(starts), func(i, j int) { starts[i], starts[j] = starts[j], starts[i] })

		for _, start := range starts {
			end := start + totalPeriods - 1

			if lp, ok := schedule.LunchBreakDay[day]; ok {
				if start <= lp && lp <= end {
					continue
				}
			}

			theoryRoom := "x"
			labRoom := "x"

			if o.TheoryPeriods > 0 && o.TheoryRoomRaw != "x" && len(o.TheoryRoomIDs) > 0 {
				rooms := make([]string, len(o.TheoryRoomIDs))
				copy(rooms, o.TheoryRoomIDs)
				s.rng.Shuffle(len(rooms), func(i, j int) { rooms[i], rooms[j] = rooms[j], rooms[i] })
				for _, tr := range rooms {
					avail := true
					for p := start; p < start+o.TheoryPeriods; p++ {
						rk := resourceKey{day: day, period: p}
						if roomB[tr] != nil && roomB[tr][rk] {
							avail = false
							break
						}
						if s.isRoomReservedByInstructorUnavailable(tr, day, p) {
							avail = false
							break
						}
						if s.isRoomReservedByGroupsUnavailable(tr, day, p) {
							avail = false
							break
						}
					}
					if avail {
						theoryRoom = tr
						break
					}
				}
			}

			if o.LabPeriods > 0 && o.LabRoomRaw != "x" && len(o.LabRoomIDs) > 0 {
				rooms := make([]string, len(o.LabRoomIDs))
				copy(rooms, o.LabRoomIDs)
				s.rng.Shuffle(len(rooms), func(i, j int) { rooms[i], rooms[j] = rooms[j], rooms[i] })
				for _, lr := range rooms {
					avail := true
					for p := start + o.TheoryPeriods; p <= end; p++ {
						rk := resourceKey{day: day, period: p}
						if roomB[lr] != nil && roomB[lr][rk] {
							avail = false
							break
						}
						if s.isRoomReservedByInstructorUnavailable(lr, day, p) {
							avail = false
							break
						}
						if s.isRoomReservedByGroupsUnavailable(lr, day, p) {
							avail = false
							break
						}
					}
					if avail {
						labRoom = lr
						break
					}
				}
			}

			if (o.TheoryPeriods > 0 && o.TheoryRoomRaw != "x" && theoryRoom == "x") ||
				(o.LabPeriods > 0 && o.LabRoomRaw != "x" && labRoom == "x") {
				continue
			}

			allValid := true
			for p := start; p <= end; p++ {
				rk := resourceKey{day: day, period: p}
				isTheory := p < start+o.TheoryPeriods

				for _, gid := range o.GroupIDs {
					if s.isGroupUnavailable(gid, day, p) {
						allValid = false
						break
					}
					if grpB[gid] != nil && grpB[gid][rk] {
						allValid = false
						break
					}
				}
				if !allValid {
					break
				}

				if isTheory && o.MainInstructorID != "x" {
					if s.isInstructorUnavailable(o.MainInstructorID, day, p) {
						allValid = false
						break
					}
					if s.isInstructorUnavailableMain(o.MainInstructorID, day, p) {
						allValid = false
						break
					}
					if s.isInstructorNoLate(o.MainInstructorID, day, start, totalPeriods) {
						allValid = false
						break
					}
					if instB[o.MainInstructorID] != nil && instB[o.MainInstructorID][rk] {
						allValid = false
						break
					}
				}
				if !isTheory {
					if o.MainInstructorID != "x" {
						if s.isInstructorUnavailable(o.MainInstructorID, day, p) {
							allValid = false
							break
						}
						if s.isInstructorUnavailableMain(o.MainInstructorID, day, p) {
							allValid = false
							break
						}
						if s.isInstructorNoLate(o.MainInstructorID, day, start, totalPeriods) {
							allValid = false
							break
						}
						if instB[o.MainInstructorID] != nil && instB[o.MainInstructorID][rk] {
							allValid = false
							break
						}
					}
					for _, coID := range o.CoInstructorIDs {
						if s.isInstructorUnavailable(coID, day, p) {
							allValid = false
							break
						}
						if instB[coID] != nil && instB[coID][rk] {
							allValid = false
							break
						}
					}
				}
				if !allValid {
					break
				}
			}

			if allValid {
				newAss := &types.Assignment{
					Offering:     o,
					Day:          day,
					TheoryStart:  start,
					LabStart:     start + o.TheoryPeriods,
					TheoryRoomID: theoryRoom,
					LabRoomID:    labRoom,
				}
				return append(rest, newAss), true
			}
		}
	}

	return nil, false
}

// ==========================================
// Cost Function
// ==========================================

func (s *Scheduler) calculateCost(sch *types.Schedule) float64 {
	cost := 0.0

	for _, a := range sch.Assignments {
		o := a.Offering

		if o.TheoryPeriods > 0 && o.LabPeriods > 0 {
			start := a.TheoryStart
			if start != 1 && start != 6 {
				d1 := math.Abs(float64(start - 1))
				d6 := math.Abs(float64(start - 6))
				cost += 100.0 * math.Min(d1, d6)
			}
		}

		if a.TheoryStart > 0 {
			cost += float64(a.TheoryStart) * 5.0
		} else if a.LabStart > 0 {
			cost += float64(a.LabStart) * 5.0
		}

		if a.TheoryStart > 0 {
			for p := a.TheoryStart; p < a.TheoryStart+o.TheoryPeriods; p++ {
				if p >= 9 {
					cost += 50.0
				}
			}
		}
		if a.LabStart > 0 {
			for p := a.LabStart; p < a.LabStart+o.LabPeriods; p++ {
				if p >= 9 {
					cost += 50.0
				}
			}
		}

		if o.TheoryPeriods > 0 && o.LabPeriods > 0 {
			if a.TheoryRoomID != "x" && a.LabRoomID != "x" && a.TheoryRoomID != a.LabRoomID {
				cost += 20.0
			}
		}
	}

	roomCount := make(map[string]int)
	for _, a := range sch.Assignments {
		if a.TheoryRoomID != "" && a.TheoryRoomID != "x" {
			roomCount[a.TheoryRoomID] += a.Offering.TheoryPeriods
		}
		if a.LabRoomID != "" && a.LabRoomID != "x" {
			roomCount[a.LabRoomID] += a.Offering.LabPeriods
		}
	}
	if len(roomCount) > 0 {
		maxC, minC := 0, math.MaxInt32
		for _, c := range roomCount {
			if c > maxC {
				maxC = c
			}
			if c < minC {
				minC = c
			}
		}
		if minC == math.MaxInt32 {
			minC = 0
		}
		cost += float64(maxC-minC) * 10.0
	}

	return cost
}

// ResolveDollarRooms resolves "$" rooms in predefined assignments after scheduling is complete.
// For each predefined assignment with a "$" room field, it finds an available room from config.Rooms.
// It considers all room bookings from scheduled assignments and other predefined (non-$) assignments.
func (s *Scheduler) ResolveDollarRooms(predefined []*types.PredefinedAssignment, schedule *types.Schedule) []error {
	var errors []error

	// Build room booking map from all sources
	roomB := make(map[string]map[resourceKey]bool)

	// 1. Pre-book rooms from scheduled assignments
	for _, a := range schedule.Assignments {
		for p := 1; p <= types.MaxPeriodsPerDay; p++ {
			if !a.ContainsPeriod(a.Day, p) {
				continue
			}
			rk := resourceKey{day: a.Day, period: p}
			if a.IsTheoryPeriod(p) {
				rm := a.TheoryRoomID
				if rm != "" && rm != "x" {
					if roomB[rm] == nil {
						roomB[rm] = make(map[resourceKey]bool)
					}
					roomB[rm][rk] = true
				}
			} else if a.IsLabPeriod(p) {
				rm := a.LabRoomID
				if rm != "" && rm != "x" {
					if roomB[rm] == nil {
						roomB[rm] = make(map[resourceKey]bool)
					}
					roomB[rm][rk] = true
				}
			}
		}
	}

	// 2. Pre-book rooms from predefined assignments (non-$ rooms)
	for _, pa := range predefined {
		totalPeriods := pa.TheoryPeriods + pa.LabPeriods
		if totalPeriods == 0 {
			continue
		}
		// Determine the correct start period
		startPeriod := pa.TheoryStart
		if startPeriod == 0 {
			startPeriod = pa.LabStart
		}
		for p := startPeriod; p < startPeriod+totalPeriods; p++ {
			rk := resourceKey{day: pa.Day, period: p}
			isLab := pa.LabStart > 0 && p >= pa.LabStart
			isTheory := pa.TheoryStart > 0 && p >= pa.TheoryStart && p < pa.TheoryStart+pa.TheoryPeriods

			var rm string
			if isTheory {
				rm = pa.TheoryRoomID
			} else if isLab {
				rm = pa.LabRoomID
			}
			if rm != "" && rm != "x" && rm != "$" {
				if roomB[rm] == nil {
					roomB[rm] = make(map[resourceKey]bool)
				}
				roomB[rm][rk] = true
			}
		}
	}

	// 3. Include rooms reserved by groups_unavailable
	for _, gu := range s.config.GroupsUnavailable {
		if gu.RoomID == "" || gu.RoomID == "none" {
			continue
		}
		for p := gu.StartPeriod; p <= gu.EndPeriod; p++ {
			rk := resourceKey{day: gu.Day, period: p}
			if roomB[gu.RoomID] == nil {
				roomB[gu.RoomID] = make(map[resourceKey]bool)
			}
			roomB[gu.RoomID][rk] = true
		}
	}

	// 4. Include rooms reserved by instructor_unavailable
	for _, iu := range s.config.InstructorUnavailable {
		if iu.RoomID == "" || iu.RoomID == "none" {
			continue
		}
		for p := iu.StartPeriod; p <= iu.EndPeriod; p++ {
			rk := resourceKey{day: iu.Day, period: p}
			if roomB[iu.RoomID] == nil {
				roomB[iu.RoomID] = make(map[resourceKey]bool)
			}
			roomB[iu.RoomID][rk] = true
		}
	}

	// 5. Resolve "$" rooms for each predefined assignment
	for _, pa := range predefined {
		// Initialize resolved IDs to current values
		pa.ResolvedTheoryRoomID = pa.TheoryRoomID
		pa.ResolvedLabRoomID = pa.LabRoomID

		// Resolve theory room if $
		if pa.TheoryRoomID == "$" && pa.TheoryPeriods > 0 {
			roomID := s.findAvailableRoom(pa.Day, pa.TheoryStart, pa.TheoryStart+pa.TheoryPeriods-1, roomB)
			if roomID == "" {
				errors = append(errors, fmt.Errorf("cannot find available room for theory block of '%s' on %s periods %d-%d",
					pa.CourseID, pa.Day, pa.TheoryStart, pa.TheoryStart+pa.TheoryPeriods-1))
			} else {
				pa.ResolvedTheoryRoomID = roomID
				// Book the resolved room
				for p := pa.TheoryStart; p < pa.TheoryStart+pa.TheoryPeriods; p++ {
					rk := resourceKey{day: pa.Day, period: p}
					if roomB[roomID] == nil {
						roomB[roomID] = make(map[resourceKey]bool)
					}
					roomB[roomID][rk] = true
				}
				if s.verbose {
					fmt.Printf("  Resolved $ theory room for '%s' on %s periods %d-%d -> %s\n",
						pa.CourseID, pa.Day, pa.TheoryStart, pa.TheoryStart+pa.TheoryPeriods-1, roomID)
				}
			}
		}

		// Resolve lab room if $
		if pa.LabRoomID == "$" && pa.LabPeriods > 0 {
			roomID := s.findAvailableRoom(pa.Day, pa.LabStart, pa.LabStart+pa.LabPeriods-1, roomB)
			if roomID == "" {
				errors = append(errors, fmt.Errorf("cannot find available room for lab block of '%s' on %s periods %d-%d",
					pa.CourseID, pa.Day, pa.LabStart, pa.LabStart+pa.LabPeriods-1))
			} else {
				pa.ResolvedLabRoomID = roomID
				// Book the resolved room
				for p := pa.LabStart; p < pa.LabStart+pa.LabPeriods; p++ {
					rk := resourceKey{day: pa.Day, period: p}
					if roomB[roomID] == nil {
						roomB[roomID] = make(map[resourceKey]bool)
					}
					roomB[roomID][rk] = true
				}
				if s.verbose {
					fmt.Printf("  Resolved $ lab room for '%s' on %s periods %d-%d -> %s\n",
						pa.CourseID, pa.Day, pa.LabStart, pa.LabStart+pa.LabPeriods-1, roomID)
				}
			}
		}
	}

	return errors
}

// findAvailableRoom finds an available room from config.Rooms for the given day and period range.
func (s *Scheduler) findAvailableRoom(day types.Day, startPeriod, endPeriod int, roomB map[string]map[resourceKey]bool) string {
	// Iterate through all rooms in config and find the first available one
	for _, rm := range s.config.Rooms {
		available := true
		for p := startPeriod; p <= endPeriod; p++ {
			rk := resourceKey{day: day, period: p}
			if roomB[rm.ID] != nil && roomB[rm.ID][rk] {
				available = false
				break
			}
		}
		if available {
			return rm.ID
		}
	}
	return ""
}

// hasPredefinedBreakConflict checks if any predefined assignment covers the given break period.
// This prevents HC-12 violations by skipping break periods that conflict with predefined assignments.
func (s *Scheduler) hasPredefinedBreakConflict(breakPeriod int) bool {
	for _, pa := range s.predefinedAssignments {
		if IsInPredefinedPeriod(pa, breakPeriod) {
			return true
		}
	}
	return false
}

// IsInPredefinedPeriod checks if the given period is covered by the predefined assignment.
func IsInPredefinedPeriod(pa *types.PredefinedAssignment, period int) bool {
	if pa.TheoryStart > 0 && period >= pa.TheoryStart && period < pa.TheoryStart+pa.TheoryPeriods {
		return true
	}
	if pa.LabStart > 0 && period >= pa.LabStart && period < pa.LabStart+pa.LabPeriods {
		return true
	}
	return false
}

func (s *Scheduler) log(format string, args ...interface{}) {
	if s.verbose {
		fmt.Printf(format+"\n", args...)
	}
}
