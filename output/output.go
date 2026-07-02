// Package output generates the timetable.md output file.
package output

import (
	"fmt"
	"sort"
	"strings"

	"timetablex/types"
)

// TimetableRenderer renders schedules into markdown tables.
type TimetableRenderer struct {
	config   *types.Config
	schedule *types.Schedule
}

// NewRenderer creates a new timetable renderer.
func NewRenderer(config *types.Config, schedule *types.Schedule) *TimetableRenderer {
	return &TimetableRenderer{
		config:   config,
		schedule: schedule,
	}
}

// Render generates the complete timetable.md content.
func (r *TimetableRenderer) Render() string {
	var sb strings.Builder

	sb.WriteString("# Timetable (ตารางสอน)\n\n")
	sb.WriteString("---\n\n")

	// Section 1: Instructor timetables
	sb.WriteString("## ตารางสอนรายอาจารย์ (Instructor Timetable)\n\n")
	r.renderInstructorTimetables(&sb)

	// Section 2: Group timetables
	sb.WriteString("## ตารางสอนรายกลุ่มเรียน (Group Timetable)\n\n")
	r.renderGroupTimetables(&sb)

	// Section 3: Room timetables
	sb.WriteString("## ตารางสอนรายห้องเรียน (Room Timetable)\n\n")
	r.renderRoomTimetables(&sb)

	return sb.String()
}

// GetAssignmentsForInstructorOnDay returns assignments for a specific instructor on a specific day.
func (r *TimetableRenderer) GetAssignmentsForInstructorOnDay(instructorID string, day types.Day) []*types.Assignment {
	var result []*types.Assignment
	for _, a := range r.schedule.Assignments {
		if a.Day != day {
			continue
		}
		offering := a.Offering
		if offering.MainInstructorID == instructorID {
			result = append(result, a)
			continue
		}
		for _, coID := range offering.CoInstructorIDs {
			if coID == instructorID {
				result = append(result, a)
				break
			}
		}
	}
	return result
}

// GetAssignmentsForGroupOnDay returns assignments for a specific group on a specific day.
func (r *TimetableRenderer) GetAssignmentsForGroupOnDay(groupID string, day types.Day) []*types.Assignment {
	var result []*types.Assignment
	for _, a := range r.schedule.Assignments {
		if a.Day != day {
			continue
		}
		for _, gid := range a.Offering.GroupIDs {
			if gid == groupID {
				result = append(result, a)
				break
			}
		}
	}
	return result
}

// GetAssignmentsForRoomOnDay returns assignments for a specific room on a specific day.
func (r *TimetableRenderer) GetAssignmentsForRoomOnDay(roomID string, day types.Day) []*types.Assignment {
	var result []*types.Assignment
	for _, a := range r.schedule.Assignments {
		if a.Day != day {
			continue
		}
		if a.TheoryRoomID == roomID || a.LabRoomID == roomID {
			result = append(result, a)
		}
	}
	return result
}

// getInstructorStatusOnPeriod determines if an instructor is main or co for a period.
func (r *TimetableRenderer) getInstructorStatusOnPeriod(a *types.Assignment, instructorID string, period int) string {
	if a.IsTheoryPeriod(period) {
		if a.Offering.MainInstructorID == instructorID {
			return "main"
		}
		return ""
	}
	if a.IsLabPeriod(period) {
		if a.Offering.MainInstructorID == instructorID {
			return "main"
		}
		for _, coID := range a.Offering.CoInstructorIDs {
			if coID == instructorID {
				return "co"
			}
		}
	}
	return ""
}

