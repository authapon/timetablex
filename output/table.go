// Package output generates the timetable.md and table.txt output files.
package output

import (
	"fmt"
	"sort"
	"strings"

	"timetablex/types"
)

// TableRenderer renders schedules into raw table.txt format.
type TableRenderer struct {
	config   *types.Config
	schedule *types.Schedule
}

// NewTableRenderer creates a new table renderer for raw data output.
func NewTableRenderer(config *types.Config, schedule *types.Schedule) *TableRenderer {
	return &TableRenderer{
		config:   config,
		schedule: schedule,
	}
}

// Render generates the complete table.txt content.
//
// The file contains one type of line:
//
// <course_id> <theory_periods> <lab_periods> <group_ids> <day> <theory_start> <lab_start> <theory_room> <lab_room> <main_instructor> <co_instructors>
//
// Fields:
//   - course_id:        รหัสวิชา
//   - theory_periods:   จำนวนคาบทฤษฎี
//   - lab_periods:      จำนวนคาบปฏิบัติ
//   - group_ids:        รหัสกลุ่มเรียน (คั่นด้วยจุลภาค ถ้าหลายกลุ่ม)
//   - day:              วัน (2 ตัวอักษร: mo, tu, we, th, fr, sa, su)
//   - theory_start:     คาบเริ่มต้นทฤษฎี (0 ถ้าไม่มี)
//   - lab_start:        คาบเริ่มต้นปฏิบัติ (0 ถ้าไม่มี)
//   - theory_room:      ห้องทฤษฎีที่ใช้ (x ถ้าไม่มี)
//   - lab_room:         ห้องปฏิบัติที่ใช้ (x ถ้าไม่มี)
//   - main_instructor:  อาจารย์หลัก (x ถ้าไม่มี)
//   - co_instructors:   อาจารย์ร่วม (x ถ้าไม่มี; คั่นด้วยจุลภาค ถ้ามีหลายคน)
//
// สามารถใช้ไฟล์ table.txt เป็น pre-defined สำหรับจัดตารางเริ่มต้นหรือแก้ไขปรับปรุงได้
func (r *TableRenderer) Render() string {
	var sb strings.Builder

	// Sort assignments by day, then theory_start for consistent output
	sorted := make([]*types.Assignment, len(r.schedule.Assignments))
	copy(sorted, r.schedule.Assignments)
	sort.Slice(sorted, func(i, j int) bool {
		ai, aj := sorted[i], sorted[j]
		// Sort by day first
		di := dayOrder(ai.Day)
		dj := dayOrder(aj.Day)
		if di != dj {
			return di < dj
		}
		// Then by theory start (or lab start if no theory)
		si := ai.TheoryStart
		if si == 0 {
			si = ai.LabStart
		}
		sj := aj.TheoryStart
		if sj == 0 {
			sj = aj.LabStart
		}
		if si != sj {
			return si < sj
		}
		// Then by course ID
		return ai.Offering.CourseID < aj.Offering.CourseID
	})

	// Write header comment
	sb.WriteString("# Timetable Table - ข้อมูลตารางสอนในรูปแบบ raw data\n")
	sb.WriteString("# ใช้เป็น pre-defined สำหรับจัดตารางเริ่มต้นหรือแก้ไขปรับปรุง\n")
	sb.WriteString("# รูปแบบ: <course_id> <theory_periods> <lab_periods> <group_ids> <day> <theory_start> <lab_start> <theory_room> <lab_room> <main_instructor> <co_instructors>\n")

	for _, a := range sorted {
		off := a.Offering

		// Rooms: use assigned room, or "x" if none
		theoryRoom := a.TheoryRoomID
		if theoryRoom == "" {
			theoryRoom = "x"
		}
		labRoom := a.LabRoomID
		if labRoom == "" {
			labRoom = "x"
		}

		// Co-instructors
		coInstructors := off.CoInstructorRaw
		if coInstructors == "" {
			coInstructors = "x"
		}

		// Force start values to 0 when there are no periods of that type
		// (parser validation requires this for round-trip compatibility)
		theoryStart := a.TheoryStart
		if off.TheoryPeriods == 0 {
			theoryStart = 0
		}
		labStart := a.LabStart
		if off.LabPeriods == 0 {
			labStart = 0
		}

		line := fmt.Sprintf("%s %d %d %s %s %d %d %s %s %s %s\n",
			off.CourseID,
			off.TheoryPeriods,
			off.LabPeriods,
			off.GroupIDRaw,
			string(a.Day),
			theoryStart,
			labStart,
			theoryRoom,
			labRoom,
			off.MainInstructorID,
			coInstructors,
		)
		sb.WriteString(line)
	}

	return sb.String()
}

// dayOrderMap maps days to their sort order (Monday=0, Sunday=6).
var dayOrderMap = map[types.Day]int{
	types.Monday:    0,
	types.Tuesday:   1,
	types.Wednesday: 2,
	types.Thursday:  3,
	types.Friday:    4,
	types.Saturday:  5,
	types.Sunday:    6,
}

// dayOrder returns a sort order for days (Monday=0, Sunday=6).
func dayOrder(d types.Day) int {
	return dayOrderMap[d]
}
