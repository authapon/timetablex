// Package types defines all data structures used in the class scheduling system.
package types

import "fmt"

// Day represents a day of the week (2-letter code).
type Day string

const (
	Monday    Day = "mo"
	Tuesday   Day = "tu"
	Wednesday Day = "we"
	Thursday  Day = "th"
	Friday    Day = "fr"
	Saturday  Day = "sa"
	Sunday    Day = "su"
)

// DayFullName maps day codes to full Thai names.
var DayFullName = map[Day]string{
	Monday:    "จันทร์",
	Tuesday:   "อังคาร",
	Wednesday: "พุธ",
	Thursday:  "พฤหัสบดี",
	Friday:    "ศุกร์",
	Saturday:  "เสาร์",
	Sunday:    "อาทิตย์",
}

// AllDays returns all days in order.
func AllDays() []Day {
	return []Day{Monday, Tuesday, Wednesday, Thursday, Friday, Saturday, Sunday}
}

// TermType represents the term type of a group.
type TermType string

const (
	Normal  TermType = "n"
	Special TermType = "s"
)

// UnavailabilityMode represents the mode of an unavailability entry.
type UnavailabilityMode int

const (
	ModeHidden  UnavailabilityMode = iota // hidden: don't show anything
	ModeMessage                           // custom message to display
)

// Instructor represents a single instructor.
type Instructor struct {
	ID   string
	Name string
}

// Group represents a group of students.
type Group struct {
	ID       string
	TermType TermType
	Name     string
}

// Room represents a classroom.
type Room struct {
	ID   string
	Name string
}

// Course represents a course/subject.
type Course struct {
	ID   string
	Name string
}

// Offering represents a course offering in a semester.
type Offering struct {
	CourseID         string
	TheoryPeriods    int
	LabPeriods       int
	GroupIDs         []string // if multiple, it's a merged session
	GroupIDRaw       string   // original comma-separated string
	MainInstructorID string   // "x" if none
	CoInstructorIDs  []string // empty if "x"
	CoInstructorRaw  string   // original comma-separated string, or "x"
	TheoryRoomIDs    []string // empty if "x"
	TheoryRoomRaw    string   // original comma-separated string, or "x"
	LabRoomIDs       []string // empty if "x"
	LabRoomRaw       string   // original comma-separated string, or "x"
}

// TotalPeriods returns the total number of periods for this offering.
func (o *Offering) TotalPeriods() int {
	return o.TheoryPeriods + o.LabPeriods
}

// HasTheory returns true if this offering has theory periods.
func (o *Offering) HasTheory() bool {
	return o.TheoryPeriods > 0
}

// HasLab returns true if this offering has lab periods.
func (o *Offering) HasLab() bool {
	return o.LabPeriods > 0
}

// GroupUnavailable represents a time period when a group is unavailable.
type GroupUnavailable struct {
	GroupID     string
	Day         Day
	StartPeriod int
	EndPeriod   int
	RoomID      string // "none" if no room, or room ID
	Mode        UnavailabilityMode
	Message     string
}

// InstructorUnavailable represents a time period when an instructor is unavailable.
type InstructorUnavailable struct {
	InstructorID string
	Day          Day
	StartPeriod  int
	EndPeriod    int
	RoomID       string // "none" if no room, or room ID
	Mode         UnavailabilityMode
	Message      string
}

// InstructorUnavailableMain represents a time period when an instructor cannot be a main instructor.
type InstructorUnavailableMain struct {
	InstructorID string
	Day          Day
	StartPeriod  int
	EndPeriod    int
	RoomID       string // "none" if no room, or room ID
}

// InstructorNoLate represents a constraint that an instructor cannot be main instructor
// from PeriodThreshold onwards.
type InstructorNoLate struct {
	InstructorID    string
	Day             Day
	PeriodThreshold int
}

// Break represents lunch break configuration (ordered by priority).
type Break struct {
	Periods []int // ordered by priority (first = highest priority)
}

// Config holds all parsed configuration data.
type Config struct {
	Instructors               map[string]*Instructor
	Groups                    map[string]*Group
	Rooms                     map[string]*Room
	Courses                   map[string]*Course
	Offerings                 []*Offering
	GroupsUnavailable         []*GroupUnavailable
	InstructorUnavailable     []*InstructorUnavailable
	InstructorUnavailableMain []*InstructorUnavailableMain
	InstructorNoLate          []*InstructorNoLate
	Breaks                    *Break
}

// PeriodInfo holds information about a scheduled period.
type PeriodInfo struct {
	Day         Day
	PeriodStart int // 1-based
	PeriodEnd   int // 1-based, inclusive
}