// renderInstructorTimetables renders all instructor timetables.
func (r *TimetableRenderer) renderInstructorTimetables(sb *strings.Builder) {
	// Get instructors in order
	type instructorEntry struct {
		ID   string
		Name string
	}
	var instructors []instructorEntry
	for id, inst := range r.config.Instructors {
		instructors = append(instructors, instructorEntry{ID: id, Name: inst.Name})
	}
	sort.Slice(instructors, func(i, j int) bool {
		return instructors[i].ID < instructors[j].ID
	})

	for _, inst := range instructors {
		sb.WriteString(fmt.Sprintf("### อาจารย์ %s (%s)\n\n", inst.Name, inst.ID))

		// Collect all days where this instructor has activity or unavailable entries
		activeDays := r.getInstructorActiveDays(inst.ID)

		if len(activeDays) == 0 {
			sb.WriteString("ไม่มีการสอน\n\n")
			continue
		}

		sb.WriteString("| วัน | คาบที่ | วิชา | ประเภทคาบ | กลุ่มเรียน | ห้องเรียน |\n")
		sb.WriteString("|-----|--------|------|-----------|-----------|----------|\n")

		// Order days
		orderedDays := r.orderDays(activeDays)

		for _, day := range orderedDays {
			dayName := types.DayFullName[day]
			// Get periods for this instructor on this day
			periods := r.getInstructorPeriods(inst.ID, day)

			// Check for unavailable entries with messages
			unavailableMsgs := r.getInstructorUnavailableMessages(inst.ID, day)

			// Merge periods and unavailable messages
			allPeriods := r.mergeInstructorPeriods(periods, unavailableMsgs, inst.ID, day)

			for _, pe := range allPeriods {
				if pe.periodStart == pe.periodEnd {
					sb.WriteString(fmt.Sprintf("| %s | %d | %s | %s | %s | %s |\n",
						dayName, pe.periodStart, pe.subject, pe.periodType, pe.groups, pe.room))
				} else {
					sb.WriteString(fmt.Sprintf("| %s | %d-%d | %s | %s | %s | %s |\n",
						dayName, pe.periodStart, pe.periodEnd, pe.subject, pe.periodType, pe.groups, pe.room))
				}
			}
		}

		sb.WriteString("\n")
	}
}

// renderGroupTimetables renders all group timetables.
func (r *TimetableRenderer) renderGroupTimetables(sb *strings.Builder) {
	var groups []struct {
		ID   string
		Name string
	}
	for id, grp := range r.config.Groups {
		groups = append(groups, struct {
			ID   string
			Name string
		}{ID: id, Name: grp.Name})
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].ID < groups[j].ID
	})

	for _, grp := range groups {
		sb.WriteString(fmt.Sprintf("### กลุ่มเรียน %s (%s)\n\n", grp.Name, grp.ID))

		activeDays := r.getGroupActiveDays(grp.ID)

		if len(activeDays) == 0 {
			sb.WriteString("ไม่มีการเรียน\n\n")
			continue
		}

		sb.WriteString("| วัน | คาบที่ | วิชา | ประเภทคาบ | อาจารย์ | ห้องเรียน |\n")
		sb.WriteString("|-----|--------|------|-----------|---------|----------|\n")

		// Check for unavailable entries with hidden/message mode
		unavailableMap := make(map[types.Day][]*periodEntry)

		for _, gu := range r.config.GroupsUnavailable {
			if gu.GroupID != grp.ID {
				continue
			}
			if gu.Mode == types.ModeHidden {
				// Don't add any entry - skip this period entirely
				continue
			}
			// ModeMessage
			roomDisplay := "x"
			if gu.RoomID != "none" && gu.RoomID != "" {
				if rm, ok := r.config.Rooms[gu.RoomID]; ok {
					roomDisplay = rm.Name
				} else {
					roomDisplay = gu.RoomID
				}
			}
			for p := gu.StartPeriod; p <= gu.EndPeriod; p++ {
				unavailableMap[gu.Day] = append(unavailableMap[gu.Day], &periodEntry{
					periodStart: p,
					periodEnd:   p,
					subject:     gu.Message,
					room:        roomDisplay,
				})
			}
		}

		orderedDays := r.orderDays(activeDays)

		for _, day := range orderedDays {
			dayName := types.DayFullName[day]
			assignments := r.GetAssignmentsForGroupOnDay(grp.ID, day)
			unavailable := unavailableMap[day]

			// Build period entries for this day
			periodEntries := r.buildGroupPeriodEntries(grp.ID, day, assignments, unavailable)

			for _, pe := range periodEntries {
				if pe.periodStart == pe.periodEnd {
					sb.WriteString(fmt.Sprintf("| %s | %d | %s | %s | %s | %s |\n",
						dayName, pe.periodStart, pe.subject, pe.periodType, pe.groups, pe.room))
				} else {
					sb.WriteString(fmt.Sprintf("| %s | %d-%d | %s | %s | %s | %s |\n",
						dayName, pe.periodStart, pe.periodEnd, pe.subject, pe.periodType, pe.groups, pe.room))
				}
			}
		}

		sb.WriteString("\n")
	}
}

