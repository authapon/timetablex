// Package parser parses the config.conf file and table-format config.txt files.
package parser

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"timetablex/types"
)

// ParsePredefined parses a table-format file (config.txt) and returns pre-defined assignment data.
// The format is the same as table.txt output:
//
//	<course_id> <theory_periods> <lab_periods> <group_ids> <day> <theory_start> <lab_start> <theory_room> <lab_room> <main_instructor> <co_instructors>
//	GU <group_id> <day> <start_period> <end_period> <room> <mode>
//	IU <instructor_id> <day> <start_period> <end_period> <room> <mode>
//
// Note: The old format with "A" prefix at the start of assignment lines is still supported for backward compatibility.
func ParsePredefined(filepath string) (*types.PredefinedData, []ParseError) {
	result := &types.PredefinedData{}
	var errors []ParseError

	f, err := os.Open(filepath)
	if err != nil {
		errors = append(errors, ParseError{Line: 0, Message: fmt.Sprintf("cannot open file: %v", err)})
		return result, errors
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Strip comments
		if commentIdx := strings.Index(line, "#"); commentIdx >= 0 {
			line = line[:commentIdx]
		}

		// Trim whitespace
		line = strings.TrimSpace(line)

		// Skip empty lines
		if line == "" {
			continue
		}

		var parseErr *ParseError
		if strings.HasPrefix(line, "GU ") || strings.HasPrefix(line, "GU\t") {
			parseErr = parsePredefinedGU(result, line, lineNum)
		} else if strings.HasPrefix(line, "IU ") || strings.HasPrefix(line, "IU\t") {
			parseErr = parsePredefinedIU(result, line, lineNum)
		} else {
			// Lines that don't start with GU or IU are treated as assignment lines
			// Supports both old format ("A <course_id>...") and new format ("<course_id>...")
			parseErr = parsePredefinedA(result, line, lineNum)
		}

		if parseErr != nil {
			errors = append(errors, *parseErr)
		}
	}

	if err := scanner.Err(); err != nil {
		errors = append(errors, ParseError{Line: 0, Message: fmt.Sprintf("error reading file: %v", err)})
	}

	return result, errors
}

