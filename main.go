package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"timetablex/output"
	"timetablex/parser"
	"timetablex/scheduler"
	"timetablex/types"
	"timetablex/validator"
	"timetablex/web"
)

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n")
		printConfigFormat()
	}
}

func main() {
	configPath := flag.String("config", "config.conf", "Path to config file")
	outputPath := flag.String("output", "timetable.md", "Path to output file")
	seed := flag.Int64("seed", time.Now().UnixNano(), "Random seed for reproducibility")
	attempts := flag.Int("attempts", 200, "Number of scheduling attempts")
	verbose := flag.Bool("verbose", false, "Enable verbose output")
	port := flag.Int("p", 0, "Start web server on specified port")
	flag.Parse()

	// If -p flag is set, start web server mode
	if *port > 0 {
		srv := web.NewServer(*port, *attempts)
		if err := srv.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Web server error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *verbose {
		fmt.Printf("Using seed: %d\n", *seed)
	}

	// Step 1: Parse config.conf
	if *verbose {
		fmt.Println("Parsing config...")
	}

	parseResult := parser.Parse(*configPath)
	if len(parseResult.Errors) > 0 {
		for _, err := range parseResult.Errors {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}

	if !parseResult.HasSections() {
		fmt.Fprintf(os.Stderr, "Error: no valid data found in config file\n")
		os.Exit(1)
	}

	// Step 1.5: Check for config.txt (pre-defined schedule)
	predefinedPath := "config.txt"
	var predefinedData *types.PredefinedData
	hasPredefined := false

	if _, err := os.Stat(predefinedPath); err == nil {
		if *verbose {
			fmt.Println("Found config.txt, parsing pre-defined schedule...")
		}

		data, parseErrs := parser.ParsePredefined(predefinedPath)
		if len(parseErrs) > 0 {
			for _, err := range parseErrs {
				fmt.Fprintf(os.Stderr, "Config.txt error: %v\n", err)
			}
			os.Exit(1)
		}

		// Validate predefined data against config.conf
		if *verbose {
			fmt.Println("Validating config.txt data...")
		}

		validationErrs := validator.ValidatePredefined(parseResult.Config, data)
		if len(validationErrs) > 0 {
			for _, err := range validationErrs {
				fmt.Fprintf(os.Stderr, "Config.txt validation error: %v\n", err)
			}
			os.Exit(1)
		}

		predefinedData = data
		hasPredefined = true

		if *verbose {
			fmt.Printf("Loaded %d predefined assignments from config.txt\n", len(predefinedData.Assignments))
		}
	} else {
		if *verbose {
			fmt.Println("No config.txt found, scheduling all offerings normally")
		}
	}

	// Step 2: Validate config.conf
	if *verbose {
		fmt.Println("Validating config...")
	}

	validationErrors := validator.Validate(parseResult.Config)
	if len(validationErrors) > 0 {
		for _, err := range validationErrors {
			fmt.Fprintf(os.Stderr, "Validation error: %v\n", err)
		}
		// We exit for validation errors that are critical
		// Some are just warnings about feasibility
	}

	// If we have predefined data, filter out matched offerings from scheduling pool
	// and merge GU/IU entries
	if hasPredefined {
		var remainingOfferings []*types.Offering
		for _, offering := range parseResult.Config.Offerings {
			matched := false
			for _, pa := range predefinedData.Assignments {
				if pa.CourseID == offering.CourseID &&
					pa.GroupIDRaw == offering.GroupIDRaw &&
					pa.MainInstructorID == offering.MainInstructorID {
					matched = true
					break
				}
			}
			if !matched {
				remainingOfferings = append(remainingOfferings, offering)
			}
		}
		parseResult.Config.Offerings = remainingOfferings

		// Merge GU and IU from config.txt into config (for rendering and validation)
		parseResult.Config.GroupsUnavailable = append(parseResult.Config.GroupsUnavailable, predefinedData.GroupsUnavailable...)
		parseResult.Config.InstructorUnavailable = append(parseResult.Config.InstructorUnavailable, predefinedData.InstructorUnavailable...)

		if *verbose {
			fmt.Printf("Remaining offerings to schedule: %d (after removing %d predefined)\n",
				len(remainingOfferings), len(predefinedData.Assignments))
		}
	}

	// Step 3: Check basic feasibility (only if there are offerings to schedule)
	if len(parseResult.Config.Offerings) > 0 {
		if *verbose {
			fmt.Println("Checking feasibility...")
		}

		feasible, reason := validator.CanSchedule(parseResult.Config)
		if !feasible {
			fmt.Fprintf(os.Stderr, "Error: %s\n", reason)
			os.Exit(1)
		}
	} else if hasPredefined && *verbose {
		fmt.Println("No offerings to schedule (all are predefined)")
	}

	// Step 4: Schedule (only if there are offerings to schedule)
	sch := scheduler.NewScheduler(parseResult.Config, *seed, *attempts, *verbose)
	if hasPredefined {
		sch.SetPredefinedAssignments(predefinedData.Assignments)
	}

	var schedule *types.Schedule

	if len(parseResult.Config.Offerings) > 0 {
		if *verbose {
			fmt.Println("Scheduling...")
		}

		var err error
		schedule, err = sch.Schedule()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else if hasPredefined {
		// All offerings are predefined — create an empty schedule with lunch break
		if *verbose {
			fmt.Println("All offerings are predefined, creating empty schedule...")
		}
		instBreak := make(map[string]map[types.Day]int)
		groupBreak := make(map[string]map[types.Day]int)
		instPriority := make(map[string]map[types.Day]int)
		groupPriority := make(map[string]map[types.Day]int)

		// Build per-entity break maps, checking predefined conflicts and instructor/group unavailable periods
		if len(parseResult.Config.Breaks.Periods) > 0 {
			// For each instructor, pick the best break period per day
			for instID := range parseResult.Config.Instructors {
				dayBreaks := make(map[types.Day]int)
				dayPriorities := make(map[types.Day]int)
				for _, day := range types.AllDays() {
					// Check for full-day (1-13) unavailable - skip break entirely
					hasFullDayUnavailable := false
					for _, iu := range parseResult.Config.InstructorUnavailable {
						if iu.InstructorID == instID && iu.Day == day && iu.StartPeriod == 1 && iu.EndPeriod == 13 {
							hasFullDayUnavailable = true
							break
						}
					}
					if hasFullDayUnavailable {
						dayBreaks[day] = 0
						continue
					}

					found := false
					for i, bp := range parseResult.Config.Breaks.Periods {
						// Check predefined assignment conflict
						hasConflict := false
						for _, pa := range predefinedData.Assignments {
							if scheduler.IsInPredefinedPeriod(pa, day, bp) {
								hasConflict = true
								break
							}
						}
						if hasConflict {
							continue
						}

						// Check instructor_unavailable
						isUnavailable := false
						for _, iu := range parseResult.Config.InstructorUnavailable {
							if iu.InstructorID == instID && iu.Day == day && bp >= iu.StartPeriod && bp <= iu.EndPeriod {
								isUnavailable = true
								break
							}
						}
						if isUnavailable {
							continue
						}

						dayBreaks[day] = bp
						dayPriorities[day] = i + 1
						found = true
						break
					}
					if !found {
						dayBreaks[day] = 0
					}
				}
				instBreak[instID] = dayBreaks
				instPriority[instID] = dayPriorities
			}

			// For each group, pick the best break period per day
			for groupID := range parseResult.Config.Groups {
				dayBreaks := make(map[types.Day]int)
				dayPriorities := make(map[types.Day]int)
				for _, day := range types.AllDays() {
					// Check for full-day (1-13) unavailable - skip break entirely
					hasFullDayUnavailable := false
					for _, gu := range parseResult.Config.GroupsUnavailable {
						if gu.GroupID == groupID && gu.Day == day && gu.StartPeriod == 1 && gu.EndPeriod == 13 {
							hasFullDayUnavailable = true
							break
						}
					}
					if hasFullDayUnavailable {
						dayBreaks[day] = 0
						continue
					}

					found := false
					for i, bp := range parseResult.Config.Breaks.Periods {
						// Check predefined assignment conflict
						hasConflict := false
						for _, pa := range predefinedData.Assignments {
							if scheduler.IsInPredefinedPeriod(pa, day, bp) {
								hasConflict = true
								break
							}
						}
						if hasConflict {
							continue
						}

						// Check groups_unavailable
						isUnavailable := false
						for _, gu := range parseResult.Config.GroupsUnavailable {
							if gu.GroupID == groupID && gu.Day == day && bp >= gu.StartPeriod && bp <= gu.EndPeriod {
								isUnavailable = true
								break
							}
						}
						if isUnavailable {
							continue
						}

						dayBreaks[day] = bp
						dayPriorities[day] = i + 1
						found = true
						break
					}
					if !found {
						dayBreaks[day] = 0
					}
				}
				groupBreak[groupID] = dayBreaks
				groupPriority[groupID] = dayPriorities
			}
		}

		schedule = &types.Schedule{
			InstructorLunchBreak:    instBreak,
			GroupLunchBreak:         groupBreak,
			InstructorBreakPriority: instPriority,
			GroupBreakPriority:      groupPriority,
			Config:                  parseResult.Config,
		}
	}

	// Step 4.5: Resolve $ rooms in predefined assignments
	if schedule == nil {
		fmt.Fprintf(os.Stderr, "Error: no schedule to validate\n")
		os.Exit(1)
	}

	// Step 4.5: Resolve $ rooms in predefined assignments
	if hasPredefined && len(predefinedData.Assignments) > 0 {
		if *verbose {
			fmt.Println("Resolving $ rooms in predefined assignments...")
		}

		dollarErrors := sch.ResolveDollarRooms(predefinedData.Assignments, schedule)
		if len(dollarErrors) > 0 {
			for _, err := range dollarErrors {
				fmt.Fprintf(os.Stderr, "Room assignment error: %v\n", err)
			}
			os.Exit(1)
		}

		// Merge predefined assignments into the schedule
		for _, pa := range predefinedData.Assignments {
			// Resolve room IDs
			theoryRoom := pa.ResolvedTheoryRoomID
			if theoryRoom == "" || theoryRoom == "$" {
				theoryRoom = "x"
			}
			labRoom := pa.ResolvedLabRoomID
			if labRoom == "" || labRoom == "$" {
				labRoom = "x"
			}

			// Build TheoryRoomIDs / LabRoomIDs for HC-13 validation
			var theoryRoomIDs []string
			var theoryRoomRaw string
			if theoryRoom != "x" {
				theoryRoomIDs = []string{theoryRoom}
				theoryRoomRaw = theoryRoom
			} else {
				theoryRoomRaw = "x"
			}

			var labRoomIDs []string
			var labRoomRaw string
			if labRoom != "x" {
				labRoomIDs = []string{labRoom}
				labRoomRaw = labRoom
			} else {
				labRoomRaw = "x"
			}

			offering := &types.Offering{
				CourseID:         pa.CourseID,
				TheoryPeriods:    pa.TheoryPeriods,
				LabPeriods:       pa.LabPeriods,
				GroupIDs:         pa.GroupIDs,
				GroupIDRaw:       pa.GroupIDRaw,
				MainInstructorID: pa.MainInstructorID,
				CoInstructorIDs:  pa.CoInstructorIDs,
				CoInstructorRaw:  pa.CoInstructorRaw,
				TheoryRoomIDs:    theoryRoomIDs,
				TheoryRoomRaw:    theoryRoomRaw,
				LabRoomIDs:       labRoomIDs,
				LabRoomRaw:       labRoomRaw,
			}

			assignment := &types.Assignment{
				Offering:     offering,
				Day:          pa.Day,
				TheoryStart:  pa.TheoryStart,
				LabStart:     pa.LabStart,
				TheoryRoomID: theoryRoom,
				LabRoomID:    labRoom,
			}
			schedule.Assignments = append(schedule.Assignments, assignment)
		}
	}

	// Step 5: Validate schedule against all hard constraints
	if *verbose {
		fmt.Println("Validating schedule...")
	}

	hcErrors := validateHardConstraints(parseResult.Config, schedule)
	if len(hcErrors) > 0 {
		for _, err := range hcErrors {
			fmt.Fprintf(os.Stderr, "Hard constraint violation: %v\n", err)
		}
		os.Exit(1)
	}

	// Step 6: Render output
	if *verbose {
		fmt.Println("Rendering output...")
	}

	renderer := output.NewRenderer(parseResult.Config, schedule)
	content := renderer.Render()

	// Write timetable.md output file
	if err := os.WriteFile(*outputPath, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing output file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Timetable written to %s\n", *outputPath)

	// Write table.txt output file (raw data for pre-defined schedule)
	if *verbose {
		fmt.Println("Rendering table.txt...")
	}

	tableRenderer := output.NewTableRenderer(parseResult.Config, schedule)
	tableContent := tableRenderer.Render()

	tablePath := "table.txt"
	if err := os.WriteFile(tablePath, []byte(tableContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing table file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Table data written to %s\n", tablePath)
}

// printConfigFormat prints the config.conf file format documentation.
func printConfigFormat() {
	fmt.Fprintf(os.Stderr, `Config File Format (config.conf):

=== Sections and Formats ===

1. [instructor] - กำหนดอาจารย์ผู้สอน
   Format: <id> <name>
   Example:
     I001 นายสมชาย ใจดี
     I002 นางสาวสมหญิง รักเรียน

2. [groups] - กำหนดกลุ่มเรียน
   Format: <id> <term_type> <name>
     term_type: n = ปกติ (จันทร์-ศุกร์), s = พิเศษ (เสาร์-อาทิตย์)
   Example:
     G01 n ปวช.1/1
     G02 s ปวส.พิเศษ/1

3. [rooms] - กำหนดห้องเรียน
   Format: <id> <name>
   Example:
     R101 ห้องเรียน 101
     LAB1 ห้องปฏิบัติการคอมพิวเตอร์ 1

4. [courses] - กำหนดวิชา
   Format: <id> <name>
   Example:
     ENG101 ภาษาอังกฤษธุรกิจ
     PRO201 การเขียนโปรแกรมเบื้องต้น

5. [offering] - กำหนดรายวิชาที่เปิดสอน
   Format: <course_id> <theory_periods> <lab_periods> <group_ids> <main_instructor_id> <co_instructors> <theory_rooms> <lab_rooms>
     group_ids: รหัสกลุ่ม คั่นด้วยจุลภาค (,) ถ้าหลายกลุ่มให้เรียนรวมกัน
     main_instructor_id: รหัสอาจารย์หลัก, ใช้ "x" ถ้าไม่มี
     co_instructors: รหัสอาจารย์ร่วม คั่นด้วยจุลภาค, ใช้ "x" ถ้าไม่มี
     theory_rooms: ห้องทฤษฎี คั่นด้วยจุลภาค, ใช้ "x" ถ้าไม่ต้องระบุ
     lab_rooms: ห้องปฏิบัติ คั่นด้วยจุลภาค, ใช้ "x" ถ้าไม่ต้องระบุ
   Example:
     ENG101 3 0 G01 I001 x x x
     PRO201 2 3 G01,G02 I001 I002 R101 LAB1

6. [groups_unavailable] - กำหนดช่วงเวลาที่กลุ่มไม่ว่าง
   Format: <group_id> <day> <start_period-end_period> <room> <mode>
     day: mo, tu, we, th, fr, sa, su (สองตัวอักษร)
     room: รหัสห้องเรียน หรือ "none" ถ้าไม่มี
     mode: "hidden" = ไม่แสดง, หรือข้อความในเครื่องหมายคำพูด เช่น "\"กิจกรรมพิเศษ\""
   Example:
     G01 mo 1-2 none "กิจกรรมหน้าเสาธง"
     G02 fr 5-8 R101 hidden

7. [instructor_unavailable] - กำหนดช่วงเวลาที่อาจารย์ไม่ว่าง (ทุกบทบาท)
   Format: <instructor_id> <day> <start_period-end_period> <room> <mode>
     room: รหัสห้องเรียน หรือ "none" ถ้าไม่มี
     mode: "hidden" = ไม่แสดง, หรือข้อความ
   Example:
     I001 we 1-4 none "ประชุม"
     I002 sa 1-13 R101 hidden

8. [instructor_unavailable_main] - กำหนดช่วงเวลาที่อาจารย์ไม่สามารถเป็นอาจารย์หลักได้
   Format: <instructor_id> <day> <start_period-end_period>
   Example:
     I001 tu 5-8

9. [instructor_nolate] - กำหนดช่วงเวลาที่อาจารย์ไม่สามารถสอนในช่วงท้าย
   Format: <instructor_id> <day> <period_threshold>
     period_threshold: ตั้งแต่คาบนี้ขึ้นไป อาจารย์ไม่สามารถสอนได้
   Example:
     I001 fr 9

10. [break] - กำหนดช่วงพัก (break) เรียงลำดับตามความสำคัญ
    Format: <period_number>
      ตัวแรกมีลำดับความสำคัญสูงสุด
    Example:
      6
      7

=== Day Codes ===
  mo = จันทร์, tu = อังคาร, we = พุธ, th = พฤหัสบดี
  fr = ศุกร์, sa = เสาร์, su = อาทิตย์

=== Periods ===
  มีทั้งหมด 13 คาบต่อวัน (1-13)

=== Notes ===
  - คอมเมนต์เริ่มต้นด้วย # ไปจนจบบรรทัด
  - แต่ละฟิลด์คั่นด้วยช่องว่าง (space/tab)
  - ค่า "x" หมายถึง ไม่ได้ระบุ/ไม่มี
  - กรุณาเรียงลำดับ day codes ในแต่ละส่วนเพื่อให้อ่านง่าย

`)
}

// validateHardConstraints checks all hard constraints on the final schedule.
func validateHardConstraints(config *types.Config, schedule *types.Schedule) []error {
	var errors []error

	// HC-1: No double-booking of instructors
	for _, a := range schedule.Assignments {
		for _, a2 := range schedule.Assignments {
			if a == a2 {
				continue
			}
			if a.Day != a2.Day {
				continue
			}
			// Check for instructor overlap
			instA := a.Offering.MainInstructorID
			instA2 := a2.Offering.MainInstructorID

			for p := 1; p <= types.MaxPeriodsPerDay; p++ {
				if !a.ContainsPeriod(a.Day, p) || !a2.ContainsPeriod(a2.Day, p) {
					continue
				}

				// Check main instructors
				if instA != "x" && instA2 != "x" && instA == instA2 {
					errors = append(errors, fmt.Errorf("HC-1: instructor '%s' double-booked on %s period %d (%s and %s)",
						instA, a.Day, p, a.Offering.CourseID, a2.Offering.CourseID))
					continue
				}

				// Check co-instructors
				for _, coA := range a.Offering.CoInstructorIDs {
					if a.IsLabPeriod(p) && coA == instA2 {
						errors = append(errors, fmt.Errorf("HC-1: instructor '%s' double-booked (co) on %s period %d",
							coA, a.Day, p))
					}
					for _, coA2 := range a2.Offering.CoInstructorIDs {
						// Both must be in lab periods for co-co conflict
						if a.IsLabPeriod(p) && a2.IsLabPeriod(p) && coA == coA2 {
							errors = append(errors, fmt.Errorf("HC-1: instructor '%s' double-booked (co-co) on %s period %d",
								coA, a.Day, p))
						}
					}
				}
			}
		}
	}

	// HC-2: No double-booking of groups
	for _, a := range schedule.Assignments {
		for _, a2 := range schedule.Assignments {
			if a == a2 {
				continue
			}
			if a.Day != a2.Day {
				continue
			}
			for p := 1; p <= types.MaxPeriodsPerDay; p++ {
				if !a.ContainsPeriod(a.Day, p) || !a2.ContainsPeriod(a2.Day, p) {
					continue
				}
				for _, gid := range a.Offering.GroupIDs {
					for _, gid2 := range a2.Offering.GroupIDs {
						if gid == gid2 {
							errors = append(errors, fmt.Errorf("HC-2: group '%s' double-booked on %s period %d (%s and %s)",
								gid, a.Day, p, a.Offering.CourseID, a2.Offering.CourseID))
						}
					}
				}
			}
		}
	}

	// HC-3: No double-booking of rooms
	for _, a := range schedule.Assignments {
		for _, a2 := range schedule.Assignments {
			if a == a2 {
				continue
			}
			if a.Day != a2.Day {
				continue
			}
			for p := 1; p <= types.MaxPeriodsPerDay; p++ {
				if !a.ContainsPeriod(a.Day, p) || !a2.ContainsPeriod(a2.Day, p) {
					continue
				}

				aRoom := ""
				if a.IsTheoryPeriod(p) {
					aRoom = a.TheoryRoomID
				} else if a.IsLabPeriod(p) {
					aRoom = a.LabRoomID
				}

				a2Room := ""
				if a2.IsTheoryPeriod(p) {
					a2Room = a2.TheoryRoomID
				} else if a2.IsLabPeriod(p) {
					a2Room = a2.LabRoomID
				}

				if aRoom != "" && aRoom != "x" && a2Room != "" && a2Room != "x" && aRoom == a2Room {
					errors = append(errors, fmt.Errorf("HC-3: room '%s' double-booked on %s period %d (%s and %s)",
						aRoom, a.Day, p, a.Offering.CourseID, a2.Offering.CourseID))
				}
			}
		}
	}

	// HC-4: Contiguity
	for _, a := range schedule.Assignments {
		if a.TheoryStart > 0 {
			for p := a.TheoryStart; p < a.TheoryStart+a.Offering.TheoryPeriods; p++ {
				if !a.ContainsPeriod(a.Day, p) {
					errors = append(errors, fmt.Errorf("HC-4: %s theory not contiguous on %s", a.Offering.CourseID, a.Day))
					break
				}
			}
		}
		if a.LabStart > 0 {
			for p := a.LabStart; p < a.LabStart+a.Offering.LabPeriods; p++ {
				if !a.ContainsPeriod(a.Day, p) {
					errors = append(errors, fmt.Errorf("HC-4: %s lab not contiguous on %s", a.Offering.CourseID, a.Day))
					break
				}
			}
		}
	}

	// HC-5: Theory before lab, same day
	for _, a := range schedule.Assignments {
		if a.TheoryStart > 0 && a.LabStart > 0 {
			if a.TheoryStart+a.Offering.TheoryPeriods != a.LabStart {
				errors = append(errors, fmt.Errorf("HC-5: %s theory and lab not contiguous on %s", a.Offering.CourseID, a.Day))
			}
		}
	}

	// HC-6: Group term type restrictions
	for _, a := range schedule.Assignments {
		for _, gid := range a.Offering.GroupIDs {
			if grp, ok := config.Groups[gid]; ok {
				if grp.TermType == types.Normal && (a.Day == types.Saturday || a.Day == types.Sunday) {
					errors = append(errors, fmt.Errorf("HC-6: normal group '%s' scheduled on weekend %s", gid, a.Day))
				}
				if grp.TermType == types.Special && (a.Day != types.Saturday && a.Day != types.Sunday) {
					errors = append(errors, fmt.Errorf("HC-6: special group '%s' scheduled on weekday %s", gid, a.Day))
				}
			}
		}
	}

	// HC-7: Main instructor teaches everything, co-instructor only lab
	// This is about role assignment, not schedule - checked at schedule time

	// HC-8: Groups unavailable
	for _, a := range schedule.Assignments {
		for _, gid := range a.Offering.GroupIDs {
			for _, gu := range config.GroupsUnavailable {
				if gu.GroupID != gid || gu.Day != a.Day {
					continue
				}
				for p := 1; p <= types.MaxPeriodsPerDay; p++ {
					if !a.ContainsPeriod(a.Day, p) {
						continue
					}
					if p >= gu.StartPeriod && p <= gu.EndPeriod {
						errors = append(errors, fmt.Errorf("HC-8: group '%s' unavailable on %s period %d (%s scheduled)",
							gid, a.Day, p, a.Offering.CourseID))
					}
				}
			}
		}
	}

	// HC-9: Instructor unavailable (full)
	for _, a := range schedule.Assignments {
		for p := 1; p <= types.MaxPeriodsPerDay; p++ {
			if !a.ContainsPeriod(a.Day, p) {
				continue
			}
			if a.Offering.MainInstructorID != "x" {
				for _, iu := range config.InstructorUnavailable {
					if iu.InstructorID == a.Offering.MainInstructorID && iu.Day == a.Day &&
						p >= iu.StartPeriod && p <= iu.EndPeriod {
						errors = append(errors, fmt.Errorf("HC-9: instructor '%s' unavailable on %s period %d",
							iu.InstructorID, a.Day, p))
					}
				}
			}
			if a.IsLabPeriod(p) {
				for _, coID := range a.Offering.CoInstructorIDs {
					for _, iu := range config.InstructorUnavailable {
						if iu.InstructorID == coID && iu.Day == a.Day &&
							p >= iu.StartPeriod && p <= iu.EndPeriod {
							errors = append(errors, fmt.Errorf("HC-9: instructor '%s' unavailable on %s period %d",
								iu.InstructorID, a.Day, p))
						}
					}
				}
			}
		}
	}

	// HC-10: Instructor unavailable for main role
	for _, a := range schedule.Assignments {
		if a.Offering.MainInstructorID != "x" {
			for p := 1; p <= types.MaxPeriodsPerDay; p++ {
				if !a.ContainsPeriod(a.Day, p) {
					continue
				}
				for _, iu := range config.InstructorUnavailableMain {
					if iu.InstructorID == a.Offering.MainInstructorID && iu.Day == a.Day &&
						p >= iu.StartPeriod && p <= iu.EndPeriod {
						errors = append(errors, fmt.Errorf("HC-10: instructor '%s' unavailable as main on %s period %d",
							iu.InstructorID, a.Day, p))
					}
				}
			}
		}
	}

	// HC-11: Instructor no-late periods
	for _, a := range schedule.Assignments {
		if a.Offering.MainInstructorID != "x" {
			for _, inl := range config.InstructorNoLate {
				if inl.InstructorID == a.Offering.MainInstructorID && inl.Day == a.Day {
					startPeriod := a.TheoryStart
					if startPeriod == 0 {
						startPeriod = a.LabStart
					}
					totalPeriods := a.Offering.TheoryPeriods + a.Offering.LabPeriods
					endPeriod := startPeriod + totalPeriods - 1
					if endPeriod >= inl.PeriodThreshold {
						errors = append(errors, fmt.Errorf("HC-11: instructor '%s' cannot teach in nolate zone from period %d onwards on %s (covers periods %d-%d)",
							inl.InstructorID, inl.PeriodThreshold, a.Day, startPeriod, endPeriod))
					}
				}
			}
		}
	}

	// HC-12: Lunch break - no assignments during break (per-entity check)
	// Check each assignment against involved entities' breaks
	for _, a := range schedule.Assignments {
		o := a.Offering
		day := a.Day
		for p := 1; p <= types.MaxPeriodsPerDay; p++ {
			if !a.ContainsPeriod(day, p) {
				continue
			}
			// Check main instructor's break
			if o.MainInstructorID != "x" {
				if dayBreaks, ok := schedule.InstructorLunchBreak[o.MainInstructorID]; ok {
					if dayBreaks[day] == p {
						errors = append(errors, fmt.Errorf("HC-12: assignment on break period %d %s for instructor '%s' (%s)",
							p, day, o.MainInstructorID, o.CourseID))
					}
				}
			}
			// Check co-instructors' breaks (lab periods only)
			if a.IsLabPeriod(p) {
				for _, coID := range o.CoInstructorIDs {
					if dayBreaks, ok := schedule.InstructorLunchBreak[coID]; ok {
						if dayBreaks[day] == p {
							errors = append(errors, fmt.Errorf("HC-12: assignment on break period %d %s for instructor '%s' (%s)",
								p, day, coID, o.CourseID))
						}
					}
				}
			}
			// Check groups' breaks
			for _, gid := range o.GroupIDs {
				if dayBreaks, ok := schedule.GroupLunchBreak[gid]; ok {
					if dayBreaks[day] == p {
						errors = append(errors, fmt.Errorf("HC-12: assignment on break period %d %s for group '%s' (%s)",
							p, day, gid, o.CourseID))
					}
				}
			}
		}
	}

	// HC-13: Room must be in allowed list
	for _, a := range schedule.Assignments {
		if a.TheoryRoomID != "" && a.TheoryRoomID != "x" {
			found := false
			for _, rid := range a.Offering.TheoryRoomIDs {
				if rid == a.TheoryRoomID {
					found = true
					break
				}
			}
			if !found {
				errors = append(errors, fmt.Errorf("HC-13: room '%s' not in theory room list for %s", a.TheoryRoomID, a.Offering.CourseID))
			}
		}
		if a.LabRoomID != "" && a.LabRoomID != "x" {
			found := false
			for _, rid := range a.Offering.LabRoomIDs {
				if rid == a.LabRoomID {
					found = true
					break
				}
			}
			if !found {
				errors = append(errors, fmt.Errorf("HC-13: room '%s' not in lab room list for %s", a.LabRoomID, a.Offering.CourseID))
			}
		}
	}

	// HC-14: Period bounds
	for _, a := range schedule.Assignments {
		if a.TheoryStart+a.Offering.TheoryPeriods-1 > types.MaxPeriodsPerDay {
			errors = append(errors, fmt.Errorf("HC-14: %s theory exceeds max period", a.Offering.CourseID))
		}
		if a.LabStart+a.Offering.LabPeriods-1 > types.MaxPeriodsPerDay {
			errors = append(errors, fmt.Errorf("HC-14: %s lab exceeds max period", a.Offering.CourseID))
		}
	}

	// HC-15: All offerings must be scheduled
	// Check from the number of assignments
	scheduled := make(map[string]bool)
	for _, a := range schedule.Assignments {
		key := a.Offering.CourseID + "|" + a.Offering.GroupIDRaw + "|" + a.Offering.MainInstructorID
		scheduled[key] = true
	}
	for _, offering := range config.Offerings {
		key := offering.CourseID + "|" + offering.GroupIDRaw + "|" + offering.MainInstructorID
		if !scheduled[key] {
			errors = append(errors, fmt.Errorf("HC-15: offering '%s %s' not scheduled", offering.CourseID, offering.GroupIDRaw))
		}
	}

	// Remove duplicates
	return deduplicateErrors(errors)
}

func deduplicateErrors(errs []error) []error {
	seen := make(map[string]bool)
	var result []error
	for _, e := range errs {
		msg := e.Error()
		if !seen[msg] {
			seen[msg] = true
			result = append(result, e)
		}
	}
	return result
}