// renderRoomTimetables renders all room timetables.
func (r *TimetableRenderer) renderRoomTimetables(sb *strings.Builder) {
	var rooms []struct {
		ID   string
		Name string
	}
	for id, rm := range r.config.Rooms {
		rooms = append(rooms, struct {
			ID   string
			Name string
		}{ID: id, Name: rm.Name})
	}
	sort.Slice(rooms, func(i, j int) bool {
		return rooms[i].ID < rooms[j].ID
	})

	for _, rm := range rooms {
		sb.WriteString(fmt.Sprintf("### ห้อง %s (%s)\n\n", rm.Name, rm.ID))

		activeDays := r.getRoomActiveDays(rm.ID)

		if len(activeDays) == 0 {
			sb.WriteString("ไม่มีการใช้งาน\n\n")
			continue
		}

		sb.WriteString("| วัน | คาบที่ | วิชา | ประเภทคาบ | กลุ่มเรียน | อาจารย์ |\n")
		sb.WriteString("|-----|--------|------|-----------|-----------|---------|\n")

		orderedDays := r.orderDays(activeDays)

		for _, day := range orderedDays {
			dayName := types.DayFullName[day]
			periodEntries := r.buildRoomPeriodEntries(rm.ID, day)

			for _, pe := range periodEntries {
				if pe.periodStart == pe.periodEnd {
					sb.WriteString(fmt.Sprintf("| %s | %d | %s | %s | %s | %s |\n",
						dayName, pe.periodStart, pe.subject, pe.periodType, pe.groups, pe.room))
				} else {
					sb.WriteString(fmt.Sprintf("| %s | %d-%d | %s | %s | %s | %s |\n",
						dayName, pe.periodStart, pe.periodEnd, pe.subject, pe.periodType, pe.groups, pe.room))
				}
			}
		}

		sb.WriteString("\n")
	}
}

// periodEntry represents a single row in a timetable.
type periodEntry struct {
	periodStart int
	periodEnd   int
	subject     string
	periodType  string
	groups      string // Instructor or group names
	room        string // Room or instructor names
}

// getInstructorActiveDays returns days where an instructor has activity.
func (r *TimetableRenderer) getInstructorActiveDays(instructorID string) map[types.Day]bool {
	days := make(map[types.Day]bool)
	for _, a := range r.schedule.Assignments {
		offering := a.Offering
		if offering.MainInstructorID == instructorID {
			days[a.Day] = true
		}
		for _, coID := range offering.CoInstructorIDs {
			if coID == instructorID {
				days[a.Day] = true
			}
		}
	}
	// Also check unavailable entries
	for _, iu := range r.config.InstructorUnavailable {
		if iu.InstructorID == instructorID {
			days[iu.Day] = true
		}
	}
	return days
}

// getGroupActiveDays returns days where a group has activity.
func (r *TimetableRenderer) getGroupActiveDays(groupID string) map[types.Day]bool {
	days := make(map[types.Day]bool)
	for _, a := range r.schedule.Assignments {
		for _, gid := range a.Offering.GroupIDs {
			if gid == groupID {
				days[a.Day] = true
			}
		}
	}
	// Check unavailable entries
	for _, gu := range r.config.GroupsUnavailable {
		if gu.GroupID == groupID {
			days[gu.Day] = true
		}
	}
	return days
}

// getRoomActiveDays returns days where a room has activity.
func (r *TimetableRenderer) getRoomActiveDays(roomID string) map[types.Day]bool {
	days := make(map[types.Day]bool)
	for _, a := range r.schedule.Assignments {
		if a.TheoryRoomID == roomID || a.LabRoomID == roomID {
			days[a.Day] = true
		}
	}
	// Also check instructor_unavailable entries that use this room
	for _, iu := range r.config.InstructorUnavailable {
		if iu.RoomID == roomID {
			days[iu.Day] = true
		}
	}
	// Also check groups_unavailable entries that use this room
	for _, gu := range r.config.GroupsUnavailable {
		if gu.RoomID == roomID {
			days[gu.Day] = true
		}
	}
	return days
}

