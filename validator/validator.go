// Package validator validates the parsed config for cross-reference consistency and feasibility.
package validator

import (
	"fmt"
	"strings"

	"timetablex/types"
)

// ValidationError represents a validation error.
type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}

// Validate checks the config for consistency and feasibility.
// Returns nil if valid, or a list of errors.
func Validate(config *types.Config) []ValidationError {
	var errs []ValidationError

	if config == nil {
		return []ValidationError{{Message: "config is nil"}}
	}

	// Check breaks
	if len(config.Breaks.Periods) == 0 {
		errs = append(errs, ValidationError{Message: "break: must have at least one period"})
	}

	// Check cross-references in offerings
	for i, offering := range config.Offerings {
		// Check course exists
		if offering.MainInstructorID != "x" {
			courseID := offering.CourseID
			if _, ok := config.Courses[courseID]; !ok {
				errs = append(errs, ValidationError{
					Message: fmt.Sprintf("offering #%d: course_id '%s' not found in [courses]", i+1, courseID),
				})
			}
		}

		// Check instructor exists
		if offering.MainInstructorID != "x" {
			if _, ok := config.Instructors[offering.MainInstructorID]; !ok {
				errs = append(errs, ValidationError{
					Message: fmt.Sprintf("offering #%d: main_instructor_id '%s' not found in [instructor]", i+1, offering.MainInstructorID),
				})
			}
		}

		// Check co-instructors exist
		for _, coID := range offering.CoInstructorIDs {
			if _, ok := config.Instructors[coID]; !ok {
				errs = append(errs, ValidationError{
					Message: fmt.Sprintf("offering #%d: co_instructor_id '%s' not found in [instructor]", i+1, coID),
				})
			}
		}

		// Check groups exist
		for _, gid := range offering.GroupIDs {
			if _, ok := config.Groups[gid]; !ok {
				errs = append(errs, ValidationError{
					Message: fmt.Sprintf("offering #%d: group_id '%s' not found in [groups]", i+1, gid),
				})
			}
		}

		// Check theory rooms exist
		for _, rid := range offering.TheoryRoomIDs {
			if _, ok := config.Rooms[rid]; !ok {
				errs = append(errs, ValidationError{
					Message: fmt.Sprintf("offering #%d: theory_room_id '%s' not found in [rooms]", i+1, rid),
				})
			}
		}

		// Check lab rooms exist
		for _, rid := range offering.LabRoomIDs {
			if _, ok := config.Rooms[rid]; !ok {
				errs = append(errs, ValidationError{
					Message: fmt.Sprintf("offering #%d: lab_room_id '%s' not found in [rooms]", i+1, rid),
				})
			}
		}
	}

	// Check groups_unavailable references
	for i, gu := range config.GroupsUnavailable {
		if _, ok := config.Groups[gu.GroupID]; !ok {
			errs = append(errs, ValidationError{
				Message: fmt.Sprintf("groups_unavailable #%d: group_id '%s' not found in [groups]", i+1, gu.GroupID),
			})
		}
		if gu.RoomID != "none" && gu.RoomID != "" {
			if _, ok := config.Rooms[gu.RoomID]; !ok {
				errs = append(errs, ValidationError{
					Message: fmt.Sprintf("groups_unavailable #%d: room_id '%s' not found in [rooms]", i+1, gu.RoomID),
				})
			}
		}
	}

	// Check instructor_unavailable references
	for i, iu := range config.InstructorUnavailable {
		if _, ok := config.Instructors[iu.InstructorID]; !ok {
			errs = append(errs, ValidationError{
				Message: fmt.Sprintf("instructor_unavailable #%d: instructor_id '%s' not found in [instructor]", i+1, iu.InstructorID),
			})
		}
		if iu.RoomID != "none" && iu.RoomID != "" {
			if _, ok := config.Rooms[iu.RoomID]; !ok {
				errs = append(errs, ValidationError{
					Message: fmt.Sprintf("instructor_unavailable #%d: room_id '%s' not found in [rooms]", i+1, iu.RoomID),
				})
			}
		}
	}

	// Check instructor_unavailable_main references
	for i, ium := range config.InstructorUnavailableMain {
		if _, ok := config.Instructors[ium.InstructorID]; !ok {
			errs = append(errs, ValidationError{
				Message: fmt.Sprintf("instructor_unavailable_main #%d: instructor_id '%s' not found in [instructor]", i+1, ium.InstructorID),
			})
		}
	}

	// Check instructor_nolate references
	for i, inl := range config.InstructorNoLate {
		if _, ok := config.Instructors[inl.InstructorID]; !ok {
			errs = append(errs, ValidationError{
				Message: fmt.Sprintf("instructor_nolate #%d: instructor_id '%s' not found in [instructor]", i+1, inl.InstructorID),
			})
		}
	}

	// Preliminary feasibility check: total period demand vs capacity
	errs = append(errs, checkFeasibilityPreliminary(config)...)

	return errs
}

