package output

import (
	"strings"
	"testing"

	"timetablex/types"
)

func gridFixture() (*types.Config, *types.Schedule) {
	cfg := &types.Config{
		Instructors: map[string]*types.Instructor{"i1": {ID: "i1", Name: "สมชาย"}, "i2": {ID: "i2", Name: "สมหญิง"}},
		Groups:      map[string]*types.Group{"g1": {ID: "g1", Name: "ปวช.1/1"}},
		Rooms:       map[string]*types.Room{"r1": {ID: "r1", Name: "A101"}, "r2": {ID: "r2", Name: "LAB1"}},
		Courses:     map[string]*types.Course{"c1": {ID: "c1", Name: "คอมพิวเตอร์"}, "c2": {ID: "c2", Name: "ภาษาไทย"}},
	}
	o1 := &types.Offering{CourseID: "c1", TheoryPeriods: 2, LabPeriods: 2, GroupIDs: []string{"g1"}, MainInstructorID: "i1", CoInstructorIDs: []string{"i2"}}
	o2 := &types.Offering{CourseID: "c2", TheoryPeriods: 2, GroupIDs: []string{"g1"}, MainInstructorID: "i1"}
	sch := &types.Schedule{
		Assignments: []*types.Assignment{
			{Offering: o1, Day: types.Monday, TheoryStart: 1, LabStart: 3, TheoryRoomID: "r1", LabRoomID: "r2"},
			{Offering: o2, Day: types.Monday, TheoryStart: 6, TheoryRoomID: "r1", LabRoomID: "x"},
		},
		Config: cfg,
	}
	return cfg, sch
}

func TestGridSeparatesTheoryAndLab(t *testing.T) {
	cfg, sch := gridFixture()
	out := NewRenderer(cfg, sch).Render()

	// Main teaching is blue, co-teaching (lab with co-instructor i2) is orange
	if !strings.Contains(out, `colspan="2" bgcolor="`+mainColor+`"`) {
		t.Fatalf("expected main-teaching cells:\n%s", out)
	}
	instr2 := out[strings.Index(out, "(i2)"):strings.Index(out, "## ตารางสอนรายกลุ่มเรียน")]
	if !strings.Contains(instr2, `bgcolor="`+coColor+`"`) || strings.Contains(instr2, `bgcolor="`+mainColor+`"`) {
		t.Fatalf("co-instructor timetable should only have co-teaching cells:\n%s", instr2)
	}
	if !strings.Contains(out, "<b>[ทฤษฎี]</b>") || !strings.Contains(out, "<b>[ปฏิบัติ]</b>") {
		t.Fatalf("missing period type labels:\n%s", out)
	}
	if !strings.Contains(out, "<th>วัน / คาบที่</th><th>1<br>08.00‑09.00</th>") {
		t.Fatalf("missing period header row:\n%s", out)
	}
}

// Two sections of the same course taught back-to-back by one instructor must not be merged
// into one cell (the second group used to disappear from the instructor's timetable).
func TestGridKeepsAdjacentSectionsOfSameCourseSeparate(t *testing.T) {
	cfg, _ := gridFixture()
	cfg.Groups["g2"] = &types.Group{ID: "g2", Name: "ปวช.1/2"}
	cfg.Rooms["r3"] = &types.Room{ID: "r3", Name: "B202"}
	o1 := &types.Offering{CourseID: "c2", TheoryPeriods: 1, GroupIDs: []string{"g1"}, MainInstructorID: "i1"}
	o2 := &types.Offering{CourseID: "c2", TheoryPeriods: 1, GroupIDs: []string{"g2"}, MainInstructorID: "i1"}
	sch := &types.Schedule{
		Assignments: []*types.Assignment{
			{Offering: o1, Day: types.Monday, TheoryStart: 1, TheoryRoomID: "r1", LabRoomID: "x"},
			{Offering: o2, Day: types.Monday, TheoryStart: 2, TheoryRoomID: "r3", LabRoomID: "x"},
		},
		Config: cfg,
	}
	out := NewRenderer(cfg, sch).Render()

	instructor := out[:strings.Index(out, "## ตารางสอนรายกลุ่มเรียน")]
	for _, want := range []string{"กลุ่ม ปวช.1/1", "ห้อง A101", "กลุ่ม ปวช.1/2", "ห้อง B202"} {
		if !strings.Contains(instructor, want) {
			t.Fatalf("instructor timetable is missing %q:\n%s", want, instructor)
		}
	}
	if strings.Contains(instructor, `colspan="2"`) {
		t.Fatalf("different sections must not be merged:\n%s", instructor)
	}
}
