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
	// periodLimit is the last period new placements may use; it is raised step by step
	// from types.PreferredMaxPeriod up to types.MaxPeriodsPerDay only when needed.
	periodLimit int
	// tierAttempts is the number of construction attempts for the current period limit.
	tierAttempts int
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
		periodLimit:           types.MaxPeriodsPerDay,
		tierAttempts:          attempts,
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

// maxTierAttempts bounds the construction attempts for the period limits below
// types.MaxPeriodsPerDay: a limit that is too tight is usually hopeless, so fail fast.
const maxTierAttempts = 10

// Schedule runs the scheduling algorithm and returns a schedule.
// It first tries to keep every class within the preferred last period and only
// extends the limit one period at a time (up to types.MaxPeriodsPerDay) when no
// schedule fits, so nobody is scheduled later than necessary.
func (s *Scheduler) Schedule() (*types.Schedule, error) {
	defer func() { s.periodLimit = types.MaxPeriodsPerDay }()

	var err error = fmt.Errorf("unable to find feasible schedule after %d attempts", s.attempts)
	for limit := types.PreferredMaxPeriod; limit <= types.MaxPeriodsPerDay; limit++ {
		last := limit == types.MaxPeriodsPerDay
		if !last && !s.limitHasCapacity(limit) {
			s.log("Periods 1-%d cannot hold the load of some group, skipping", limit)
			continue
		}
		s.periodLimit = limit
		s.tierAttempts = s.attempts
		if !last && s.tierAttempts > maxTierAttempts {
			s.tierAttempts = maxTierAttempts
		}
		s.log("Trying to schedule within periods 1-%d...", limit)
		var sch *types.Schedule
		sch, err = s.scheduleWithinLimit()
		if err == nil {
			return sch, nil
		}
	}
	return nil, err
}

// limitHasCapacity is a quick necessary condition for a period limit: every group's weekly
// load must fit into its available periods (minus unavailable periods and one lunch break
// per day) within periods 1..limit.
func (s *Scheduler) limitHasCapacity(limit int) bool {
	load := make(map[string]int)
	for _, o := range s.config.Offerings {
		for _, gid := range o.GroupIDs {
			load[gid] += o.TheoryPeriods + o.LabPeriods
		}
	}
	for _, pa := range s.predefinedAssignments {
		for _, gid := range pa.GroupIDs {
			load[gid] += pa.TheoryPeriods + pa.LabPeriods
		}
	}

	hasBreak := s.config.Breaks != nil && len(s.config.Breaks.Periods) > 0
	for gid, n := range load {
		capacity := 0
		for _, day := range types.AllDays() {
			if !s.isDayValidForGroups([]string{gid}, day) {
				continue
			}
			free := 0
			for p := 1; p <= limit; p++ {
				if !s.isGroupUnavailable(gid, day, p) {
					free++
				}
			}
			if hasBreak && free > 0 && !s.hasFullDayGroupUnavailable(gid, day) {
				free-- // lunch break
			}
			capacity += free
		}
		if n > capacity {
			return false
		}
	}
	return true
}

// scheduleWithinLimit schedules using the current s.periodLimit.
func (s *Scheduler) scheduleWithinLimit() (*types.Schedule, error) {
	s.log("Starting scheduling (backtracking construction)...")

	// Phase 1: Try with the optimal per-entity break map (highest priority per entity per day)
	sch, err := s.trySchedule()
	if err == nil {
		return sch, nil
	}

	// Phase 2: Fallback - ใช้ break ที่มี priority ต่ำสุดสำหรับทุก entity (ยืดหยุ่นที่สุด)
	s.log("  Optimal breaks not feasible, trying fallback with lowest-priority break...")

	if len(s.config.Breaks.Periods) > 0 {
		// Build per-entity fallback breaks with lowest priority period,
		// but skip periods that conflict with instructor/group unavailable
		fallbackInstBreak := make(map[string]map[types.Day]int)
		fallbackGroupBreak := make(map[string]map[types.Day]int)
		fallbackInstPriority := make(map[string]map[types.Day]int)
		fallbackGroupPriority := make(map[string]map[types.Day]int)

		for instID := range s.config.Instructors {
			dayBreaks := make(map[types.Day]int)
			dayPriorities := make(map[types.Day]int)
			for _, day := range types.AllDays() {
				// If full-day unavailable, skip break
				if s.hasFullDayInstructorUnavailable(instID, day) {
					dayBreaks[day] = 0
					continue
				}
				// Pick a break period that doesn't conflict with instructor_unavailable
				found := false
				for i := len(s.config.Breaks.Periods) - 1; i >= 0; i-- {
					bp := s.config.Breaks.Periods[i]
					if !s.instructorHasPredefinedAt(instID, day, bp) &&
						!s.isInstructorUnavailable(instID, day, bp) {
						dayBreaks[day] = bp
						dayPriorities[day] = i + 1
						found = true
						break
					}
				}
				if !found {
					dayBreaks[day] = 0
				}
			}
			fallbackInstBreak[instID] = dayBreaks
			fallbackInstPriority[instID] = dayPriorities
		}
		for groupID := range s.config.Groups {
			dayBreaks := make(map[types.Day]int)
			dayPriorities := make(map[types.Day]int)
			for _, day := range types.AllDays() {
				// If full-day unavailable, skip break
				if s.hasFullDayGroupUnavailable(groupID, day) {
					dayBreaks[day] = 0
					continue
				}
				// Pick a break period that doesn't conflict with groups_unavailable
				found := false
				for i := len(s.config.Breaks.Periods) - 1; i >= 0; i-- {
					bp := s.config.Breaks.Periods[i]
					if !s.groupHasPredefinedAt(groupID, day, bp) &&
						!s.isGroupUnavailable(groupID, day, bp) {
						dayBreaks[day] = bp
						dayPriorities[day] = i + 1
						found = true
						break
					}
				}
				if !found {
					dayBreaks[day] = 0
				}
			}
			fallbackGroupBreak[groupID] = dayBreaks
			fallbackGroupPriority[groupID] = dayPriorities
		}

		s.log("  Fallback lunch break: built per-entity breaks avoiding unavailable periods")

		units := make([]unit, len(s.config.Offerings))
		for i, o := range s.config.Offerings {
			total := o.TheoryPeriods + o.LabPeriods
			u := unit{
				offering:      o,
				idx:           i,
				theoryPeriods: o.TheoryPeriods,
				labPeriods:    o.LabPeriods,
				totalPeriods:  total,
				domainSize:    s.estimateDomainSize(o, total, fallbackInstBreak, fallbackGroupBreak),
			}
			units[i] = u
		}

		maxBacktracks := len(units) * 50
		var best *types.Schedule
		bestCost := math.Inf(1)
		found := 0
		for restart := 0; restart < s.tierAttempts && found < polishRuns; restart++ {
			if restart%10 == 0 {
				s.log("  Fallback attempt %d...", restart+1)
			}

			s.rng.Shuffle(len(units), func(i, j int) { units[i], units[j] = units[j], units[i] })
			sort.SliceStable(units, func(i, j int) bool {
				return units[i].domainSize < units[j].domainSize
			})

			assignments, ok := s.backtrackSchedule(units, fallbackInstBreak, fallbackGroupBreak, maxBacktracks)
			if ok && len(assignments) == len(units) {
				s.log("  Found feasible solution on fallback attempt %d", restart+1)
				found++
				sch := s.buildAndPolish(assignments, fallbackInstBreak, fallbackGroupBreak,
					fallbackInstPriority, fallbackGroupPriority)
				if c := s.calculateCost(sch); c < bestCost {
					best, bestCost = sch, c
				}
			}
		}
		if best != nil {
			s.log("  Best of %d solutions: cost %.2f", found, bestCost)
			return best, nil
		}
	}

	return nil, fmt.Errorf("unable to find feasible schedule after %d attempts", s.attempts)
}

