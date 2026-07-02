// Package parser parses the config.conf file into a Config struct.
package parser

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"timetablex/types"
)

// ParseResult holds the parsed config and any parsing errors.
type ParseResult struct {
	Config *types.Config
	Errors []ParseError
}

// ParseError represents a parsing error with line number.
type ParseError struct {
	Line    int
	Message string
}

func (e ParseError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Message)
}

// Parse parses the given config file and returns the result.
func Parse(filepath string) *ParseResult {
	result := &ParseResult{
		Config: &types.Config{
			Instructors:               make(map[string]*types.Instructor),
			Groups:                    make(map[string]*types.Group),
			Rooms:                     make(map[string]*types.Room),
			Courses:                   make(map[string]*types.Course),
			Offerings:                 nil,
			GroupsUnavailable:         nil,
			InstructorUnavailable:     nil,
			InstructorUnavailableMain: nil,
			InstructorNoLate:          nil,
			Breaks:                    &types.Break{Periods: nil},
		},
	}

	f, err := os.Open(filepath)
	if err != nil {
		result.Errors = append(result.Errors, ParseError{Line: 0, Message: fmt.Sprintf("cannot open file: %v", err)})
		return result
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	var currentSection string

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Strip comments: remove everything from '#' to end of line
		if commentIdx := strings.Index(line, "#"); commentIdx >= 0 {
			line = line[:commentIdx]
		}

		// Trim whitespace
		line = strings.TrimSpace(line)

		// Skip empty lines
		if line == "" {
			continue
		}

		// Check for section header
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.ToLower(line[1 : len(line)-1])
			continue
		}

		// Parse based on current section
		var parseErr *ParseError
		switch currentSection {
		case "instructor":
			parseErr = parseInstructor(result.Config, line, lineNum)
		case "groups":
			parseErr = parseGroups(result.Config, line, lineNum)
		case "rooms":
			parseErr = parseRooms(result.Config, line, lineNum)
		case "courses":
			parseErr = parseCourses(result.Config, line, lineNum)
		case "offering":
			parseErr = parseOffering(result.Config, line, lineNum)
		case "groups_unavailable":
			parseErr = parseGroupsUnavailable(result.Config, line, lineNum)
		case "instructor_unavailable":
			parseErr = parseInstructorUnavailable(result.Config, line, lineNum)
		case "instructor_unavailable_main":
			parseErr = parseInstructorUnavailableMain(result.Config, line, lineNum)
		case "instructor_nolate":
			parseErr = parseInstructorNoLate(result.Config, line, lineNum)
		case "break":
			parseErr = parseBreak(result.Config, line, lineNum)
		default:
			// Unknown section - ignore
		}

		if parseErr != nil {
			result.Errors = append(result.Errors, *parseErr)
		}
	}

	if err := scanner.Err(); err != nil {
		result.Errors = append(result.Errors, ParseError{Line: 0, Message: fmt.Sprintf("error reading file: %v", err)})
	}

	return result
}

// splitFields splits a line by whitespace (one or more spaces/tabs).
func splitFields(line string) []string {
	re := regexp.MustCompile(`\s+`)
	return re.Split(strings.TrimSpace(line), -1)
}

// parseQuotedValue extracts a quoted string value from a line.
// Returns the extracted value (without quotes) and the remaining unparsed part.
func parseQuotedValue(line string) (string, string, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "\"") {
		// Try to find it
		idx := strings.Index(line, "\"")
		if idx < 0 {
			return "", line, fmt.Errorf("no quoted string found")
		}
		line = line[idx:]
	}
	// Find closing quote
	endIdx := strings.LastIndex(line, "\"")
	if endIdx <= 0 {
		return "", line, fmt.Errorf("unclosed quote")
	}
	value := line[1:endIdx]
	remaining := strings.TrimSpace(line[endIdx+1:])
	return value, remaining, nil
}