// orderDays orders days chronologically.
func (r *TimetableRenderer) orderDays(days map[types.Day]bool) []types.Day {
	allDays := types.AllDays()
	var ordered []types.Day
	for _, d := range allDays {
		if days[d] {
			ordered = append(ordered, d)
		}
	}
	return ordered
}

// getInstructorPeriods returns all period entries for an instructor on a day.
func (r *TimetableRenderer) getInstructorPeriods(instructorID string, day types.Day) []*periodEntry {
	var entries []*periodEntry

	for _, a := range r.schedule.Assignments {
		if a.Day != day {
			continue
		}

		offering := a.Offering
		isMain := offering.MainInstructorID == instructorID
		isCo := false
		for _, coID := range offering.CoInstructorIDs {
			if coID == instructorID {
				isCo = true
				break
			}
		}

		if !isMain && !isCo {
			continue
		}

		courseName := ""
		if c, ok := r.config.Courses[offering.CourseID]; ok {
			courseName = c.Name
		} else {
			courseName = offering.CourseID
		}

		if offering.TheoryPeriods > 0 && isMain {
			periodEnd := a.TheoryStart + offering.TheoryPeriods - 1

			subjectDisplay := courseName + " (หลัก)"

			groupNames := r.getGroupNames(offering.GroupIDs)
			roomName := "x"
			if a.TheoryRoomID != "" && a.TheoryRoomID != "x" {
				if rm, ok := r.config.Rooms[a.TheoryRoomID]; ok {
					roomName = rm.Name
				}
			}

			entries = append(entries, &periodEntry{
				periodStart: a.TheoryStart,
				periodEnd:   periodEnd,
				subject:     subjectDisplay,
				periodType:  "ทฤษฎี",
				groups:      groupNames,
				room:        roomName,
			})
		}

		if offering.LabPeriods > 0 {
			periodEnd := a.LabStart + offering.LabPeriods - 1

			subjectDisplay := courseName
			if isMain {
				subjectDisplay += " (หลัก)"
			} else if isCo {
				subjectDisplay += " (ร่วม)"
			}

			groupNames := r.getGroupNames(offering.GroupIDs)
			roomName := "x"
			if a.LabRoomID != "" && a.LabRoomID != "x" {
				if rm, ok := r.config.Rooms[a.LabRoomID]; ok {
					roomName = rm.Name
				}
			}

			entries = append(entries, &periodEntry{
				periodStart: a.LabStart,
				periodEnd:   periodEnd,
				subject:     subjectDisplay,
				periodType:  "ปฏิบัติ",
				groups:      groupNames,
				room:        roomName,
			})
		}
	}

	return entries
}

// getInstructorUnavailableMessages returns period entries for instructor unavailable with messages.
func (r *TimetableRenderer) getInstructorUnavailableMessages(instructorID string, day types.Day) []*periodEntry {
	var entries []*periodEntry
	for _, iu := range r.config.InstructorUnavailable {
		if iu.InstructorID != instructorID || iu.Day != day {
			continue
		}
		if iu.Mode == types.ModeHidden {
			continue // Don't show
		}
		roomDisplay := "x"
		if iu.RoomID != "none" && iu.RoomID != "" {
			if rm, ok := r.config.Rooms[iu.RoomID]; ok {
				roomDisplay = rm.Name
			} else {
				roomDisplay = iu.RoomID
			}
		}
		entries = append(entries, &periodEntry{
			periodStart: iu.StartPeriod,
			periodEnd:   iu.EndPeriod,
			subject:     iu.Message,
			periodType:  "x",
			groups:      "x",
			room:        roomDisplay,
		})
	}
	return entries
}