func getInstructorNoLateThreshold(config *types.Config, instructorID string, day types.Day) int {
	for _, inl := range config.InstructorNoLate {
		if inl.InstructorID == instructorID && inl.Day == day {
			return inl.PeriodThreshold
		}
	}
	return 0 // no constraint
}

// checkFeasibilityPreliminary performs a preliminary feasibility check.
func checkFeasibilityPreliminary(config *types.Config) []ValidationError {
	var errs []ValidationError

	// Count total periods per instructor
	instructorLoad := make(map[string]int)
	for _, offering := range config.Offerings {
		if offering.MainInstructorID != "x" {
			instructorLoad[offering.MainInstructorID] += offering.TotalPeriods()
		}
		for _, coID := range offering.CoInstructorIDs {
			instructorLoad[coID] += offering.LabPeriods
		}
	}

	// Maximum available periods per day per instructor = 13 (minus unavailable periods and break)
	maxPeriodsPerWeek := len(types.AllDays()) * types.MaxPeriodsPerDay

	for _, inst := range config.Instructors {
		load := instructorLoad[inst.ID]
		if load > maxPeriodsPerWeek {
			errs = append(errs, ValidationError{
				Message: fmt.Sprintf("instructor '%s' (%s) has total load of %d periods, exceeding max possible %d periods/week (preliminary - may still be feasible with proper scheduling)",
					inst.ID, inst.Name, load, maxPeriodsPerWeek),
			})
		}
	}

	// Count total periods per group
	groupLoad := make(map[string]int)
	for _, offering := range config.Offerings {
		for _, gid := range offering.GroupIDs {
			groupLoad[gid] += offering.TotalPeriods()
		}
	}

	for _, grp := range config.Groups {
		load := groupLoad[grp.ID]
		maxPeriods := maxPeriodsPerWeek
		if grp.TermType == types.Normal {
			maxPeriods = 5 * types.MaxPeriodsPerDay // Mon-Fri
		} else {
			maxPeriods = 2 * types.MaxPeriodsPerDay // Sat-Sun
		}
		if load > maxPeriods {
			errs = append(errs, ValidationError{
				Message: fmt.Sprintf("group '%s' (%s) has total load of %d periods, exceeding max possible %d periods/week (preliminary)",
					grp.ID, grp.Name, load, maxPeriods),
			})
		}
	}

	return errs
}

// CanSchedule checks basic feasibility: whether there exists at least one possible position for each offering.
// This is a simplified check that doesn't consider all constraints but catches obvious infeasibility.
func CanSchedule(config *types.Config) (bool, string) {
	if len(config.Offerings) == 0 {
		return false, "no offerings to schedule"
	}

	// Check each offering has at least some valid periods
	allDays := types.AllDays()

	for i, offering := range config.Offerings {
		hasValidPosition := false

		for _, day := range allDays {
			groupDayOK := true
			for _, gid := range offering.GroupIDs {
				if grp, ok := config.Groups[gid]; ok {
					if grp.TermType == types.Normal && (day == types.Saturday || day == types.Sunday) {
						groupDayOK = false
						break
					}
					if grp.TermType == types.Special && (day != types.Saturday && day != types.Sunday) {
						groupDayOK = false
						break
					}
				}
			}
			if !groupDayOK {
				continue
			}

			// Check if there's any start period that would fit
			maxStart := types.MaxPeriodsPerDay - offering.TotalPeriods() + 1
			if maxStart < 1 {
				continue
			}
			_ = maxStart

			// Check if instructor is available for some start period
			if offering.MainInstructorID != "x" {
				instUnavailable := getInstructorUnavailableForDay(config, offering.MainInstructorID, day)
				instUnavailableMain := getInstructorUnavailableMainForDay(config, offering.MainInstructorID, day)
				nolateThreshold := getInstructorNoLateThreshold(config, offering.MainInstructorID, day)

				for start := 1; start <= types.MaxPeriodsPerDay-offering.TotalPeriods()+1; start++ {
					valid := true
					// Check instructor constraints
					for p := start; p < start+offering.TotalPeriods(); p++ {
						if isInRange(p, instUnavailable) {
							valid = false
							break
						}
						if nolateThreshold > 0 && p >= nolateThreshold {
							valid = false
							break
						}
						if p < start+offering.TheoryPeriods {
							// Theory period: main instructor must be available as main
							if isInRange(p, instUnavailableMain) {
								valid = false
								break
							}
						}
					}
					if valid {
						hasValidPosition = true
						break
					}
				}
			} else {
				hasValidPosition = true
			}

			if hasValidPosition {
				break
			}
		}

		if !hasValidPosition {
			return false, fmt.Sprintf("offering #%d (%s) has no valid position in any day", i+1, offering.CourseID)
		}
	}

	return true, ""
}