func parseInstructor(config *types.Config, line string, lineNum int) *ParseError {
	fields := splitFields(line)
	if len(fields) < 2 {
		return &ParseError{Line: lineNum, Message: "instructor: expected at least 2 fields (id name)"}
	}
	id := fields[0]
	// Name is the rest of the line after the first field
	firstFieldLen := len(fields[0])
	restIdx := firstFieldLen
	for restIdx < len(line) && (line[restIdx] == ' ' || line[restIdx] == '\t') {
		restIdx++
	}
	name := strings.TrimSpace(line[restIdx:])

	if _, exists := config.Instructors[id]; exists {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("instructor: duplicate id '%s'", id)}
	}
	config.Instructors[id] = &types.Instructor{ID: id, Name: name}
	return nil
}

func parseGroups(config *types.Config, line string, lineNum int) *ParseError {
	fields := splitFields(line)
	if len(fields) < 3 {
		return &ParseError{Line: lineNum, Message: "groups: expected at least 3 fields (id term_type name)"}
	}
	id := fields[0]
	termType := fields[1]
	if termType != "n" && termType != "s" {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("groups: invalid term_type '%s' (must be 'n' or 's')", termType)}
	}
	// Name is the rest after the second field
	secondFieldEnd := len(fields[0]) + 1
	for idx := 1; idx <= 1; idx++ {
		secondFieldEnd = strings.Index(line[secondFieldEnd:], fields[idx])
		if secondFieldEnd >= 0 {
			secondFieldEnd += len(line[:strings.Index(line[secondFieldEnd:], fields[idx])]) + len(fields[idx])
		}
	}
	// Simpler: just find the position after the second field
	re := regexp.MustCompile(`\s+`)
	matches := re.FindAllStringIndex(line, -1)
	if len(matches) >= 2 {
		nameStart := matches[1][1]
		name := strings.TrimSpace(line[nameStart:])
		if _, exists := config.Groups[id]; exists {
			return &ParseError{Line: lineNum, Message: fmt.Sprintf("groups: duplicate id '%s'", id)}
		}
		config.Groups[id] = &types.Group{ID: id, TermType: types.TermType(termType), Name: name}
	} else {
		return &ParseError{Line: lineNum, Message: "groups: cannot parse name field"}
	}
	return nil
}

func parseRooms(config *types.Config, line string, lineNum int) *ParseError {
	fields := splitFields(line)
	if len(fields) < 2 {
		return &ParseError{Line: lineNum, Message: "rooms: expected at least 2 fields (id name)"}
	}
	id := fields[0]
	firstFieldLen := len(fields[0])
	restIdx := firstFieldLen
	for restIdx < len(line) && (line[restIdx] == ' ' || line[restIdx] == '\t') {
		restIdx++
	}
	name := strings.TrimSpace(line[restIdx:])

	if _, exists := config.Rooms[id]; exists {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("rooms: duplicate id '%s'", id)}
	}
	config.Rooms[id] = &types.Room{ID: id, Name: name}
	return nil
}

func parseCourses(config *types.Config, line string, lineNum int) *ParseError {
	fields := splitFields(line)
	if len(fields) < 2 {
		return &ParseError{Line: lineNum, Message: "courses: expected at least 2 fields (id name)"}
	}
	id := fields[0]
	firstFieldLen := len(fields[0])
	restIdx := firstFieldLen
	for restIdx < len(line) && (line[restIdx] == ' ' || line[restIdx] == '\t') {
		restIdx++
	}
	name := strings.TrimSpace(line[restIdx:])

	if _, exists := config.Courses[id]; exists {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("courses: duplicate id '%s'", id)}
	}
	config.Courses[id] = &types.Course{ID: id, Name: name}
	return nil
}

