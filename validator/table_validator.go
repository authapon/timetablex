// Package validator validates config data including pre-defined schedule from config.txt.
package validator

import (
	"fmt"

	"timetablex/types"
)

// PredefinedValidationError represents a validation error for pre-defined data.
type PredefinedValidationError struct {
	Index      int    // 0-based index in the predefined data list
	LineNumber int    // line number in config.txt (0 if unknown)
	Message    string // description of the error
}

func (e PredefinedValidationError) Error() string {
	if e.LineNumber > 0 {
		return fmt.Sprintf("config.txt line %d: %s", e.LineNumber, e.Message)
	}
	return fmt.Sprintf("config.txt entry #%d: %s", e.Index+1, e.Message)
}

// ValidatePredefined validates pre-defined assignment data from config.txt against the config.conf data.
// It checks:
//   - Cross-references: courses, groups, instructors, rooms must exist in config.conf
//   - Offering consistency: theory/lab periods must match the offering definition
//   - Term type restrictions: normal groups on weekdays, special groups on weekends
//   - Constraint violations: no conflicts with groups_unavailable, instructor_unavailable,
//     instructor_unavailable_main, instructor_nolate, or lunch breaks
//   - Internal conflicts: no double-booking of instructors, groups between pre-defined assignments
func ValidatePredefined(config *types.Config, data *types.PredefinedData) []PredefinedValidationError {
	var errs []PredefinedValidationError

	// Validate each predefined assignment
	for i, pa := range data.Assignments {
		// Check course exists
		if _, ok := config.Courses[pa.CourseID]; !ok {
			errs = append(errs, PredefinedValidationError{
				Index:      i,
				LineNumber: pa.LineNumber,
				Message:    fmt.Sprintf("course_id '%s' not found in [courses]", pa.CourseID),
			})
		}

		// Check groups exist
		for _, gid := range pa.GroupIDs {
			if _, ok := config.Groups[gid]; !ok {
				errs = append(errs, PredefinedValidationError{
					Index:      i,
					LineNumber: pa.LineNumber,
					Message:    fmt.Sprintf("group_id '%s' not found in [groups]", gid),
				})
			}
		}

		// Check main instructor exists
		if pa.MainInstructorID != "x" {
			if _, ok := config.Instructors[pa.MainInstructorID]; !ok {
				errs = append(errs, PredefinedValidationError{
					Index:      i,
					LineNumber: pa.LineNumber,
					Message:    fmt.Sprintf("main_instructor_id '%s' not found in [instructor]", pa.MainInstructorID),
				})
			}
		}

		// Check co-instructors exist
		for _, coID := range pa.CoInstructorIDs {
			if _, ok := config.Instructors[coID]; !ok {
				errs = append(errs, PredefinedValidationError{
					Index:      i,
					LineNumber: pa.LineNumber,
					Message:    fmt.Sprintf("co_instructor_id '%s' not found in [instructor]", coID),
				})
			}
		}

		// Check rooms exist (unless "$" or "x")
		if pa.TheoryRoomID != "$" && pa.TheoryRoomID != "x" && pa.TheoryRoomID != "" {
			if _, ok := config.Rooms[pa.TheoryRoomID]; !ok {
				errs = append(errs, PredefinedValidationError{
					Index:      i,
					LineNumber: pa.LineNumber,
					Message:    fmt.Sprintf("theory_room_id '%s' not found in [rooms]", pa.TheoryRoomID),
				})
			}
		}
		if pa.LabRoomID != "$" && pa.LabRoomID != "x" && pa.LabRoomID != "" {
			if _, ok := config.Rooms[pa.LabRoomID]; !ok {
				errs = append(errs, PredefinedValidationError{
					Index:      i,
					LineNumber: pa.LineNumber,
					Message:    fmt.Sprintf("lab_room_id '%s' not found in [rooms]", pa.LabRoomID),
				})
			}
		}

		// Check offering consistency: find matching offering and verify periods match
		matchedOffering := findMatchingOffering(config, pa)
		if matchedOffering != nil {
			if matchedOffering.TheoryPeriods != pa.TheoryPeriods {
				errs = append(errs, PredefinedValidationError{
					Index:      i,
					LineNumber: pa.LineNumber,
					Message: fmt.Sprintf("theory_periods mismatch for offering '%s' groups='%s' instructor='%s': predefined=%d, config.conf=%d",
						pa.CourseID, pa.GroupIDRaw, pa.MainInstructorID, pa.TheoryPeriods, matchedOffering.TheoryPeriods),
				})
			}
			if matchedOffering.LabPeriods != pa.LabPeriods {
				errs = append(errs, PredefinedValidationError{
					Index:      i,
					LineNumber: pa.LineNumber,
					Message: fmt.Sprintf("lab_periods mismatch for offering '%s' groups='%s' instructor='%s': predefined=%d, config.conf=%d",
						pa.CourseID, pa.GroupIDRaw, pa.MainInstructorID, pa.LabPeriods, matchedOffering.LabPeriods),
				})
			}
		} else {
			errs = append(errs, PredefinedValidationError{
				Index:      i,
				LineNumber: pa.LineNumber,
				Message: fmt.Sprintf("no matching offering found in config.conf for course='%s' groups='%s' instructor='%s'",
					pa.CourseID, pa.GroupIDRaw, pa.MainInstructorID),
			})
		}

		// Check term type restrictions
		isWeekend := pa.Day == types.Saturday || pa.Day == types.Sunday
		for _, gid := range pa.GroupIDs {
			if grp, ok := config.Groups[gid]; ok {
				if grp.TermType == types.Normal && isWeekend {
					errs = append(errs, PredefinedValidationError{
						Index:      i,
						LineNumber: pa.LineNumber,
						Message: fmt.Sprintf("group '%s' (normal term) cannot be scheduled on weekend (%s)", gid, pa.Day),
					})
				}
				if grp.TermType == types.Special && !isWeekend {
					errs = append(errs, PredefinedValidationError{
						Index:      i,
						LineNumber: pa.LineNumber,
						Message: fmt.Sprintf("group '%s' (special term) cannot be scheduled on weekday (%s)", gid, pa.Day),
					})
				}
			}
		}

		// Check lunch break conflict
		// The config may define multiple break periods in priority order (e.g., [5, 6]).
		// The scheduler will pick the first break period not occupied by any assignment.
		// So an assignment is only invalid if it covers ALL break periods on that day,
		// leaving no period available for lunch.
		if len(config.Breaks.Periods) > 0 {
			// Count how many break periods this assignment covers
			coveredBreakCount := 0
			for _, bp := range config.Breaks.Periods {
				if isInPredefinedPeriod(pa, bp) {
					coveredBreakCount++
				}
			}

			// Only flag an error if the assignment covers ALL defined break periods
			if coveredBreakCount == len(config.Breaks.Periods) {
				// All break periods are occupied — check if custom schedules explain it
				hasCustomSchedule := false
				lunchPeriod := config.Breaks.Periods[0]

				// --- Check GU entries in config.txt ---
				for _, gid := range pa.GroupIDs {
					for _, gu := range data.GroupsUnavailable {
						if gu.GroupID == gid && gu.Day == pa.Day {
							hasCustomSchedule = true
							break
						}
					}
					if hasCustomSchedule {
						break
					}
				}

				// --- Check GU entries in config.conf ---
				if !hasCustomSchedule {
					for _, gid := range pa.GroupIDs {
						for _, gu := range config.GroupsUnavailable {
							if gu.GroupID == gid && gu.Day == pa.Day &&
								lunchPeriod >= gu.StartPeriod && lunchPeriod <= gu.EndPeriod {
								hasCustomSchedule = true
								break
							}
						}
						if hasCustomSchedule {
							break
						}
					}
				}

				// --- Check IU entries in config.txt ---
				if !hasCustomSchedule && pa.MainInstructorID != "x" {
					for _, iu := range data.InstructorUnavailable {
						if iu.InstructorID == pa.MainInstructorID && iu.Day == pa.Day {
							hasCustomSchedule = true
							break
						}
					}
				}

				// --- Check IU entries in config.conf ---
				if !hasCustomSchedule && pa.MainInstructorID != "x" {
					for _, iu := range config.InstructorUnavailable {
						if iu.InstructorID == pa.MainInstructorID && iu.Day == pa.Day &&
							lunchPeriod >= iu.StartPeriod && lunchPeriod <= iu.EndPeriod {
							hasCustomSchedule = true
							break
						}
					}
				}

				// --- Check co-instructor IU entries in config.txt ---
				if !hasCustomSchedule {
					for _, coID := range pa.CoInstructorIDs {
						for _, iu := range data.InstructorUnavailable {
							if iu.InstructorID == coID && iu.Day == pa.Day {
								hasCustomSchedule = true
								break
							}
						}
						if hasCustomSchedule {
							break
						}
					}
				}

				// --- Check co-instructor IU entries in config.conf ---
				if !hasCustomSchedule {
					for _, coID := range pa.CoInstructorIDs {
						for _, iu := range config.InstructorUnavailable {
							if iu.InstructorID == coID && iu.Day == pa.Day &&
								lunchPeriod >= iu.StartPeriod && lunchPeriod <= iu.EndPeriod {
								hasCustomSchedule = true
								break
							}
						}
						if hasCustomSchedule {
							break
						}
					}
				}

				if !hasCustomSchedule {
					errs = append(errs, PredefinedValidationError{
						Index:      i,
						LineNumber: pa.LineNumber,
						Message: fmt.Sprintf("assignment covers all break periods (%v) on %s, leaving no lunch break available", config.Breaks.Periods, pa.Day),
					})
				}
			}
		}

		// Check instructor unavailable constraints
		if pa.MainInstructorID != "x" {
			for p := 1; p <= types.MaxPeriodsPerDay; p++ {
				if !isInPredefinedPeriod(pa, p) {
					continue
				}

				// Check full unavailable
				for _, iu := range config.InstructorUnavailable {
					if iu.InstructorID == pa.MainInstructorID && iu.Day == pa.Day &&
						p >= iu.StartPeriod && p <= iu.EndPeriod {
						errs = append(errs, PredefinedValidationError{
							Index:      i,
							LineNumber: pa.LineNumber,
							Message: fmt.Sprintf("instructor '%s' is unavailable on %s period %d (see config.conf instructor_unavailable)", pa.MainInstructorID, pa.Day, p),
						})
					}
				}

				// Check unavailable as main (theory periods only)
				isTheory := pa.TheoryStart > 0 && p >= pa.TheoryStart && p < pa.TheoryStart+pa.TheoryPeriods
				if isTheory {
					for _, ium := range config.InstructorUnavailableMain {
						if ium.InstructorID == pa.MainInstructorID && ium.Day == pa.Day &&
							p >= ium.StartPeriod && p <= ium.EndPeriod {
							errs = append(errs, PredefinedValidationError{
								Index:      i,
								LineNumber: pa.LineNumber,
								Message: fmt.Sprintf("instructor '%s' is unavailable as main instructor on %s period %d (see config.conf instructor_unavailable_main)", pa.MainInstructorID, pa.Day, p),
							})
						}
					}
				}
			}

			// Check instructor nolate
			for _, inl := range config.InstructorNoLate {
				if inl.InstructorID == pa.MainInstructorID && inl.Day == pa.Day {
					startPeriod := pa.TheoryStart
					if startPeriod == 0 {
						startPeriod = pa.LabStart
					}
					totalPeriods := pa.TheoryPeriods + pa.LabPeriods
					endPeriod := startPeriod + totalPeriods - 1
					if endPeriod >= inl.PeriodThreshold {
						errs = append(errs, PredefinedValidationError{
							Index:      i,
							LineNumber: pa.LineNumber,
							Message: fmt.Sprintf("instructor '%s' cannot teach from period %d onwards on %s (nolate constraint from config.conf)", pa.MainInstructorID, inl.PeriodThreshold, pa.Day),
						})
					}
				}
			}
		}

		// Check group unavailable constraints
		for _, gid := range pa.GroupIDs {
			for p := 1; p <= types.MaxPeriodsPerDay; p++ {
				if !isInPredefinedPeriod(pa, p) {
					continue
				}
				for _, gu := range config.GroupsUnavailable {
					if gu.GroupID == gid && gu.Day == pa.Day &&
						p >= gu.StartPeriod && p <= gu.EndPeriod {
						errs = append(errs, PredefinedValidationError{
							Index:      i,
							LineNumber: pa.LineNumber,
							Message: fmt.Sprintf("group '%s' is unavailable on %s period %d (see config.conf groups_unavailable)", gid, pa.Day, p),
						})
					}
				}
			}
		}

		// Check room conflicts with GroupsUnavailable entries (from config.conf and config.txt)
		// that specify a room ID (not "none"). These entries reserve the room for that period.
		for p := 1; p <= types.MaxPeriodsPerDay; p++ {
			if !isInPredefinedPeriod(pa, p) {
				continue
			}
			roomID := getPredefinedRoomForPeriod(pa, p)
			if roomID == "" || roomID == "x" || roomID == "$" {
				continue
			}

			// Check config.conf GU entries
			for _, gu := range config.GroupsUnavailable {
				if gu.RoomID == "" || gu.RoomID == "none" {
					continue
				}
				if gu.RoomID == roomID && gu.Day == pa.Day &&
					p >= gu.StartPeriod && p <= gu.EndPeriod {
					errs = append(errs, PredefinedValidationError{
						Index:      i,
						LineNumber: pa.LineNumber,
						Message: fmt.Sprintf("room '%s' conflict: reserved by groups_unavailable (group '%s') on %s period %d (config.conf)", roomID, gu.GroupID, pa.Day, p),
					})
				}
			}

			// Check config.txt GU entries
			for _, gu := range data.GroupsUnavailable {
				if gu.RoomID == "" || gu.RoomID == "none" {
					continue
				}
				if gu.RoomID == roomID && gu.Day == pa.Day &&
					p >= gu.StartPeriod && p <= gu.EndPeriod {
					errs = append(errs, PredefinedValidationError{
						Index:      i,
						LineNumber: pa.LineNumber,
						Message: fmt.Sprintf("room '%s' conflict: reserved by groups_unavailable (group '%s') on %s period %d (config.txt)", roomID, gu.GroupID, pa.Day, p),
					})
				}
			}
		}

		// Check room conflicts with InstructorUnavailable entries (from config.conf and config.txt)
		// that specify a room ID (not "none"). These entries reserve the room for that period.
		for p := 1; p <= types.MaxPeriodsPerDay; p++ {
			if !isInPredefinedPeriod(pa, p) {
				continue
			}
			roomID := getPredefinedRoomForPeriod(pa, p)
			if roomID == "" || roomID == "x" || roomID == "$" {
				continue
			}

			// Check config.conf IU entries
			for _, iu := range config.InstructorUnavailable {
				if iu.RoomID == "" || iu.RoomID == "none" {
					continue
				}
				if iu.RoomID == roomID && iu.Day == pa.Day &&
					p >= iu.StartPeriod && p <= iu.EndPeriod {
					errs = append(errs, PredefinedValidationError{
						Index:      i,
						LineNumber: pa.LineNumber,
						Message: fmt.Sprintf("room '%s' conflict: reserved by instructor_unavailable (instructor '%s') on %s period %d (config.conf)", roomID, iu.InstructorID, pa.Day, p),
					})
				}
			}

			// Check config.txt IU entries
			for _, iu := range data.InstructorUnavailable {
				if iu.RoomID == "" || iu.RoomID == "none" {
					continue
				}
				if iu.RoomID == roomID && iu.Day == pa.Day &&
					p >= iu.StartPeriod && p <= iu.EndPeriod {
					errs = append(errs, PredefinedValidationError{
						Index:      i,
						LineNumber: pa.LineNumber,
						Message: fmt.Sprintf("room '%s' conflict: reserved by instructor_unavailable (instructor '%s') on %s period %d (config.txt)", roomID, iu.InstructorID, pa.Day, p),
					})
				}
			}
		}
	}

	// Check conflicts BETWEEN pre-defined assignments (instructor/group/room double-booking)
	for i, a := range data.Assignments {
		for j, b := range data.Assignments {
			if i >= j {
				continue
			}
			if a.Day != b.Day {
				continue
			}

			for p := 1; p <= types.MaxPeriodsPerDay; p++ {
				if !isInPredefinedPeriod(a, p) || !isInPredefinedPeriod(b, p) {
					continue
				}

				// Check instructor overlap (main vs main)
				if a.MainInstructorID != "x" && b.MainInstructorID != "x" && a.MainInstructorID == b.MainInstructorID {
					errs = append(errs, PredefinedValidationError{
						Index:      i,
						LineNumber: a.LineNumber,
						Message: fmt.Sprintf("internal conflict: instructor '%s' double-booked with line %d on %s period %d", a.MainInstructorID, b.LineNumber, a.Day, p),
					})
					continue
				}

				// Check main vs co overlap
				isLabA := a.LabStart > 0 && p >= a.LabStart
				isLabB := b.LabStart > 0 && p >= b.LabStart
				if isLabB {
					for _, coA := range a.CoInstructorIDs {
						if coA == b.MainInstructorID {
							errs = append(errs, PredefinedValidationError{
								Index:      i,
								LineNumber: a.LineNumber,
								Message: fmt.Sprintf("internal conflict: instructor '%s' double-booked (co vs main) with line %d on %s period %d", coA, b.LineNumber, a.Day, p),
							})
						}
					}
				}
				if isLabA {
					for _, coB := range b.CoInstructorIDs {
						if coB == a.MainInstructorID {
							errs = append(errs, PredefinedValidationError{
								Index:      i,
								LineNumber: a.LineNumber,
								Message: fmt.Sprintf("internal conflict: instructor '%s' double-booked (main vs co) with line %d on %s period %d", coB, b.LineNumber, a.Day, p),
							})
						}
					}
				}

				// Check co vs co overlap (both must be in lab periods)
				if isLabA && isLabB {
					for _, coA := range a.CoInstructorIDs {
						for _, coB := range b.CoInstructorIDs {
							if coA == coB {
								errs = append(errs, PredefinedValidationError{
									Index:      i,
									LineNumber: a.LineNumber,
									Message: fmt.Sprintf("internal conflict: instructor '%s' double-booked (co-co) with line %d on %s period %d", coA, b.LineNumber, a.Day, p),
								})
							}
						}
					}
				}

				// Check group overlap
				for _, gidA := range a.GroupIDs {
					for _, gidB := range b.GroupIDs {
						if gidA == gidB {
							errs = append(errs, PredefinedValidationError{
								Index:      i,
								LineNumber: a.LineNumber,
								Message: fmt.Sprintf("internal conflict: group '%s' double-booked with line %d on %s period %d", gidA, b.LineNumber, a.Day, p),
							})
						}
					}
				}

				// Check room overlap (only if both have specific rooms, not "$" or "x")
				roomA := getPredefinedRoomForPeriod(a, p)
				roomB := getPredefinedRoomForPeriod(b, p)
				if roomA != "" && roomA != "x" && roomA != "$" &&
					roomB != "" && roomB != "x" && roomB != "$" &&
					roomA == roomB {
					errs = append(errs, PredefinedValidationError{
						Index:      i,
						LineNumber: a.LineNumber,
						Message: fmt.Sprintf("internal conflict: room '%s' double-booked with line %d on %s period %d", roomA, b.LineNumber, a.Day, p),
					})
				}
			}
		}
	}

	// Validate GU and IU entries from config.txt
	for i, gu := range data.GroupsUnavailable {
		if _, ok := config.Groups[gu.GroupID]; !ok {
			errs = append(errs, PredefinedValidationError{
				Index:   i,
				Message: fmt.Sprintf("GU group_id '%s' not found in [groups]", gu.GroupID),
			})
		}
		if gu.RoomID != "none" && gu.RoomID != "" {
			if _, ok := config.Rooms[gu.RoomID]; !ok {
				errs = append(errs, PredefinedValidationError{
					Index:   i,
					Message: fmt.Sprintf("GU room_id '%s' not found in [rooms]", gu.RoomID),
				})
			}
		}
	}

	for i, iu := range data.InstructorUnavailable {
		if _, ok := config.Instructors[iu.InstructorID]; !ok {
			errs = append(errs, PredefinedValidationError{
				Index:   i,
				Message: fmt.Sprintf("IU instructor_id '%s' not found in [instructor]", iu.InstructorID),
			})
		}
		if iu.RoomID != "none" && iu.RoomID != "" {
			if _, ok := config.Rooms[iu.RoomID]; !ok {
				errs = append(errs, PredefinedValidationError{
					Index:   i,
					Message: fmt.Sprintf("IU room_id '%s' not found in [rooms]", iu.RoomID),
				})
			}
		}
	}

	return errs
}