// polishRuns is how many feasible constructions are improved and compared; the cheapest wins.
const polishRuns = 8

// copyDayMap deep-copies a per-entity, per-day map.
func copyDayMap(m map[string]map[types.Day]int) map[string]map[types.Day]int {
	c := make(map[string]map[types.Day]int, len(m))
	for k, v := range m {
		inner := make(map[types.Day]int, len(v))
		for d, n := range v {
			inner[d] = n
		}
		c[k] = inner
	}
	return c
}

// buildAndPolish turns constructed assignments into a schedule and improves it with
// simulated annealing and the per-entity break upgrade. The break maps are copied because
// the upgrade modifies them and the same maps are reused for the next construction.
func (s *Scheduler) buildAndPolish(assignments []innerAssignment,
	instBreak, groupBreak, instPriority, groupPriority map[string]map[types.Day]int) *types.Schedule {
	sch := &types.Schedule{
		InstructorLunchBreak:    copyDayMap(instBreak),
		GroupLunchBreak:         copyDayMap(groupBreak),
		InstructorBreakPriority: copyDayMap(instPriority),
		GroupBreakPriority:      copyDayMap(groupPriority),
		Config:                  s.config,
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

	// Phase 3: try to upgrade each entity's break period to a higher-priority one
	s.log("Phase 3: Attempting per-entity break upgrade to higher-priority periods...")
	if s.tryUpgradeEntityBreaks(sch) {
		s.log("  Successfully upgraded at least one entity's break to higher-priority period!")
	}
	return sch
}

// trySchedule attempts to schedule with the optimal per-entity break map.
func (s *Scheduler) trySchedule() (*types.Schedule, error) {
	// Build per-entity lunch break map: for each entity independently pick the best
	// available break period (highest priority that doesn't conflict with predefined)
	instBreak, groupBreak := s.buildEntityBreaks()
	instPriority, groupPriority := s.buildEntityBreakPriorities(instBreak, groupBreak)
	s.logEntityBreaks(instBreak, groupBreak)

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
			domainSize:    s.estimateDomainSize(o, total, instBreak, groupBreak),
		}
		units[i] = u
	}

	maxBacktracks := len(units) * 50 // Limit total backtracks per attempt
	var best *types.Schedule
	bestCost := math.Inf(1)
	found := 0
	for restart := 0; restart < s.tierAttempts && found < polishRuns; restart++ {
		if restart%10 == 0 {
			s.log("  Attempt %d...", restart+1)
		}

		s.rng.Shuffle(len(units), func(i, j int) { units[i], units[j] = units[j], units[i] })
		sort.SliceStable(units, func(i, j int) bool {
			return units[i].domainSize < units[j].domainSize
		})

		assignments, ok := s.backtrackSchedule(units, instBreak, groupBreak, maxBacktracks)
		if ok && len(assignments) == len(units) {
			s.log("  Found feasible solution on attempt %d", restart+1)
			found++
			sch := s.buildAndPolish(assignments, instBreak, groupBreak, instPriority, groupPriority)
			if c := s.calculateCost(sch); c < bestCost {
				best, bestCost = sch, c
			}
		}
	}
	if best != nil {
		s.log("  Best of %d solutions: cost %.2f", found, bestCost)
		return best, nil
	}

	return nil, fmt.Errorf("unable to find feasible schedule with optimal breaks")
}