func parseOffering(config *types.Config, line string, lineNum int) *ParseError {
	fields := splitFields(line)
	if len(fields) < 8 {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("offering: expected 8 fields, got %d", len(fields))}
	}

	courseID := fields[0]
	theoryPeriods, err := strconv.Atoi(fields[1])
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("offering: invalid theory_periods '%s'", fields[1])}
	}
	labPeriods, err := strconv.Atoi(fields[2])
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("offering: invalid lab_periods '%s'", fields[2])}
	}
	if theoryPeriods < 0 || labPeriods < 0 {
		return &ParseError{Line: lineNum, Message: "offering: theory_periods and lab_periods must be non-negative"}
	}
	if theoryPeriods == 0 && labPeriods == 0 {
		return &ParseError{Line: lineNum, Message: "offering: theory_periods and lab_periods cannot both be 0"}
	}

	groupIDList := fields[3]
	mainInstructorID := fields[4]
	coInstructorRaw := fields[5]
	theoryRoomRaw := fields[6]
	labRoomRaw := fields[7]

	// Parse group IDs (comma separated)
	groupIDs := parseCommaList(groupIDList)
	coInstructorIDs := parseCommaOrX(coInstructorRaw)
	theoryRoomIDs := parseCommaOrX(theoryRoomRaw)
	labRoomIDs := parseCommaOrX(labRoomRaw)

	offering := &types.Offering{
		CourseID:         courseID,
		TheoryPeriods:    theoryPeriods,
		LabPeriods:       labPeriods,
		GroupIDs:         groupIDs,
		GroupIDRaw:       groupIDList,
		MainInstructorID: mainInstructorID,
		CoInstructorIDs:  coInstructorIDs,
		CoInstructorRaw:  coInstructorRaw,
		TheoryRoomIDs:    theoryRoomIDs,
		TheoryRoomRaw:    theoryRoomRaw,
		LabRoomIDs:       labRoomIDs,
		LabRoomRaw:       labRoomRaw,
	}

	config.Offerings = append(config.Offerings, offering)
	return nil
}

func parseCommaList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func parseCommaOrX(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "x" {
		return nil
	}
	return parseCommaList(s)
}

func parseGroupsUnavailable(config *types.Config, line string, lineNum int) *ParseError {
	// Format (old): <group_id> <day> <start_period>-<end_period> <mode>
	// Format (new): <group_id> <day> <start_period>-<end_period> <room> <mode>
	// mode: "hidden" or quoted message like "\"กิจกรรมพิเศษ\""
	fields := splitFields(line)
	if len(fields) < 4 {
		return &ParseError{Line: lineNum, Message: "groups_unavailable: expected at least 4 fields"}
	}

	groupID := fields[0]
	day := types.Day(fields[1])
	periodRange := fields[2]

	startPeriod, endPeriod, err := parsePeriodRange(periodRange)
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("groups_unavailable: %v", err)}
	}

	if !isValidDay(day) {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("groups_unavailable: invalid day '%s'", day)}
	}

	gu := &types.GroupUnavailable{
		GroupID:     groupID,
		Day:         day,
		StartPeriod: startPeriod,
		EndPeriod:   endPeriod,
		RoomID:      "none",
	}

	// Detect format: new format has room as field[3] (5+ fields total)
	var modeRaw string
	if len(fields) >= 5 {
		// New format: <id> <day> <range> <room> <mode>
		gu.RoomID = fields[3]
		modeRaw = strings.Join(fields[4:], " ")
	} else {
		// Old format: <id> <day> <range> <mode>
		modeRaw = fields[3]
	}

	if modeRaw == "hidden" {
		gu.Mode = types.ModeHidden
	} else if strings.HasPrefix(modeRaw, "\"") {
		value, _, err := parseQuotedValue(modeRaw)
		if err != nil {
			return &ParseError{Line: lineNum, Message: fmt.Sprintf("groups_unavailable: invalid mode format")}
		}
		gu.Mode = types.ModeMessage
		gu.Message = value
	} else {
		// Treat as message without quotes
		gu.Mode = types.ModeMessage
		gu.Message = modeRaw
	}

	config.GroupsUnavailable = append(config.GroupsUnavailable, gu)
	return nil
}

func parseInstructorUnavailable(config *types.Config, line string, lineNum int) *ParseError {
	fields := splitFields(line)
	if len(fields) < 4 {
		return &ParseError{Line: lineNum, Message: "instructor_unavailable: expected at least 4 fields"}
	}

	instructorID := fields[0]
	day := types.Day(fields[1])
	periodRange := fields[2]

	startPeriod, endPeriod, err := parsePeriodRange(periodRange)
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("instructor_unavailable: %v", err)}
	}

	if !isValidDay(day) {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("instructor_unavailable: invalid day '%s'", day)}
	}

	iu := &types.InstructorUnavailable{
		InstructorID: instructorID,
		Day:          day,
		StartPeriod:  startPeriod,
		EndPeriod:    endPeriod,
		RoomID:       "none",
	}

	// Detect format: new format has room as field[3] (5+ fields total)
	var modeRaw string
	if len(fields) >= 5 {
		// New format: <id> <day> <range> <room> <mode>
		iu.RoomID = fields[3]
		modeRaw = strings.Join(fields[4:], " ")
	} else {
		// Old format: <id> <day> <range> <mode>
		modeRaw = fields[3]
	}

	if modeRaw == "hidden" {
		iu.Mode = types.ModeHidden
	} else if strings.HasPrefix(modeRaw, "\"") {
		value, _, err := parseQuotedValue(modeRaw)
		if err != nil {
			return &ParseError{Line: lineNum, Message: fmt.Sprintf("instructor_unavailable: invalid mode format")}
		}
		iu.Mode = types.ModeMessage
		iu.Message = value
	} else {
		iu.Mode = types.ModeMessage
		iu.Message = modeRaw
	}

	config.InstructorUnavailable = append(config.InstructorUnavailable, iu)
	return nil
}