func isInRange(period int, ranges [][2]int) bool {
	for _, r := range ranges {
		if period >= r[0] && period <= r[1] {
			return true
		}
	}
	return false
}

func getInstructorUnavailableForDay(config *types.Config, instructorID string, day types.Day) [][2]int {
	var ranges [][2]int
	for _, iu := range config.InstructorUnavailable {
		if iu.InstructorID == instructorID && iu.Day == day {
			ranges = append(ranges, [2]int{iu.StartPeriod, iu.EndPeriod})
		}
	}
	return ranges
}

func getInstructorUnavailableMainForDay(config *types.Config, instructorID string, day types.Day) [][2]int {
	var ranges [][2]int
	for _, iu := range config.InstructorUnavailableMain {
		if iu.InstructorID == instructorID && iu.Day == day {
			ranges = append(ranges, [2]int{iu.StartPeriod, iu.EndPeriod})
		}
	}
	return ranges
}

// ExpandOfferingBlocks splits offerings into individual blocks (theory and lab).
// Returns a list of "schedule units" that need to be assigned.
func ExpandOfferingBlocks(offerings []*types.Offering) []*ScheduleUnit {
	var units []*ScheduleUnit
	for _, o := range offerings {
		if o.TheoryPeriods > 0 {
			units = append(units, &ScheduleUnit{
				Offering:    o,
				BlockType:   BlockTheory,
				NumPeriods:  o.TheoryPeriods,
				OriginalIdx: len(units),
			})
		}
		if o.LabPeriods > 0 {
			units = append(units, &ScheduleUnit{
				Offering:    o,
				BlockType:   BlockLab,
				NumPeriods:  o.LabPeriods,
				OriginalIdx: len(units),
			})
		}
	}
	return units
}

// BlockType represents the type of a schedule block.
type BlockType int

const (
	BlockTheory BlockType = iota
	BlockLab
)

// ScheduleUnit represents a single block (theory or lab) that needs to be scheduled.
type ScheduleUnit struct {
	Offering    *types.Offering
	BlockType   BlockType
	NumPeriods  int
	OriginalIdx int
}

// String returns a string representation of the schedule unit.
func (su *ScheduleUnit) String() string {
	bt := "theory"
	if su.BlockType == BlockLab {
		bt = "lab"
	}
	return fmt.Sprintf("%s[%s](%d periods)", su.Offering.CourseID, bt, su.NumPeriods)
}

// GetRoomIDs returns the allowed room IDs for this block type.
func (su *ScheduleUnit) GetRoomIDs() []string {
	if su.BlockType == BlockTheory {
		return su.Offering.TheoryRoomIDs
	}
	return su.Offering.LabRoomIDs
}

// GetOtherRoomIDs returns the allowed room IDs for the other block type.
func (su *ScheduleUnit) GetOtherRoomIDs() []string {
	if su.BlockType == BlockTheory {
		return su.Offering.LabRoomIDs
	}
	return su.Offering.TheoryRoomIDs
}

// GetInstructorIDs returns the instructor IDs involved in this block.
func (su *ScheduleUnit) GetInstructorIDs() []string {
	var ids []string
	if su.Offering.MainInstructorID != "x" {
		ids = append(ids, su.Offering.MainInstructorID)
	}
	if su.BlockType == BlockLab {
		ids = append(ids, su.Offering.CoInstructorIDs...)
	}
	return ids
}

// HasInstructor returns true if the instructor teaches this block.
func (su *ScheduleUnit) HasInstructor(instructorID string) bool {
	for _, id := range su.GetInstructorIDs() {
		if id == instructorID {
			return true
		}
	}
	return false
}

// GetRoomName returns the display name of a room ID.
func GetRoomName(config *types.Config, roomID string) string {
	if roomID == "" || roomID == "x" {
		return "x"
	}
	if r, ok := config.Rooms[roomID]; ok {
		return r.Name
	}
	return roomID
}

// GetGroupNames returns comma-separated group names for a list of group IDs.
func GetGroupNames(config *types.Config, groupIDs []string) string {
	names := make([]string, len(groupIDs))
	for i, gid := range groupIDs {
		if g, ok := config.Groups[gid]; ok {
			names[i] = g.Name
		} else {
			names[i] = gid
		}
	}
	return strings.Join(names, ",")
}

// GetInstructorName returns the name of an instructor.
func GetInstructorName(config *types.Config, instructorID string) string {
	if i, ok := config.Instructors[instructorID]; ok {
		return i.Name
	}
	return instructorID
}

// GetCourseName returns the name of a course.
func GetCourseName(config *types.Config, courseID string) string {
	if c, ok := config.Courses[courseID]; ok {
		return c.Name
	}
	return courseID
}