// buildEntityBreaks builds per-entity lunch break maps by picking the best available
// break period (highest priority) for each entity independently, considering predefined
// assignment conflicts and instructor/group unavailable periods on that specific day.
func (s *Scheduler) buildEntityBreaks() (map[string]map[types.Day]int, map[string]map[types.Day]int) {
	instBreak := make(map[string]map[types.Day]int)
	groupBreak := make(map[string]map[types.Day]int)

	if len(s.config.Breaks.Periods) == 0 {
		return instBreak, groupBreak
	}

	// Build for each instructor
	for instID := range s.config.Instructors {
		dayBreaks := make(map[types.Day]int)
		for _, day := range types.AllDays() {
			// If the instructor has full-day (1-15) unavailable, skip break entirely
			if s.hasFullDayInstructorUnavailable(instID, day) {
				dayBreaks[day] = 0
				continue
			}
			found := false
			for _, bp := range s.config.Breaks.Periods {
				if !s.instructorHasPredefinedAt(instID, day, bp) &&
					!s.isInstructorUnavailable(instID, day, bp) {
					dayBreaks[day] = bp
					found = true
					break
				}
			}
			if !found {
				dayBreaks[day] = 0
			}
		}
		instBreak[instID] = dayBreaks
	}

	// Build for each group
	for groupID := range s.config.Groups {
		dayBreaks := make(map[types.Day]int)
		for _, day := range types.AllDays() {
			// If the group has full-day (1-15) unavailable, skip break entirely
			if s.hasFullDayGroupUnavailable(groupID, day) {
				dayBreaks[day] = 0
				continue
			}
			found := false
			for _, bp := range s.config.Breaks.Periods {
				if !s.groupHasPredefinedAt(groupID, day, bp) &&
					!s.isGroupUnavailable(groupID, day, bp) {
					dayBreaks[day] = bp
					found = true
					break
				}
			}
			if !found {
				dayBreaks[day] = 0
			}
		}
		groupBreak[groupID] = dayBreaks
	}

	return instBreak, groupBreak
}

// buildEntityBreakPriorities builds priority maps from break maps.
func (s *Scheduler) buildEntityBreakPriorities(
	instBreak map[string]map[types.Day]int,
	groupBreak map[string]map[types.Day]int,
) (map[string]map[types.Day]int, map[string]map[types.Day]int) {
	instPriority := make(map[string]map[types.Day]int)
	groupPriority := make(map[string]map[types.Day]int)

	for instID, dayBreaks := range instBreak {
		pri := make(map[types.Day]int)
		for day, bp := range dayBreaks {
			if bp > 0 {
				for i, p := range s.config.Breaks.Periods {
					if p == bp {
						pri[day] = i + 1
						break
					}
				}
			}
		}
		instPriority[instID] = pri
	}

	for groupID, dayBreaks := range groupBreak {
		pri := make(map[types.Day]int)
		for day, bp := range dayBreaks {
			if bp > 0 {
				for i, p := range s.config.Breaks.Periods {
					if p == bp {
						pri[day] = i + 1
						break
					}
				}
			}
		}
		groupPriority[groupID] = pri
	}

	return instPriority, groupPriority
}

// hasFullDayInstructorUnavailable checks if an instructor has a full-day (1-15) unavailable entry on the given day.
func (s *Scheduler) hasFullDayInstructorUnavailable(instID string, day types.Day) bool {
	for _, iu := range s.config.InstructorUnavailable {
		if iu.InstructorID == instID && iu.Day == day && types.IsFullDay(iu.StartPeriod, iu.EndPeriod) {
			return true
		}
	}
	return false
}

// hasFullDayGroupUnavailable checks if a group has a full-day (1-15) unavailable entry on the given day.
func (s *Scheduler) hasFullDayGroupUnavailable(groupID string, day types.Day) bool {
	for _, gu := range s.config.GroupsUnavailable {
		if gu.GroupID == groupID && gu.Day == day && types.IsFullDay(gu.StartPeriod, gu.EndPeriod) {
			return true
		}
	}
	return false
}

func (s *Scheduler) logEntityBreaks(instBreak map[string]map[types.Day]int, groupBreak map[string]map[types.Day]int) {
	s.log("  Lunch break by entity (sample):")
	// Sample a few instructors
	count := 0
	for instID, dayBreaks := range instBreak {
		if count >= 3 {
			break
		}
		for _, day := range types.AllDays() {
			if bp, ok := dayBreaks[day]; ok && bp > 0 {
				s.log("    Instructor %s %s: period %d", instID, day, bp)
			}
		}
		count++
	}
	// Sample a few groups
	count = 0
	for groupID, dayBreaks := range groupBreak {
		if count >= 3 {
			break
		}
		for _, day := range types.AllDays() {
			if bp, ok := dayBreaks[day]; ok && bp > 0 {
				s.log("    Group %s %s: period %d", groupID, day, bp)
			}
		}
		count++
	}
}