// mergeInstructorPeriods merges assignments with unavailable entries and lunch breaks.
func (r *TimetableRenderer) mergeInstructorPeriods(periods []*periodEntry, unavailable []*periodEntry, instructorID string, day types.Day) []*periodEntry {
	// Build a map of period -> entry
	periodMap := make(map[int]*periodEntry)

	// Add unavailable entries
	for _, ue := range unavailable {
		for p := ue.periodStart; p <= ue.periodEnd; p++ {
			periodMap[p] = ue
		}
	}

	// Add assignment periods
	for _, pe := range periods {
		for p := pe.periodStart; p <= pe.periodEnd; p++ {
			periodMap[p] = pe
		}
	}

	// Add lunch break entry (overrides unavailable entries for the lunch period)
	// Exceptions: do not force lunch break if the day has full-day (1-13) instructor_unavailable
	if r.schedule != nil {
		if lp, ok := r.schedule.LunchBreakDay[day]; ok && lp > 0 {
			hasFullDayUnavailable := false
			for _, iu := range r.config.InstructorUnavailable {
				if iu.InstructorID == instructorID && iu.Day == day && iu.StartPeriod == 1 && iu.EndPeriod == 13 {
					hasFullDayUnavailable = true
					break
				}
			}
			if !hasFullDayUnavailable {
				periodMap[lp] = &periodEntry{
					periodStart: lp,
					periodEnd:   lp,
					subject:     "พักเที่ยง",
					periodType:  "x",
					groups:      "x",
					room:        "x",
				}
			}
		}
	}

	// Merge consecutive same entries
	var result []*periodEntry

	// Get sorted periods
	var sortedPeriods []int
	for p := 1; p <= types.MaxPeriodsPerDay; p++ {
		if _, ok := periodMap[p]; ok {
			sortedPeriods = append(sortedPeriods, p)
		}
	}

	i := 0
	for i < len(sortedPeriods) {
		p := sortedPeriods[i]
		pe := periodMap[p]

		// Find the end of this consecutive block
		j := i
		for j+1 < len(sortedPeriods) && sortedPeriods[j+1] == sortedPeriods[j]+1 {
			nextPE := periodMap[sortedPeriods[j+1]]
			if nextPE == pe {
				j++
			} else {
				break
			}
		}

		resultEntry := &periodEntry{
			periodStart: pe.periodStart,
			periodEnd:   pe.periodEnd,
			subject:     pe.subject,
			periodType:  pe.periodType,
			groups:      pe.groups,
			room:        pe.room,
		}
		resultEntry.periodStart = p
		resultEntry.periodEnd = sortedPeriods[j]

		result = append(result, resultEntry)
		i = j + 1
	}

	return result
}