// findMatchingOffering finds an offering in config that matches the predefined assignment.
func findMatchingOffering(config *types.Config, pa *types.PredefinedAssignment) *types.Offering {
	for _, offering := range config.Offerings {
		if offering.CourseID == pa.CourseID &&
			offering.GroupIDRaw == pa.GroupIDRaw &&
			offering.MainInstructorID == pa.MainInstructorID {
			return offering
		}
	}
	return nil
}

// isInPredefinedPeriod checks if the given period is covered by the predefined assignment.
func isInPredefinedPeriod(pa *types.PredefinedAssignment, period int) bool {
	if pa.TheoryStart > 0 && period >= pa.TheoryStart && period < pa.TheoryStart+pa.TheoryPeriods {
		return true
	}
	if pa.LabStart > 0 && period >= pa.LabStart && period < pa.LabStart+pa.LabPeriods {
		return true
	}
	return false
}

// getPredefinedRoomForPeriod returns the room ID used by a predefined assignment for the given period.
func getPredefinedRoomForPeriod(pa *types.PredefinedAssignment, period int) string {
	if pa.TheoryStart > 0 && period >= pa.TheoryStart && period < pa.TheoryStart+pa.TheoryPeriods {
		return pa.TheoryRoomID
	}
	if pa.LabStart > 0 && period >= pa.LabStart && period < pa.LabStart+pa.LabPeriods {
		return pa.LabRoomID
	}
	return ""
}