// estimateDomainSize counts how many (day, start) positions pass static constraints
// for this offering.
func (s *Scheduler) estimateDomainSize(o *types.Offering, total int,
	instBreak map[string]map[types.Day]int, groupBreak map[string]map[types.Day]int) int {
	count := 0
	for _, day := range types.AllDays() {
		if !s.isDayValidForGroups(o.GroupIDs, day) {
			continue
		}
		maxStart := s.periodLimit - total + 1
		if maxStart < 1 {
			continue
		}
		for start := 1; start <= maxStart; start++ {
			// Check per-entity break conflicts
			if hasBreakConflict(o, day, start, total, o.TheoryPeriods, instBreak, groupBreak) {
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
	score      float64 // heuristic cost used to order candidate positions (lower is tried first)
}

// backtrackSchedule attempts to schedule all units using backtracking search.
// It generates positions dynamically (considering current resource state)
// and backtracks when a unit can't be placed. maxBacktracks limits the total
// number of backtrack operations to bound runtime.
func (s *Scheduler) backtrackSchedule(units []unit,
	instBreak map[string]map[types.Day]int,
	groupBreak map[string]map[types.Day]int,
	maxBacktracks int) ([]innerAssignment, bool) {
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
	curPositions := s.genPositions(units[0], instBreak, groupBreak, instB, grpB, roomB)
	stack = append(stack, state{unitIdx: 0, posIndex: 0, positions: curPositions})

	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		u := units[top.unitIdx]

		// Try positions from posIndex onwards
		found := false
		for top.posIndex < len(top.positions) {
			p := top.positions[top.posIndex]
			top.posIndex++

			// Verify room availability now (resources may have changed),
			// preferring the same room for theory and lab
			theoryRoom, labRoom, ok := s.pickRooms(u.offering, p.day, p.start,
				u.theoryPeriods, u.labPeriods, roomB, false)
			if !ok {
				continue
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

			nextPositions := s.genPositions(units[top.unitIdx+1], instBreak, groupBreak, instB, grpB, roomB)
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

// freeRooms returns the rooms from candidates that are free (not booked and not
// reserved by instructor/group unavailability) for periods [from, to) on the given day.
func (s *Scheduler) freeRooms(candidates []string, day types.Day, from, to int,
	roomB map[string]map[resourceKey]bool) []string {
	var free []string
	for _, rm := range candidates {
		avail := true
		for p := from; p < to; p++ {
			rk := resourceKey{day: day, period: p}
			if (roomB[rm] != nil && roomB[rm][rk]) ||
				s.isRoomReservedByInstructorUnavailable(rm, day, p) ||
				s.isRoomReservedByGroupsUnavailable(rm, day, p) {
				avail = false
				break
			}
		}
		if avail {
			free = append(free, rm)
		}
	}
	return free
}

// pickRooms chooses the theory and lab rooms for an offering placed at (day, start).
// When both theory and lab need a room, it prefers one room that serves both blocks
// and falls back to different rooms only when no such common room is free.
// Rooms flagged "x" are returned as "x". ok is false if a required room is unavailable.
func (s *Scheduler) pickRooms(o *types.Offering, day types.Day, start, theoryPeriods, labPeriods int,
	roomB map[string]map[resourceKey]bool, shuffle bool) (theoryRoom, labRoom string, ok bool) {
	theoryRoom, labRoom = "x", "x"

	order := func(ids []string) []string {
		rooms := make([]string, len(ids))
		copy(rooms, ids)
		if shuffle {
			s.rng.Shuffle(len(rooms), func(i, j int) { rooms[i], rooms[j] = rooms[j], rooms[i] })
		}
		return rooms
	}

	needTheory := theoryPeriods > 0 && o.TheoryRoomRaw != "x" && len(o.TheoryRoomIDs) > 0
	needLab := labPeriods > 0 && o.LabRoomRaw != "x" && len(o.LabRoomIDs) > 0

	var theoryFree, labFree []string
	if needTheory {
		theoryFree = s.freeRooms(order(o.TheoryRoomIDs), day, start, start+theoryPeriods, roomB)
		if len(theoryFree) == 0 {
			return "x", "x", false
		}
		theoryRoom = theoryFree[0]
	}
	if needLab {
		labFree = s.freeRooms(order(o.LabRoomIDs), day, start+theoryPeriods, start+theoryPeriods+labPeriods, roomB)
		if len(labFree) == 0 {
			return "x", "x", false
		}
		labRoom = labFree[0]
	}

	// Prefer the same room for both blocks; differ only when no common room is free
	if needTheory && needLab {
		inLab := make(map[string]bool, len(labFree))
		for _, r := range labFree {
			inLab[r] = true
		}
		for _, r := range theoryFree {
			if inLab[r] {
				return r, r, true
			}
		}
	}
	return theoryRoom, labRoom, true
}

// Weights of the "late period" penalty. A period is late when it is after
// types.PreferredMaxPeriod. The penalty is applied per person (instructor or group) and is
// convex, so late periods are spread over many people instead of piling up on a few.
const (
	latePeriodWeight = 150.0 // each late period
	lateSquareWeight = 100.0 // (late periods of a person)^2: spread the load across people
	lateDayWeight    = 400.0 // (days with late periods of a person)^2: avoid late classes on many days
	lateDepthWeight  = 100.0 // per period beyond the preferred last period (period 15 is worse than 10)
)

// latePenaltyDelta is the extra penalty when a person who already has count late periods
// on lateDays days gets add more late periods (total depth beyond the preferred period)
// on newDays additional days.
func latePenaltyDelta(count, lateDays, add, newDays, depth int) float64 {
	if add == 0 {
		return 0
	}
	c0, c1 := float64(count), float64(count+add)
	d0, d1 := float64(lateDays), float64(lateDays+newDays)
	return latePeriodWeight*float64(add) +
		lateSquareWeight*(c1*c1-c0*c0) +
		lateDayWeight*(d1*d1-d0*d0) +
		lateDepthWeight*float64(depth)
}

// lateLoad counts the late periods and the days having late periods in a booking map.
func lateLoad(booked map[resourceKey]bool) (count int, days map[types.Day]bool) {
	for rk := range booked {
		if rk.period > types.PreferredMaxPeriod {
			if days == nil {
				days = make(map[types.Day]bool)
			}
			days[rk.day] = true
			count++
		}
	}
	return count, days
}

// positionScore rates placing unit u at (day, start): the extra late-period penalty it adds
// to the people involved (given their current bookings) plus a preference for early starts.
func (s *Scheduler) positionScore(u unit, day types.Day, start int,
	instB, grpB map[string]map[resourceKey]bool) float64 {
	o := u.offering

	// late periods this placement adds: all of them, and the lab ones (co-instructors: lab only)
	var all, labOnly, depthAll, depthLab int
	for p := start; p < start+u.totalPeriods; p++ {
		if p <= types.PreferredMaxPeriod {
			continue
		}
		all++
		depthAll += p - types.PreferredMaxPeriod
		if p >= start+u.theoryPeriods {
			labOnly++
			depthLab += p - types.PreferredMaxPeriod
		}
	}

	score := float64(start) * 5.0
	if u.theoryPeriods > 0 && u.labPeriods > 0 {
		// same preference as calculateCost: theory + lab blocks start at period 1 or 6
		if start != 1 && start != 6 {
			score += 100.0 * math.Min(math.Abs(float64(start-1)), math.Abs(float64(start-6)))
		}
	}

	person := func(booked map[resourceKey]bool, add, depth int) {
		if add == 0 {
			return
		}
		count, days := lateLoad(booked)
		newDays := 1
		if days[day] {
			newDays = 0
		}
		score += latePenaltyDelta(count, len(days), add, newDays, depth)
	}
	if o.MainInstructorID != "x" {
		person(instB[o.MainInstructorID], all, depthAll)
	}
	for _, coID := range o.CoInstructorIDs {
		person(instB[coID], labOnly, depthLab)
	}
	for _, gid := range o.GroupIDs {
		person(grpB[gid], all, depthAll)
	}

	// small jitter so that equal-looking positions differ between attempts
	return score + s.rng.Float64()*10.0
}

// latePenalty is the total per-person late-period penalty of a set of assignments
// (scheduled ones plus the pre-defined ones from config.txt).
func (s *Scheduler) latePenalty(assignments []*types.Assignment) float64 {
	type person struct {
		id    string
		group bool
	}
	type load struct {
		count, depth int
		days         map[types.Day]bool
	}
	loads := make(map[person]*load)
	add := func(id string, group bool, day types.Day, first, n int) {
		for p := first; p < first+n; p++ {
			if p <= types.PreferredMaxPeriod {
				continue
			}
			k := person{id, group}
			l := loads[k]
			if l == nil {
				l = &load{days: make(map[types.Day]bool)}
				loads[k] = l
			}
			l.count++
			l.depth += p - types.PreferredMaxPeriod
			l.days[day] = true
		}
	}

	for _, a := range assignments {
		o := a.Offering
		if o.MainInstructorID != "x" {
			add(o.MainInstructorID, false, a.Day, a.TheoryStart, o.TheoryPeriods)
			add(o.MainInstructorID, false, a.Day, a.LabStart, o.LabPeriods)
		}
		for _, coID := range o.CoInstructorIDs {
			add(coID, false, a.Day, a.LabStart, o.LabPeriods)
		}
		for _, gid := range o.GroupIDs {
			add(gid, true, a.Day, a.TheoryStart, o.TheoryPeriods)
			add(gid, true, a.Day, a.LabStart, o.LabPeriods)
		}
	}
	for _, pa := range s.predefinedAssignments {
		if pa.MainInstructorID != "x" {
			add(pa.MainInstructorID, false, pa.Day, pa.TheoryStart, pa.TheoryPeriods)
			add(pa.MainInstructorID, false, pa.Day, pa.LabStart, pa.LabPeriods)
		}
		for _, coID := range pa.CoInstructorIDs {
			add(coID, false, pa.Day, pa.LabStart, pa.LabPeriods)
		}
		for _, gid := range pa.GroupIDs {
			add(gid, true, pa.Day, pa.TheoryStart, pa.TheoryPeriods)
			add(gid, true, pa.Day, pa.LabStart, pa.LabPeriods)
		}
	}

	total := 0.0
	for _, l := range loads {
		total += latePenaltyDelta(0, 0, l.count, len(l.days), l.depth)
	}
	return total
}

// genPositions generates valid (day, start) positions for an offering
// considering CURRENT resource state (room booking, double booking).
// Returns positions sorted by heuristic quality.
func (s *Scheduler) genPositions(
	u unit,
	instBreak map[string]map[types.Day]int,
	groupBreak map[string]map[types.Day]int,
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
		maxStart := s.periodLimit - total + 1
		if maxStart < 1 {
			continue
		}
		for start := 1; start <= maxStart; start++ {
			// Check per-entity break conflicts
			if hasBreakConflict(o, day, start, total, u.theoryPeriods, instBreak, groupBreak) {
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

			// Pick rooms (same room for theory and lab whenever possible)
			theoryRoom, labRoom, ok := s.pickRooms(o, day, start, u.theoryPeriods, u.labPeriods, roomB, false)
			if !ok {
				continue // no room
			}

			positions = append(positions, pos{
				day:        day,
				start:      start,
				theoryRoom: theoryRoom,
				labRoom:    labRoom,
				score:      s.positionScore(u, day, start, instB, grpB),
			})
		}
	}

	// Try the best positions first (early periods, little extra late load for anyone).
	// The random jitter in the score keeps different attempts from being identical.
	sort.SliceStable(positions, func(i, j int) bool { return positions[i].score < positions[j].score })

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

// hasBreakConflict checks if any entity involved in an offering has a lunch break
// that overlaps with the given position (day, start, total periods).
func hasBreakConflict(
	o *types.Offering,
	day types.Day,
	start int,
	total int,
	theoryPeriods int,
	instBreak map[string]map[types.Day]int,
	groupBreak map[string]map[types.Day]int,
) bool {
	// Check main instructor's break
	if o.MainInstructorID != "x" {
		if dayBreaks, ok := instBreak[o.MainInstructorID]; ok {
			if bp, ok2 := dayBreaks[day]; ok2 && bp > 0 && start <= bp && bp < start+total {
				return true
			}
		}
	}

	// Check groups' breaks
	for _, gid := range o.GroupIDs {
		if dayBreaks, ok := groupBreak[gid]; ok {
			if bp, ok2 := dayBreaks[day]; ok2 && bp > 0 && start <= bp && bp < start+total {
				return true
			}
		}
	}

	// Check co-instructors' breaks (only during lab periods)
	labStart := start + theoryPeriods
	for _, coID := range o.CoInstructorIDs {
		if dayBreaks, ok := instBreak[coID]; ok {
			if bp, ok2 := dayBreaks[day]; ok2 && bp > 0 && bp >= labStart && bp < start+total {
				return true
			}
		}
	}

	return false
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
	t := 500.0 // costs are in the hundreds, so start warm enough to accept some worse moves
	tMin := 1.0
	alpha := 0.9995
	maxIter := 15000
	stagnationLimit := 3000
	stagnation := 0

	curCost := s.calculateCost(schedule)
	bestCost := curCost
	bestAssign := copyAssignments(schedule.Assignments)
	s.log("  Initial cost: %.2f", bestCost)

	for iter := 0; iter < maxIter && t > tMin; iter++ {
		var newAssign []*types.Assignment
		var ok bool
		if s.rng.Intn(2) == 0 {
			newAssign, ok = s.randomShift(schedule)
		} else {
			newAssign, ok = s.swapShift(schedule)
		}
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

		delta := newCost - curCost

		if delta < 0 || s.rng.Float64() < math.Exp(-delta/t) {
			curCost = newCost
			if newCost < bestCost {
				bestCost = newCost
				bestAssign = copyAssignments(newAssign)
				stagnation = 0
			} else {
				stagnation++
			}
		} else {
			schedule.Assignments = oldAssign
			stagnation++
		}
		if stagnation >= stagnationLimit {
			break
		}

		t *= alpha
	}

	schedule.Assignments = bestAssign
	s.log("  Final cost: %.2f", bestCost)
}

// tryUpgradeEntityBreaks attempts to upgrade each entity's break to a higher-priority period.
// For each entity that has a lower-priority break, it checks if the higher-priority period
// would conflict with any assignment that entity is involved in.
func (s *Scheduler) tryUpgradeEntityBreaks(schedule *types.Schedule) bool {
	upgraded := false

	// Helper: for a given entity and day, try to upgrade their break
	tryUpgrade := func(entityType string, entityID string, day types.Day,
		breakMap map[string]map[types.Day]int,
		priorityMap map[string]map[types.Day]int) {

		currentBreak := 0
		if dayBreaks, ok := breakMap[entityID]; ok {
			currentBreak = dayBreaks[day]
		}
		if currentBreak == 0 {
			return
		}

		// หา higher-priority break periods ที่มี priority สูงกว่า current break
		var higherBreaks []int
		for _, bp := range s.config.Breaks.Periods {
			if bp == currentBreak {
				break
			}
			higherBreaks = append(higherBreaks, bp)
		}

		if len(higherBreaks) == 0 {
			return
		}

		// ลอง upgrade ไปยัง higher-priority break ที่ compatible สำหรับ entity นี้
		for _, bp := range higherBreaks {
			// Skip periods in which this entity has a pre-defined class
			if (entityType == "instructor" && s.instructorHasPredefinedAt(entityID, day, bp)) ||
				(entityType == "group" && s.groupHasPredefinedAt(entityID, day, bp)) {
				continue
			}

			// ข้าม break period ที่อยู่ใน instructor_unavailable หรือ groups_unavailable
			if entityType == "instructor" && s.isInstructorUnavailable(entityID, day, bp) {
				continue
			}
			if entityType == "group" && s.isGroupUnavailable(entityID, day, bp) {
				continue
			}

			// ตรวจสอบว่า entity นี้มี assignment ใดที่ overlap กับ break period นี้ในวันนี้หรือไม่
			compatible := true
			for _, a := range schedule.Assignments {
				if a.Day != day {
					continue
				}
				if !a.ContainsPeriod(day, bp) {
					continue
				}

				// Check if this entity is involved in this assignment
				if entityType == "instructor" {
					if a.Offering.MainInstructorID == entityID {
						compatible = false
						break
					}
					for _, coID := range a.Offering.CoInstructorIDs {
						if coID == entityID {
							compatible = false
							break
						}
					}
				} else if entityType == "group" {
					for _, gid := range a.Offering.GroupIDs {
						if gid == entityID {
							compatible = false
							break
						}
					}
					if !compatible {
						break
					}
				}
				if !compatible {
					break
				}
			}

			if compatible {
				// Upgrade! เปลี่ยน lunch break entity นี้เป็น period นี้
				if breakMap[entityID] == nil {
					breakMap[entityID] = make(map[types.Day]int)
				}
				breakMap[entityID][day] = bp

				// Update priority
				for i, p := range s.config.Breaks.Periods {
					if p == bp {
						if priorityMap[entityID] == nil {
							priorityMap[entityID] = make(map[types.Day]int)
						}
						priorityMap[entityID][day] = i + 1
						break
					}
				}

				s.log("  Upgraded %s %s %s break from period %d to %d",
					entityType, entityID, day, currentBreak, bp)
				upgraded = true
				break
			}
		}
	}

	// Try upgrade each instructor
	for instID := range schedule.InstructorLunchBreak {
		for _, day := range types.AllDays() {
			tryUpgrade("instructor", instID, day,
				schedule.InstructorLunchBreak,
				schedule.InstructorBreakPriority)
		}
	}

	// Try upgrade each group
	for groupID := range schedule.GroupLunchBreak {
		for _, day := range types.AllDays() {
			tryUpgrade("group", groupID, day,
				schedule.GroupLunchBreak,
				schedule.GroupBreakPriority)
		}
	}

	return upgraded
}

func copyAssignments(a []*types.Assignment) []*types.Assignment {
	r := make([]*types.Assignment, len(a))
	for i, v := range a {
		cp := *v
		r[i] = &cp
	}
	return r
}

// endPeriod returns the last period used by an assignment.
func endPeriod(a *types.Assignment) int {
	end := 0
	if a.TheoryStart > 0 {
		end = a.TheoryStart + a.Offering.TheoryPeriods - 1
	}
	if a.LabStart > 0 {
		if e := a.LabStart + a.Offering.LabPeriods - 1; e > end {
			end = e
		}
	}
	return end
}

// pickShiftTarget returns the index of an assignment to move. Half of the time it prefers a
// class that runs past the preferred last period.
func (s *Scheduler) pickShiftTarget(ass []*types.Assignment) int {
	idx := s.rng.Intn(len(ass))
	if s.rng.Intn(2) == 0 {
		var late []int
		for i, v := range ass {
			if v.Offering.TheoryPeriods+v.Offering.LabPeriods > 0 && endPeriod(v) > types.PreferredMaxPeriod {
				late = append(late, i)
			}
		}
		if len(late) > 0 {
			idx = late[s.rng.Intn(len(late))]
		}
	}
	return idx
}

// bookingsFor builds the instructor/group/room bookings of the given assignments plus the
// pre-defined ones from config.txt, so annealing moves never create conflicts.
func (s *Scheduler) bookingsFor(rest []*types.Assignment) (instB, grpB, roomB map[string]map[resourceKey]bool) {
	instB = make(map[string]map[resourceKey]bool)
	grpB = make(map[string]map[resourceKey]bool)
	roomB = make(map[string]map[resourceKey]bool)

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

	return instB, grpB, roomB
}

// placeAt checks whether offering o can be placed at (day, start) given the bookings and, if so,
// returns the assignment (rooms are picked at random among the free ones).
func (s *Scheduler) placeAt(schedule *types.Schedule, o *types.Offering, day types.Day, start int,
	instB, grpB, roomB map[string]map[resourceKey]bool) (*types.Assignment, bool) {
	total := o.TheoryPeriods + o.LabPeriods
	if start < 1 || start+total-1 > s.periodLimit || !s.isDayValidForGroups(o.GroupIDs, day) {
		return nil, false
	}
	// Check per-entity break conflicts for this offering
	if hasBreakConflict(o, day, start, total, o.TheoryPeriods,
		schedule.InstructorLunchBreak, schedule.GroupLunchBreak) {
		return nil, false
	}

	theoryRoom, labRoom, ok := s.pickRooms(o, day, start, o.TheoryPeriods, o.LabPeriods, roomB, true)
	if !ok {
		return nil, false
	}

	for p := start; p < start+total; p++ {
		rk := resourceKey{day: day, period: p}
		isTheory := p < start+o.TheoryPeriods

		for _, gid := range o.GroupIDs {
			if s.isGroupUnavailable(gid, day, p) || grpB[gid][rk] {
				return nil, false
			}
		}
		if o.MainInstructorID != "x" {
			id := o.MainInstructorID
			if s.isInstructorUnavailable(id, day, p) || s.isInstructorUnavailableMain(id, day, p) ||
				s.isInstructorNoLate(id, day, start, total) || instB[id][rk] {
				return nil, false
			}
		}
		if !isTheory {
			for _, coID := range o.CoInstructorIDs {
				if s.isInstructorUnavailable(coID, day, p) || instB[coID][rk] {
					return nil, false
				}
			}
		}
	}

	return &types.Assignment{
		Offering:     o,
		Day:          day,
		TheoryStart:  start,
		LabStart:     start + o.TheoryPeriods,
		TheoryRoomID: theoryRoom,
		LabRoomID:    labRoom,
	}, true
}

func (s *Scheduler) randomShift(schedule *types.Schedule) ([]*types.Assignment, bool) {
	ass := schedule.Assignments
	if len(ass) == 0 {
		return nil, false
	}

	idx := s.pickShiftTarget(ass)
	o := ass[idx].Offering
	total := o.TheoryPeriods + o.LabPeriods

	rest := make([]*types.Assignment, 0, len(ass)-1)
	for i, v := range ass {
		if i != idx {
			rest = append(rest, v)
		}
	}
	instB, grpB, roomB := s.bookingsFor(rest)

	days := types.AllDays()
	s.rng.Shuffle(len(days), func(i, j int) { days[i], days[j] = days[j], days[i] })

	for _, day := range days {
		maxStart := s.periodLimit - total + 1
		if maxStart < 1 {
			continue
		}
		starts := make([]int, maxStart)
		for i := range starts {
			starts[i] = i + 1
		}
		s.rng.Shuffle(len(starts), func(i, j int) { starts[i], starts[j] = starts[j], starts[i] })

		for _, start := range starts {
			if na, ok := s.placeAt(schedule, o, day, start, instB, grpB, roomB); ok {
				return append(rest, na), true
			}
		}
	}

	return nil, false
}

// swapShift exchanges the time slots of two classes (each takes the other's day and start).
// A plain relocation cannot do this when the schedule is tight, which is exactly when
// pulling a late class earlier requires pushing another class later.
func (s *Scheduler) swapShift(schedule *types.Schedule) ([]*types.Assignment, bool) {
	ass := schedule.Assignments
	if len(ass) < 2 {
		return nil, false
	}
	i := s.pickShiftTarget(ass)
	j := s.rng.Intn(len(ass))
	if i == j {
		return nil, false
	}
	a, b := ass[i], ass[j]
	if a.Day == b.Day && a.TheoryStart == b.TheoryStart {
		return nil, false
	}

	rest := make([]*types.Assignment, 0, len(ass)-2)
	for k, v := range ass {
		if k != i && k != j {
			rest = append(rest, v)
		}
	}
	instB, grpB, roomB := s.bookingsFor(rest)

	na, ok := s.placeAt(schedule, a.Offering, b.Day, b.TheoryStart, instB, grpB, roomB)
	if !ok {
		return nil, false
	}
	s.bookResources(innerAssignment{
		offering: na.Offering, day: na.Day, theoryStart: na.TheoryStart, labStart: na.LabStart,
		theoryRoom: na.TheoryRoomID, labRoom: na.LabRoomID,
	}, na.Offering, na.Offering.TheoryPeriods+na.Offering.LabPeriods, instB, grpB, roomB)

	nb, ok := s.placeAt(schedule, b.Offering, a.Day, a.TheoryStart, instB, grpB, roomB)
	if !ok {
		return nil, false
	}
	return append(rest, na, nb), true
}

// ==========================================
// Cost Function
// ==========================================

func (s *Scheduler) calculateCost(sch *types.Schedule) float64 {
	cost := 0.0

	// Penalty สำหรับการใช้ break period ที่มี priority ต่ำกว่าสำหรับแต่ละ entity
	// priority = 1 คือดีที่สุด (highest priority)
	// รวม penalty ของ instructors
	for instID := range sch.InstructorBreakPriority {
		for _, day := range types.AllDays() {
			if pri, ok := sch.InstructorBreakPriority[instID][day]; ok && pri > 1 {
				cost += float64(pri-1) * 100.0
			}
		}
	}
	// รวม penalty ของ groups
	for groupID := range sch.GroupBreakPriority {
		for _, day := range types.AllDays() {
			if pri, ok := sch.GroupBreakPriority[groupID][day]; ok && pri > 1 {
				cost += float64(pri-1) * 100.0
			}
		}
	}

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


		if o.TheoryPeriods > 0 && o.LabPeriods > 0 {
			if a.TheoryRoomID != "x" && a.LabRoomID != "x" && a.TheoryRoomID != a.LabRoomID {
				cost += 20.0
			}
		}
	}

	// Avoid periods after the preferred last period; share the unavoidable ones fairly
	cost += s.latePenalty(sch.Assignments)

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

// instructorHasPredefinedAt reports whether the instructor teaches a pre-defined class in
// the given period (the main instructor in the whole class, a co-instructor in its lab block).
func (s *Scheduler) instructorHasPredefinedAt(instructorID string, day types.Day, period int) bool {
	for _, pa := range s.predefinedAssignments {
		if !IsInPredefinedPeriod(pa, day, period) {
			continue
		}
		if pa.MainInstructorID == instructorID {
			return true
		}
		inLab := pa.LabStart > 0 && period >= pa.LabStart && period < pa.LabStart+pa.LabPeriods
		if inLab {
			for _, coID := range pa.CoInstructorIDs {
				if coID == instructorID {
					return true
				}
			}
		}
	}
	return false
}

// groupHasPredefinedAt reports whether the group has a pre-defined class in the given period.
func (s *Scheduler) groupHasPredefinedAt(groupID string, day types.Day, period int) bool {
	for _, pa := range s.predefinedAssignments {
		if !IsInPredefinedPeriod(pa, day, period) {
			continue
		}
		for _, gid := range pa.GroupIDs {
			if gid == groupID {
				return true
			}
		}
	}
	return false
}

// PredefinedOnlySchedule returns an empty schedule carrying only the lunch breaks, for when
// every offering is already pre-defined. Each instructor and group gets, per day, the
// highest-priority break period that is free of their own pre-defined classes and
// unavailable periods.
func (s *Scheduler) PredefinedOnlySchedule() *types.Schedule {
	instBreak, groupBreak := s.buildEntityBreaks()
	instPriority, groupPriority := s.buildEntityBreakPriorities(instBreak, groupBreak)
	return &types.Schedule{
		InstructorLunchBreak:    instBreak,
		GroupLunchBreak:         groupBreak,
		InstructorBreakPriority: instPriority,
		GroupBreakPriority:      groupPriority,
		Config:                  s.config,
	}
}

// IsInPredefinedPeriod checks if the given period on the specified day is covered
// by the predefined assignment.
func IsInPredefinedPeriod(pa *types.PredefinedAssignment, day types.Day, period int) bool {
	if pa.Day != day {
		return false
	}
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