// buildGroupPeriodEntries builds period entries for a group on a specific day.
func (r *TimetableRenderer) buildGroupPeriodEntries(groupID string, day types.Day, assignments []*types.Assignment, unavailable []*periodEntry) []*periodEntry {
	periodMap := make(map[int]*periodEntry)

	// Unavailable entries (with messages)
	for _, ue := range unavailable {
		for p := ue.periodStart; p <= ue.periodEnd; p++ {
			periodMap[p] = ue
		}
	}

	// Assignments
	for _, a := range assignments {
		offering := a.Offering
		courseName := r.getCourseName(offering.CourseID)

		if offering.TheoryPeriods > 0 {
			periodEnd := a.TheoryStart + offering.TheoryPeriods - 1
			instructorStr := r.getMainInstructorDisplay(offering.MainInstructorID)
			roomName := "x"
			if a.TheoryRoomID != "" && a.TheoryRoomID != "x" {
				if rm, ok := r.config.Rooms[a.TheoryRoomID]; ok {
					roomName = rm.Name
				}
			}

			for p := a.TheoryStart; p <= periodEnd; p++ {
				if existing, ok := periodMap[p]; ok && existing.subject != "พักเที่ยง" {
					// Skip if already filled
				}
				periodMap[p] = &periodEntry{
					periodStart: p,
					periodEnd:   p,
					subject:     courseName,
					periodType:  "ทฤษฎี",
					groups:      instructorStr,
					room:        roomName,
				}
			}
		}

		if offering.LabPeriods > 0 {
			periodEnd := a.LabStart + offering.LabPeriods - 1
			instructorStr := r.getInstructorDisplay(offering.MainInstructorID, offering.CoInstructorIDs)
			roomName := "x"
			if a.LabRoomID != "" && a.LabRoomID != "x" {
				if rm, ok := r.config.Rooms[a.LabRoomID]; ok {
					roomName = rm.Name
				}
			}

			for p := a.LabStart; p <= periodEnd; p++ {
				periodMap[p] = &periodEntry{
					periodStart: p,
					periodEnd:   p,
					subject:     courseName,
					periodType:  "ปฏิบัติ",
					groups:      instructorStr,
					room:        roomName,
				}
			}
		}
	}

	// Add lunch break entry (overrides unavailable entries for the lunch period)
	// Exceptions: do not force lunch break if the day has full-day (1-13) groups_unavailable
	if r.schedule != nil {
		if lp, ok := r.schedule.LunchBreakDay[day]; ok && lp > 0 {
			hasFullDayUnavailable := false
			for _, gu := range r.config.GroupsUnavailable {
				if gu.GroupID == groupID && gu.Day == day && gu.StartPeriod == 1 && gu.EndPeriod == 13 {
					hasFullDayUnavailable = true
					break
				}
			}
			if !hasFullDayUnavailable {
				periodMap[lp] = &periodEntry{
					periodStart: lp,
					periodEnd:   lp,
					subject:     "พักเที่ยง",
					periodType:  "x",
					groups:      "x",
					room:        "x",
				}
			}
		}
	}

	// Merge consecutive periods
	var result []*periodEntry
	var sortedPeriods []int
	for p := 1; p <= types.MaxPeriodsPerDay; p++ {
		if _, ok := periodMap[p]; ok {
			sortedPeriods = append(sortedPeriods, p)
		}
	}

	i := 0
	for i < len(sortedPeriods) {
		p := sortedPeriods[i]
		pe := periodMap[p]

		j := i
		for j+1 < len(sortedPeriods) && sortedPeriods[j+1] == sortedPeriods[j]+1 {
			nextPE := periodMap[sortedPeriods[j+1]]
			if nextPE.subject == pe.subject && nextPE.periodType == pe.periodType &&
				nextPE.groups == pe.groups && nextPE.room == pe.room {
				j++
			} else {
				break
			}
		}

		resultEntry := &periodEntry{
			periodStart: p,
			periodEnd:   sortedPeriods[j],
			subject:     pe.subject,
			periodType:  pe.periodType,
			groups:      pe.groups,
			room:        pe.room,
		}
		result = append(result, resultEntry)
		i = j + 1
	}

	return result
}