// parsePredefinedA parses an assignment line: [A] <course_id> <theory_periods> <lab_periods> <group_ids> <day> <theory_start> <lab_start> <theory_room> <lab_room> <main_instructor> <co_instructors>
// The optional "A" prefix is supported for backward compatibility.
func parsePredefinedA(result *types.PredefinedData, line string, lineNum int) *ParseError {
	// Remove optional "A" prefix and whitespace (backward compatibility)
	if strings.HasPrefix(line, "A ") || strings.HasPrefix(line, "A\t") {
		line = strings.TrimSpace(line[1:])
	} else {
		line = strings.TrimSpace(line)
	}
	fields := splitFields(line)

	if len(fields) != 11 {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("A: expected 11 fields, got %d", len(fields))}
	}

	courseID := fields[0]

	theoryPeriods, err := strconv.Atoi(fields[1])
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("A: invalid theory_periods '%s'", fields[1])}
	}
	labPeriods, err := strconv.Atoi(fields[2])
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("A: invalid lab_periods '%s'", fields[2])}
	}
	if theoryPeriods < 0 || labPeriods < 0 {
		return &ParseError{Line: lineNum, Message: "A: theory_periods and lab_periods must be non-negative"}
	}
	if theoryPeriods == 0 && labPeriods == 0 {
		return &ParseError{Line: lineNum, Message: "A: theory_periods and lab_periods cannot both be 0"}
	}

	groupIDRaw := fields[3]
	groupIDs := parseCommaList(groupIDRaw)
	if len(groupIDs) == 0 {
		return &ParseError{Line: lineNum, Message: "A: group_ids is empty"}
	}

	day := types.Day(fields[4])
	if !isValidDay(day) {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("A: invalid day '%s'", fields[4])}
	}

	theoryStart, err := strconv.Atoi(fields[5])
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("A: invalid theory_start '%s'", fields[5])}
	}
	labStart, err := strconv.Atoi(fields[6])
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("A: invalid lab_start '%s'", fields[6])}
	}
	if theoryStart < 0 || labStart < 0 {
		return &ParseError{Line: lineNum, Message: "A: theory_start and lab_start must be non-negative"}
	}

	// Validate period bounds
	if theoryStart > 0 && theoryPeriods > 0 && theoryStart+theoryPeriods-1 > types.MaxPeriodsPerDay {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("A: theory block exceeds max period %d (start=%d, periods=%d)", types.MaxPeriodsPerDay, theoryStart, theoryPeriods)}
	}
	if labStart > 0 && labPeriods > 0 && labStart+labPeriods-1 > types.MaxPeriodsPerDay {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("A: lab block exceeds max period %d (start=%d, periods=%d)", types.MaxPeriodsPerDay, labStart, labPeriods)}
	}

	// Validate consistency between periods and start values
	if theoryPeriods > 0 && theoryStart <= 0 {
		return &ParseError{Line: lineNum, Message: "A: theory_start must be > 0 when theory_periods > 0"}
	}
	if labPeriods > 0 && labStart <= 0 {
		return &ParseError{Line: lineNum, Message: "A: lab_start must be > 0 when lab_periods > 0"}
	}
	if theoryPeriods == 0 && theoryStart > 0 {
		return &ParseError{Line: lineNum, Message: "A: theory_start must be 0 when theory_periods == 0"}
	}
	if labPeriods == 0 && labStart > 0 {
		return &ParseError{Line: lineNum, Message: "A: lab_start must be 0 when lab_periods == 0"}
	}

	// Theory and lab must be contiguous (theory ends exactly when lab starts)
	if theoryStart > 0 && labStart > 0 && theoryStart+theoryPeriods != labStart {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("A: theory and lab blocks must be contiguous (theory ends at %d, lab starts at %d)", theoryStart+theoryPeriods, labStart)}
	}

	theoryRoom := fields[7]
	labRoom := fields[8]

	// Validate room values
	if theoryRoom != "x" && theoryRoom != "$" && theoryRoom != "" {
		// Specific room ID - will be validated against config later
	}
	if theoryPeriods > 0 && theoryRoom == "" {
		return &ParseError{Line: lineNum, Message: "A: theory_room is required when theory_periods > 0"}
	}
	if theoryPeriods == 0 && theoryRoom != "" && theoryRoom != "x" {
		return &ParseError{Line: lineNum, Message: "A: theory_room must be 'x' when theory_periods == 0"}
	}

	if labRoom != "x" && labRoom != "$" && labRoom != "" {
		// Specific room ID - will be validated against config later
	}
	if labPeriods > 0 && labRoom == "" {
		return &ParseError{Line: lineNum, Message: "A: lab_room is required when lab_periods > 0"}
	}
	if labPeriods == 0 && labRoom != "" && labRoom != "x" {
		return &ParseError{Line: lineNum, Message: "A: lab_room must be 'x' when lab_periods == 0"}
	}

	mainInstructorID := fields[9]
	coInstructorRaw := fields[10]
	coInstructorIDs := parseCommaOrX(coInstructorRaw)

	pa := &types.PredefinedAssignment{
		LineNumber:       lineNum,
		CourseID:         courseID,
		TheoryPeriods:    theoryPeriods,
		LabPeriods:       labPeriods,
		GroupIDs:         groupIDs,
		GroupIDRaw:       groupIDRaw,
		Day:              day,
		TheoryStart:      theoryStart,
		LabStart:         labStart,
		TheoryRoomID:     theoryRoom,
		LabRoomID:        labRoom,
		MainInstructorID: mainInstructorID,
		CoInstructorIDs:  coInstructorIDs,
		CoInstructorRaw:  coInstructorRaw,
	}

	result.Assignments = append(result.Assignments, pa)
	return nil
}