func parseInstructorUnavailableMain(config *types.Config, line string, lineNum int) *ParseError {
	fields := splitFields(line)
	if len(fields) < 3 {
		return &ParseError{Line: lineNum, Message: "instructor_unavailable_main: expected at least 3 fields"}
	}

	instructorID := fields[0]
	day := types.Day(fields[1])
	periodRange := fields[2]

	startPeriod, endPeriod, err := parsePeriodRange(periodRange)
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("instructor_unavailable_main: %v", err)}
	}

	if !isValidDay(day) {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("instructor_unavailable_main: invalid day '%s'", day)}
	}

	i := &types.InstructorUnavailableMain{
		InstructorID: instructorID,
		Day:          day,
		StartPeriod:  startPeriod,
		EndPeriod:    endPeriod,
		RoomID:       "none",
	}

	config.InstructorUnavailableMain = append(config.InstructorUnavailableMain, i)
	return nil
}

func parseInstructorNoLate(config *types.Config, line string, lineNum int) *ParseError {
	fields := splitFields(line)
	if len(fields) < 3 {
		return &ParseError{Line: lineNum, Message: "instructor_nolate: expected at least 3 fields (instructor_id day period_threshold)"}
	}

	instructorID := fields[0]
	day := types.Day(fields[1])
	periodThreshold, err := strconv.Atoi(fields[2])
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("instructor_nolate: invalid period_threshold '%s'", fields[2])}
	}

	if !isValidDay(day) {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("instructor_nolate: invalid day '%s'", day)}
	}

	if periodThreshold < 1 || periodThreshold > types.MaxPeriodsPerDay {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("instructor_nolate: period_threshold must be 1-%d", types.MaxPeriodsPerDay)}
	}

	config.InstructorNoLate = append(config.InstructorNoLate, &types.InstructorNoLate{
		InstructorID:    instructorID,
		Day:             day,
		PeriodThreshold: periodThreshold,
	})
	return nil
}

func parseBreak(config *types.Config, line string, lineNum int) *ParseError {
	line = strings.TrimSpace(line)
	period, err := strconv.Atoi(line)
	if err != nil {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("break: invalid period '%s'", line)}
	}
	if period < 1 || period > types.MaxPeriodsPerDay {
		return &ParseError{Line: lineNum, Message: fmt.Sprintf("break: period must be 1-%d", types.MaxPeriodsPerDay)}
	}
	config.Breaks.Periods = append(config.Breaks.Periods, period)
	return nil
}

func parsePeriodRange(s string) (int, int, error) {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid period range '%s' (expected start-end)", s)
	}
	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start period '%s'", parts[0])
	}
	end, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid end period '%s'", parts[1])
	}
	if start < 1 || end > types.MaxPeriodsPerDay || start > end {
		return 0, 0, fmt.Errorf("invalid period range %d-%d (must be 1-%d, start <= end)", start, end, types.MaxPeriodsPerDay)
	}
	return start, end, nil
}

func isValidDay(d types.Day) bool {
	_, ok := types.DayFullName[d]
	return ok
}

// HasSections returns true if the config has any data parsed.
func (pr *ParseResult) HasSections() bool {
	return len(pr.Config.Instructors) > 0 || len(pr.Config.Groups) > 0 ||
		len(pr.Config.Rooms) > 0 || len(pr.Config.Courses) > 0 ||
		len(pr.Config.Offerings) > 0
}