// buildRoomPeriodEntries builds period entries for a room on a specific day.
func (r *TimetableRenderer) buildRoomPeriodEntries(roomID string, day types.Day) []*periodEntry {
	periodMap := make(map[int]*periodEntry)

	// Assignments using this room
	for _, a := range r.schedule.Assignments {
		if a.Day != day {
			continue
		}

		offering := a.Offering
		courseName := r.getCourseName(offering.CourseID)

		if a.TheoryRoomID == roomID && offering.TheoryPeriods > 0 {
			periodEnd := a.TheoryStart + offering.TheoryPeriods - 1
			groupNames := r.getGroupNames(offering.GroupIDs)
			instructorStr := r.getMainInstructorDisplay(offering.MainInstructorID)

			for p := a.TheoryStart; p <= periodEnd; p++ {
				periodMap[p] = &periodEntry{
					periodStart: p,
					periodEnd:   p,
					subject:     courseName,
					periodType:  "ทฤษฎี",
					groups:      groupNames,
					room:        instructorStr,
				}
			}
		}

		if a.LabRoomID == roomID && offering.LabPeriods > 0 {
			periodEnd := a.LabStart + offering.LabPeriods - 1
			groupNames := r.getGroupNames(offering.GroupIDs)
			instructorStr := r.getInstructorDisplay(offering.MainInstructorID, offering.CoInstructorIDs)

			for p := a.LabStart; p <= periodEnd; p++ {
				periodMap[p] = &periodEntry{
					periodStart: p,
					periodEnd:   p,
					subject:     courseName,
					periodType:  "ปฏิบัติ",
					groups:      groupNames,
					room:        instructorStr,
				}
			}
		}
	}

	// Include instructor_unavailable entries that use this room
	for _, iu := range r.config.InstructorUnavailable {
		if iu.RoomID != roomID || iu.RoomID == "" || iu.RoomID == "none" {
			continue
		}
		if iu.Day != day {
			continue
		}
		if iu.Mode == types.ModeHidden {
			continue
		}
		instructorName := r.getInstructorName(iu.InstructorID)
		for p := iu.StartPeriod; p <= iu.EndPeriod; p++ {
			periodMap[p] = &periodEntry{
				periodStart: p,
				periodEnd:   p,
				subject:     iu.Message,
				periodType:  "x",
				groups:      "x",
				room:        instructorName,
			}
		}
	}

	// Include groups_unavailable entries that use this room
	for _, gu := range r.config.GroupsUnavailable {
		if gu.RoomID != roomID || gu.RoomID == "" || gu.RoomID == "none" {
			continue
		}
		if gu.Day != day {
			continue
		}
		if gu.Mode == types.ModeHidden {
			continue
		}
		// Show group name as the "instructor" column and message as subject
		groupName := gu.GroupID
		if grp, ok := r.config.Groups[gu.GroupID]; ok {
			groupName = grp.Name
		}
		for p := gu.StartPeriod; p <= gu.EndPeriod; p++ {
			periodMap[p] = &periodEntry{
				periodStart: p,
				periodEnd:   p,
				subject:     gu.Message,
				periodType:  "x",
				groups:      groupName,
				room:        "x",
			}
		}
	}

	// Merge consecutive periods
	var result []*periodEntry
	var sortedPeriods []int
	for p := 1; p <= types.MaxPeriodsPerDay; p++ {
		if _, ok := periodMap[p]; ok {
			sortedPeriods = append(sortedPeriods, p)
		}
	}

	i := 0
	for i < len(sortedPeriods) {
		p := sortedPeriods[i]
		pe := periodMap[p]

		j := i
		for j+1 < len(sortedPeriods) && sortedPeriods[j+1] == sortedPeriods[j]+1 {
			nextPE := periodMap[sortedPeriods[j+1]]
			if nextPE.subject == pe.subject && nextPE.periodType == pe.periodType &&
				nextPE.groups == pe.groups && nextPE.room == pe.room {
				j++
			} else {
				break
			}
		}

		resultEntry := &periodEntry{
			periodStart: p,
			periodEnd:   sortedPeriods[j],
			subject:     pe.subject,
			periodType:  pe.periodType,
			groups:      pe.groups,
			room:        pe.room,
		}
		result = append(result, resultEntry)
		i = j + 1
	}

	return result
}

// Helper methods

func (r *TimetableRenderer) getGroupNames(groupIDs []string) string {
	names := make([]string, len(groupIDs))
	for i, gid := range groupIDs {
		if grp, ok := r.config.Groups[gid]; ok {
			names[i] = grp.Name
		} else {
			names[i] = gid
		}
	}
	return strings.Join(names, ",")
}

func (r *TimetableRenderer) getCourseName(courseID string) string {
	if c, ok := r.config.Courses[courseID]; ok {
		return c.Name
	}
	return courseID
}

func (r *TimetableRenderer) getInstructorName(instructorID string) string {
	if i, ok := r.config.Instructors[instructorID]; ok {
		return i.Name
	}
	return instructorID
}

// getMainInstructorDisplay returns the display string for a main instructor.
func (r *TimetableRenderer) getMainInstructorDisplay(mainInstructorID string) string {
	if mainInstructorID == "x" {
		return "x"
	}
	name := r.getInstructorName(mainInstructorID)
	return fmt.Sprintf("%s (หลัก)", name)
}

// getInstructorDisplay returns the display string for all instructors.
func (r *TimetableRenderer) getInstructorDisplay(mainInstructorID string, coInstructorIDs []string) string {
	var parts []string
	if mainInstructorID != "x" {
		name := r.getInstructorName(mainInstructorID)
		parts = append(parts, fmt.Sprintf("%s (หลัก)", name))
	}
	for _, coID := range coInstructorIDs {
		name := r.getInstructorName(coID)
		parts = append(parts, name)
	}
	if len(parts) == 0 {
		return "x"
	}
	return strings.Join(parts, ", ")
}