// Assignment represents a complete assignment of an offering to a specific time slot and rooms.
type Assignment struct {
	Offering      *Offering
	Day           Day
	TheoryStart   int // 1-based start period for theory block (0 if no theory)
	LabStart      int // 1-based start period for lab block (0 if no lab)
	TheoryRoomID  string // "" if no room, "x" if no room needed
	LabRoomID     string // "" if no room, "x" if no room needed
}

// TheoryBlock returns the theory period info.
func (a *Assignment) TheoryBlock() *PeriodInfo {
	if a.TheoryStart == 0 {
		return nil
	}
	return &PeriodInfo{
		Day:         a.Day,
		PeriodStart: a.TheoryStart,
		PeriodEnd:   a.TheoryStart + a.Offering.TheoryPeriods - 1,
	}
}

// LabBlock returns the lab period info.
func (a *Assignment) LabBlock() *PeriodInfo {
	if a.LabStart == 0 {
		return nil
	}
	return &PeriodInfo{
		Day:         a.Day,
		PeriodStart: a.LabStart,
		PeriodEnd:   a.LabStart + a.Offering.LabPeriods - 1,
	}
}

// ContainsPeriod checks if the assignment covers the given period.
func (a *Assignment) ContainsPeriod(day Day, period int) bool {
	if a.Day != day {
		return false
	}
	if a.TheoryStart > 0 && period >= a.TheoryStart && period < a.TheoryStart+a.Offering.TheoryPeriods {
		return true
	}
	if a.LabStart > 0 && period >= a.LabStart && period < a.LabStart+a.Offering.LabPeriods {
		return true
	}
	return false
}

// IsTheoryPeriod returns true if the given period is a theory period of this assignment.
func (a *Assignment) IsTheoryPeriod(period int) bool {
	return a.TheoryStart > 0 && period >= a.TheoryStart && period < a.TheoryStart+a.Offering.TheoryPeriods
}

// IsLabPeriod returns true if the given period is a lab period of this assignment.
func (a *Assignment) IsLabPeriod(period int) bool {
	return a.LabStart > 0 && period >= a.LabStart && period < a.LabStart+a.Offering.LabPeriods
}

// String returns a string representation of the assignment.
func (a *Assignment) String() string {
	return fmt.Sprintf("%s %s day=%s theory=%d-%d lab=%d-%d tRoom=%s lRoom=%s",
		a.Offering.CourseID, a.Offering.GroupIDRaw,
		string(a.Day), a.TheoryStart, a.TheoryStart+a.Offering.TheoryPeriods-1,
		a.LabStart, a.LabStart+a.Offering.LabPeriods-1,
		a.TheoryRoomID, a.LabRoomID)
}

// Schedule holds all assignments and the lunch break selection.
type Schedule struct {
	Assignments []*Assignment
	// Per-entity lunch break: each instructor and group can have their own break period per day.
	InstructorLunchBreak map[string]map[Day]int // instructorID -> day -> lunch break period number
	GroupLunchBreak      map[string]map[Day]int // groupID -> day -> lunch break period number
	// Per-entity lunch break priority: priority = 1 (highest), higher number = lower priority
	InstructorBreakPriority map[string]map[Day]int // instructorID -> day -> priority (1 = highest)
	GroupBreakPriority      map[string]map[Day]int // groupID -> day -> priority (1 = highest)
	Config                 *Config
}

// PredefinedAssignment represents a pre-defined schedule assignment from config.txt.
type PredefinedAssignment struct {
	LineNumber       int    // line number in config.txt
	CourseID         string
	TheoryPeriods    int
	LabPeriods       int
	GroupIDs         []string
	GroupIDRaw       string
	Day              Day
	TheoryStart      int
	LabStart         int
	TheoryRoomID     string // "$" for auto-assign, "x" for none, or room ID
	LabRoomID        string // "$" for auto-assign, "x" for none, or room ID
	MainInstructorID string
	CoInstructorIDs  []string
	CoInstructorRaw  string
	// Resolved rooms (after resolving "$")
	ResolvedTheoryRoomID string
	ResolvedLabRoomID    string
}

// PredefinedData holds all pre-defined schedule data parsed from config.txt.
type PredefinedData struct {
	Assignments            []*PredefinedAssignment
	GroupsUnavailable      []*GroupUnavailable
	InstructorUnavailable  []*InstructorUnavailable
}

// MaxPeriodsPerDay is the maximum number of periods in a day.
const MaxPeriodsPerDay = 13