// parsePredefinedGU parses a group unavailable line: GU <group_id> <day> <start_period> <end_period> <room> <mode>
func parsePredefinedGU(result *types.PredefinedData, line string, lineNum int) *ParseError {
	line = strings.TrimSpace(line[2:]) // Remove "GU"
	fields := splitFields(line)

	if len(fields) < 6 {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("GU: expected 6 fields, got %d", len(fields))}
	}

	groupID := fields[0]
	day := types.Day(fields[1])
	if !isValidDay(day) {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("GU: invalid day '%s'", fields[1])}
	}

	startPeriod, err := strconv.Atoi(fields[2])
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("GU: invalid start_period '%s'", fields[2])}
	}
	endPeriod, err := strconv.Atoi(fields[3])
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("GU: invalid end_period '%s'", fields[3])}
	}
	if startPeriod < 1 || endPeriod > types.MaxPeriodsPerDay || startPeriod > endPeriod {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("GU: invalid period range %d-%d (must be 1-%d, start <= end)", startPeriod, endPeriod, types.MaxPeriodsPerDay)}
	}

	roomID := fields[4]
	if roomID == "" || roomID == "none" {
		roomID = "none"
	}

	// Parse mode (hidden or quoted message or plain text)
	modeRaw := strings.Join(fields[5:], " ")
	var mode types.UnavailabilityMode
	var message string
	if modeRaw == "hidden" {
		mode = types.ModeHidden
	} else if strings.HasPrefix(modeRaw, "\"") {
		value, _, err := parseQuotedValue(modeRaw)
		if err != nil {
			return &ParseError{Line: lineNum, Message: "GU: invalid mode format (unclosed quote)"}
		}
		mode = types.ModeMessage
		message = value
	} else {
		mode = types.ModeMessage
		message = modeRaw
	}

	gu := &types.GroupUnavailable{
		GroupID:     groupID,
		Day:         day,
		StartPeriod: startPeriod,
		EndPeriod:   endPeriod,
		RoomID:      roomID,
		Mode:        mode,
		Message:     message,
	}

	result.GroupsUnavailable = append(result.GroupsUnavailable, gu)
	return nil
}

// parsePredefinedIU parses an instructor unavailable line: IU <instructor_id> <day> <start_period> <end_period> <room> <mode>
func parsePredefinedIU(result *types.PredefinedData, line string, lineNum int) *ParseError {
	line = strings.TrimSpace(line[2:]) // Remove "IU"
	fields := splitFields(line)

	if len(fields) < 6 {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("IU: expected 6 fields, got %d", len(fields))}
	}

	instructorID := fields[0]
	day := types.Day(fields[1])
	if !isValidDay(day) {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("IU: invalid day '%s'", fields[1])}
	}

	startPeriod, err := strconv.Atoi(fields[2])
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("IU: invalid start_period '%s'", fields[2])}
	}
	endPeriod, err := strconv.Atoi(fields[3])
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("IU: invalid end_period '%s'", fields[3])}
	}
	if startPeriod < 1 || endPeriod > types.MaxPeriodsPerDay || startPeriod > endPeriod {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("IU: invalid period range %d-%d (must be 1-%d, start <= end)", startPeriod, endPeriod, types.MaxPeriodsPerDay)}
	}

	roomID := fields[4]
	if roomID == "" || roomID == "none" {
		roomID = "none"
	}

	// Parse mode
	modeRaw := strings.Join(fields[5:], " ")
	var mode types.UnavailabilityMode
	var message string
	if modeRaw == "hidden" {
		mode = types.ModeHidden
	} else if strings.HasPrefix(modeRaw, "\"") {
		value, _, err := parseQuotedValue(modeRaw)
		if err != nil {
			return &ParseError{Line: lineNum, Message: "IU: invalid mode format (unclosed quote)"}
		}
		mode = types.ModeMessage
		message = value
	} else {
		mode = types.ModeMessage
		message = modeRaw
	}

	iu := &types.InstructorUnavailable{
		InstructorID: instructorID,
		Day:          day,
		StartPeriod:  startPeriod,
		EndPeriod:    endPeriod,
		RoomID:       roomID,
		Mode:         mode,
		Message:      message,
	}

	result.InstructorUnavailable = append(result.InstructorUnavailable, iu)
	return nil
}
