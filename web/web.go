// Package web provides the web server for the timetable scheduling application.
package web

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"timetablex/output"
	"timetablex/parser"
	"timetablex/scheduler"
	"timetablex/types"
	"timetablex/validator"
)

// Server holds the web server state.
type Server struct {
	port          int
	defaultAttemps int
	server        *http.Server
}

// NewServer creates a new web server.
func NewServer(port int, defaultAttempts int) *Server {
	return &Server{
		port:           port,
		defaultAttemps: defaultAttempts,
	}
}

// Start starts the web server.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/schedule", s.handleSchedule)

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	log.Printf("Web server started at http://localhost:%d", s.port)
	return s.server.ListenAndServe()
}

// HandleFunc wrappers that add the server context.

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleSchedule(w, r)
		return
	}
	s.renderPage(w, "timetable", map[string]interface{}{
		"DefaultAttempts": s.defaultAttemps,
		"Error":           "",
		"Result":          nil,
	})
}

func (s *Server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Parse multipart form (max 32 MB)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		s.renderPage(w, "timetable", map[string]interface{}{
			"DefaultAttempts": s.defaultAttemps,
			"Error":           fmt.Sprintf("Error parsing form: %v", err),
			"Result":          nil,
		})
		return
	}

	// Get config.conf file (required)
	configFile, configHeader, err := r.FormFile("config_conf")
	if err != nil {
		s.renderPage(w, "timetable", map[string]interface{}{
			"DefaultAttempts": s.defaultAttemps,
			"Error":           "กรุณาอัปโหลดไฟล์ config.conf (จำเป็น)",
			"Result":          nil,
		})
		return
	}
	defer configFile.Close()

	// Save config.conf to temp file
	configData, err := io.ReadAll(configFile)
	if err != nil {
		s.renderPage(w, "timetable", map[string]interface{}{
			"DefaultAttempts": s.defaultAttemps,
			"Error":           fmt.Sprintf("Error reading config.conf: %v", err),
			"Result":          nil,
		})
		return
	}

	tmpDir, err := os.MkdirTemp("", "timetablex-*")
	if err != nil {
		s.renderPage(w, "timetable", map[string]interface{}{
			"DefaultAttempts": s.defaultAttemps,
			"Error":           fmt.Sprintf("Error creating temp directory: %v", err),
			"Result":          nil,
		})
		return
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.conf")
	if err := os.WriteFile(configPath, configData, 0644); err != nil {
		s.renderPage(w, "timetable", map[string]interface{}{
			"DefaultAttempts": s.defaultAttemps,
			"Error":           fmt.Sprintf("Error writing config.conf: %v", err),
			"Result":          nil,
		})
		return
	}

	// Get config.txt file (optional)
	predefinedPath := ""
	configTxtFile, _, err := r.FormFile("config_txt")
	if err == nil {
		defer configTxtFile.Close()
		txtData, err := io.ReadAll(configTxtFile)
		if err != nil {
			s.renderPage(w, "timetable", map[string]interface{}{
				"DefaultAttempts": s.defaultAttemps,
				"Error":           fmt.Sprintf("Error reading config.txt: %v", err),
				"Result":          nil,
			})
			return
		}
		predefinedPath = filepath.Join(tmpDir, "config.txt")
		if err := os.WriteFile(predefinedPath, txtData, 0644); err != nil {
			s.renderPage(w, "timetable", map[string]interface{}{
				"DefaultAttempts": s.defaultAttemps,
				"Error":           fmt.Sprintf("Error writing config.txt: %v", err),
				"Result":          nil,
			})
			return
		}
	}

	// Get attempts parameter
	attemptsStr := r.FormValue("attempts")
	attempts := s.defaultAttemps
	if attemptsStr != "" {
		if v, err := strconv.Atoi(attemptsStr); err == nil && v > 0 {
			attempts = v
		}
	}

	// Run scheduling
	mdContent, txtContent, errStr := runScheduling(configPath, predefinedPath, attempts)
	if errStr != "" {
		s.renderPage(w, "timetable", map[string]interface{}{
			"DefaultAttempts": s.defaultAttemps,
			"Error":           errStr,
			"Result":          nil,
		})
		return
	}

	// Store results in the session (use temp files for download)
	mdPath := filepath.Join(tmpDir, "timetable.md")
	txtPath := filepath.Join(tmpDir, "table.txt")

	if err := os.WriteFile(mdPath, []byte(mdContent), 0644); err != nil {
		s.renderPage(w, "timetable", map[string]interface{}{
			"DefaultAttempts": s.defaultAttemps,
			"Error":           fmt.Sprintf("Error writing timetable.md: %v", err),
			"Result":          nil,
		})
		return
	}
	if err := os.WriteFile(txtPath, []byte(txtContent), 0644); err != nil {
		s.renderPage(w, "timetable", map[string]interface{}{
			"DefaultAttempts": s.defaultAttemps,
			"Error":           fmt.Sprintf("Error writing table.txt: %v", err),
			"Result":          nil,
		})
		return
	}

	// Instead of keeping temp files around, encode the content into the result page
	// Use the temp dir path for download serving
	// We'll serve download via a session-based approach: encode content in the result page
	// or serve from temp. Since we need to keep temp files alive for download,
	// let's encode the actual content in the result for simplicity.
	result := &ScheduleResult{
		MDContent:  mdContent,
		TxtContent: txtContent,
		ConfigName: configHeader.Filename,
	}

	s.renderPage(w, "timetable", map[string]interface{}{
		"DefaultAttempts": s.defaultAttemps,
		"Error":           "",
		"Result":          result,
	})
}

// ScheduleResult holds the scheduling output content.
type ScheduleResult struct {
	MDContent  string
	TxtContent string
	ConfigName string
}

// runScheduling performs the full scheduling workflow given file paths.
func runScheduling(configPath, predefinedPath string, attempts int) (mdContent, txtContent, errStr string) {
	seed := time.Now().UnixNano()
	verbose := false

	// Step 1: Parse config.conf
	parseResult := parser.Parse(configPath)
	if len(parseResult.Errors) > 0 {
		var buf bytes.Buffer
		for _, err := range parseResult.Errors {
			buf.WriteString(fmt.Sprintf("Error: %v\n", err))
		}
		return "", "", buf.String()
	}

	if !parseResult.HasSections() {
		return "", "", "Error: no valid data found in config file"
	}

	// Step 1.5: Check for config.txt (pre-defined schedule)
	var predefinedData *types.PredefinedData
	hasPredefined := false

	if predefinedPath != "" {
		if _, err := os.Stat(predefinedPath); err == nil {
			data, parseErrs := parser.ParsePredefined(predefinedPath)
			if len(parseErrs) > 0 {
				var buf bytes.Buffer
				for _, err := range parseErrs {
					buf.WriteString(fmt.Sprintf("Config.txt error: %v\n", err))
				}
				return "", "", buf.String()
			}

			validationErrs := validator.ValidatePredefined(parseResult.Config, data)
			if len(validationErrs) > 0 {
				var buf bytes.Buffer
				for _, err := range validationErrs {
					buf.WriteString(fmt.Sprintf("Config.txt validation error: %v\n", err))
				}
				return "", "", buf.String()
			}

			predefinedData = data
			hasPredefined = true
		}
	}

	// Step 2: Validate config.conf
	validationErrors := validator.Validate(parseResult.Config)
	if len(validationErrors) > 0 {
		// Some validation errors are just warnings, continue
		for _, err := range validationErrors {
			if verbose {
				fmt.Printf("Validation warning: %v\n", err)
			}
		}
	}

	// If we have predefined data, filter out matched offerings
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

		parseResult.Config.GroupsUnavailable = append(parseResult.Config.GroupsUnavailable, predefinedData.GroupsUnavailable...)
		parseResult.Config.InstructorUnavailable = append(parseResult.Config.InstructorUnavailable, predefinedData.InstructorUnavailable...)
	}

	// Step 3: Check basic feasibility
	if len(parseResult.Config.Offerings) > 0 {
		feasible, reason := validator.CanSchedule(parseResult.Config)
		if !feasible {
			return "", "", fmt.Sprintf("Error: %s", reason)
		}
	}

	// Step 4: Schedule
	sch := scheduler.NewScheduler(parseResult.Config, seed, attempts, verbose)
	if hasPredefined {
		sch.SetPredefinedAssignments(predefinedData.Assignments)
	}

	var schedule *types.Schedule

	if len(parseResult.Config.Offerings) > 0 {
		var err error
		schedule, err = sch.Schedule()
		if err != nil {
			return "", "", fmt.Sprintf("Error: %v", err)
		}
	} else if hasPredefined {
		lunchBreak := make(map[types.Day]int)
		if len(parseResult.Config.Breaks.Periods) > 0 {
			chosenBreak := 0
			for _, bp := range parseResult.Config.Breaks.Periods {
				hasConflict := false
				for _, pa := range predefinedData.Assignments {
					if scheduler.IsInPredefinedPeriod(pa, bp) {
						hasConflict = true
						break
					}
				}
				if !hasConflict {
					chosenBreak = bp
					break
				}
			}
			if chosenBreak == 0 && len(parseResult.Config.Breaks.Periods) > 0 {
				chosenBreak = parseResult.Config.Breaks.Periods[0]
			}
			for _, day := range types.AllDays() {
				lunchBreak[day] = chosenBreak
			}
		}
		schedule = &types.Schedule{
			LunchBreakDay: lunchBreak,
			Config:        parseResult.Config,
		}
	}

	if schedule == nil {
		return "", "", "Error: no schedule to validate"
	}

	// Step 4.5: Resolve $ rooms in predefined assignments
	if hasPredefined && len(predefinedData.Assignments) > 0 {
		dollarErrors := sch.ResolveDollarRooms(predefinedData.Assignments, schedule)
		if len(dollarErrors) > 0 {
			var buf bytes.Buffer
			for _, err := range dollarErrors {
				buf.WriteString(fmt.Sprintf("Room assignment error: %v\n", err))
			}
			return "", "", buf.String()
		}

		for _, pa := range predefinedData.Assignments {
			theoryRoom := pa.ResolvedTheoryRoomID
			if theoryRoom == "" || theoryRoom == "$" {
				theoryRoom = "x"
			}
			labRoom := pa.ResolvedLabRoomID
			if labRoom == "" || labRoom == "$" {
				labRoom = "x"
			}

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
	hcErrors := validateHardConstraints(parseResult.Config, schedule)
	if len(hcErrors) > 0 {
		var buf bytes.Buffer
		for _, err := range hcErrors {
			buf.WriteString(fmt.Sprintf("Hard constraint violation: %v\n", err))
		}
		return "", "", buf.String()
	}

	// Step 6: Render output
	renderer := output.NewRenderer(parseResult.Config, schedule)
	mdContent = renderer.Render()

	tableRenderer := output.NewTableRenderer(parseResult.Config, schedule)
	txtContent = tableRenderer.Render()

	return mdContent, txtContent, ""
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
			instA := a.Offering.MainInstructorID
			instA2 := a2.Offering.MainInstructorID

			for p := 1; p <= types.MaxPeriodsPerDay; p++ {
				if !a.ContainsPeriod(a.Day, p) || !a2.ContainsPeriod(a2.Day, p) {
					continue
				}

				if instA != "x" && instA2 != "x" && instA == instA2 {
					errors = append(errors, fmt.Errorf("HC-1: instructor '%s' double-booked on %s period %d (%s and %s)",
						instA, a.Day, p, a.Offering.CourseID, a2.Offering.CourseID))
					continue
				}

				for _, coA := range a.Offering.CoInstructorIDs {
					if a.IsLabPeriod(p) && coA == instA2 {
						errors = append(errors, fmt.Errorf("HC-1: instructor '%s' double-booked (co) on %s period %d",
							coA, a.Day, p))
					}
					for _, coA2 := range a2.Offering.CoInstructorIDs {
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

	// HC-12: Lunch break - no assignments during break
	for day, period := range schedule.LunchBreakDay {
		for _, a := range schedule.Assignments {
			if a.Day == day && a.ContainsPeriod(day, period) {
				errors = append(errors, fmt.Errorf("HC-12: assignment on break period %d %s (%s)", period, day, a.Offering.CourseID))
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

// renderPage renders an HTML page with the given template name and data.
func (s *Server) renderPage(w http.ResponseWriter, tmplName string, data map[string]interface{}) {
	tmpl := template.Must(template.New("page").Funcs(template.FuncMap{
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s)
		},
		"urlencode": func(s string) string {
			return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
		},
	}).Parse(pageTemplate))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
	}
}

const pageTemplate = `<!DOCTYPE html>
<html lang="th">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>TimetableX - จัดตารางสอน</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Playpen+Sans+Thai:wght@100;400;500;600;700&display=swap" rel="stylesheet">
<style>
:root {
  --bg0: #282828;
  --bg1: #3c3836;
  --bg2: #504945;
  --fg0: #ebdbb2;
  --fg1: #d5c4a1;
  --fg2: #bdae93;
  --red: #cc241d;
  --green: #98971a;
  --yellow: #d79921;
  --blue: #458588;
  --purple: #b16286;
  --aqua: #689d6a;
  --orange: #d65d0e;
  --gray: #928374;
  --bg0_h: #1d2021;
}

* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

body {
  font-family: 'Playpen Sans Thai', cursive, sans-serif;
  background-color: var(--bg0);
  color: var(--fg0);
  min-height: 100vh;
  line-height: 1.7;
}

.container {
  max-width: 960px;
  margin: 0 auto;
  padding: 2rem 1.5rem;
}

header {
  text-align: center;
  padding: 2.5rem 0 1.5rem;
  border-bottom: 2px solid var(--bg2);
  margin-bottom: 2rem;
}

header h1 {
  font-size: 2.2rem;
  font-weight: 700;
  color: var(--orange);
  letter-spacing: 0.02em;
  margin-bottom: 0.3rem;
}

header p {
  color: var(--gray);
  font-size: 0.95rem;
}

.section {
  background: var(--bg1);
  border-radius: 12px;
  padding: 1.8rem;
  margin-bottom: 1.8rem;
  border: 1px solid var(--bg2);
  transition: border-color 0.2s;
}

.section:hover {
  border-color: var(--gray);
}

.section h2 {
  font-size: 1.3rem;
  font-weight: 600;
  color: var(--yellow);
  margin-bottom: 1rem;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.section h2 .badge {
  font-size: 0.7rem;
  font-weight: 500;
  padding: 0.15rem 0.5rem;
  border-radius: 6px;
  background: var(--bg2);
  color: var(--gray);
}

.section h2 .required {
  background: var(--red);
  color: var(--fg0);
}

.section h2 .optional {
  background: var(--blue);
  color: var(--fg0);
}

.form-group {
  margin-bottom: 1.2rem;
}

.form-group:last-child {
  margin-bottom: 0;
}

label {
  display: block;
  font-weight: 500;
  margin-bottom: 0.5rem;
  color: var(--fg1);
  font-size: 0.95rem;
}

input[type="file"],
input[type="number"] {
  width: 100%;
  padding: 0.75rem 1rem;
  background: var(--bg0);
  border: 2px solid var(--bg2);
  border-radius: 8px;
  color: var(--fg0);
  font-family: 'Playpen Sans Thai', cursive, sans-serif;
  font-size: 0.95rem;
  transition: border-color 0.2s, box-shadow 0.2s;
}

input[type="file"]:focus,
input[type="number"]:focus {
  outline: none;
  border-color: var(--blue);
  box-shadow: 0 0 0 3px rgba(69, 133, 136, 0.2);
}

input[type="file"]::file-selector-button {
  padding: 0.4rem 0.9rem;
  border: none;
  border-radius: 6px;
  background: var(--blue);
  color: var(--fg0);
  font-family: 'Playpen Sans Thai', cursive, sans-serif;
  font-weight: 500;
  font-size: 0.85rem;
  cursor: pointer;
  margin-right: 0.8rem;
  transition: background 0.2s;
}

input[type="file"]::file-selector-button:hover {
  background: var(--aqua);
}

input[type="number"] {
  width: 180px;
}

.submit-btn {
  display: block;
  width: 100%;
  padding: 1rem;
  background: var(--green);
  color: var(--fg0);
  border: none;
  border-radius: 10px;
  font-family: 'Playpen Sans Thai', cursive, sans-serif;
  font-size: 1.15rem;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s, transform 0.1s;
  letter-spacing: 0.03em;
}

.submit-btn:hover {
  background: var(--aqua);
  transform: translateY(-1px);
}

.submit-btn:active {
  transform: translateY(0);
}

.submit-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
  transform: none;
}

.error-box {
  background: rgba(204, 36, 45, 0.15);
  border: 1px solid var(--red);
  border-radius: 10px;
  padding: 1rem 1.3rem;
  margin-bottom: 1.5rem;
  color: var(--red);
  font-weight: 500;
  white-space: pre-wrap;
}

.success-box {
  background: rgba(152, 151, 26, 0.15);
  border: 1px solid var(--green);
  border-radius: 10px;
  padding: 1.5rem;
  margin-bottom: 1.5rem;
}

.success-box h3 {
  color: var(--green);
  font-size: 1.2rem;
  margin-bottom: 0.5rem;
}

.success-box p {
  color: var(--fg1);
  margin-bottom: 1rem;
}

.download-links {
  display: flex;
  gap: 1rem;
  flex-wrap: wrap;
}

.download-btn {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.8rem 1.5rem;
  border-radius: 8px;
  text-decoration: none;
  font-weight: 600;
  font-family: 'Playpen Sans Thai', cursive, sans-serif;
  font-size: 0.95rem;
  transition: background 0.2s, transform 0.1s;
}

.download-btn:hover {
  transform: translateY(-1px);
}

.download-btn.md {
  background: var(--blue);
  color: var(--fg0);
}

.download-btn.md:hover {
  background: #5299a0;
}

.download-btn.txt {
  background: var(--purple);
  color: var(--fg0);
}

.download-btn.txt:hover {
  background: #c978a9;
}

/* Format description styles */
.format-section {
  margin-top: 2.5rem;
}

.format-section h2 {
  font-size: 1.3rem;
  font-weight: 600;
  color: var(--yellow);
  margin-bottom: 1rem;
  border-bottom: 1px solid var(--bg2);
  padding-bottom: 0.5rem;
}

.format-block {
  background: var(--bg0);
  border-radius: 8px;
  padding: 1.2rem 1.4rem;
  margin-bottom: 1.2rem;
  border-left: 3px solid var(--blue);
}

.format-block h3 {
  color: var(--orange);
  font-size: 1.05rem;
  margin-bottom: 0.5rem;
  font-weight: 600;
}

.format-block .code {
  font-family: 'Playpen Sans Thai', monospace;
  font-size: 0.85rem;
  color: var(--fg2);
  background: var(--bg1);
  padding: 0.15rem 0.4rem;
  border-radius: 4px;
  display: inline-block;
}

.format-block pre {
  font-family: 'Playpen Sans Thai', monospace;
  font-size: 0.82rem;
  color: var(--fg2);
  background: var(--bg1);
  padding: 0.8rem 1rem;
  border-radius: 6px;
  margin-top: 0.5rem;
  overflow-x: auto;
  line-height: 1.5;
  white-space: pre;
}

.format-block .example-label {
  color: var(--gray);
  font-size: 0.78rem;
  margin-top: 0.6rem;
  margin-bottom: 0.3rem;
}

.format-block ul {
  list-style: none;
  padding: 0;
}

.format-block ul li {
  color: var(--fg2);
  font-size: 0.88rem;
  padding: 0.2rem 0;
  padding-left: 1rem;
  position: relative;
}

.format-block ul li::before {
  content: "•";
  color: var(--aqua);
  position: absolute;
  left: 0;
}

footer {
  text-align: center;
  padding: 2rem 0;
  color: var(--gray);
  font-size: 0.85rem;
}

.loading {
  display: none;
  text-align: center;
  padding: 2rem;
}

.loading .spinner {
  width: 40px;
  height: 40px;
  border: 4px solid var(--bg2);
  border-top-color: var(--orange);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
  margin: 0 auto 1rem;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.loading p {
  color: var(--gray);
}
</style>
</head>
<body>
<div class="container">
  <header>
    <h1>📅 TimetableX</h1>
    <p>ระบบจัดตารางสอนอัตโนมัติ</p>
  </header>

  {{if .Error}}
  <div class="error-box">{{.Error}}</div>
  {{end}}

  {{if .Result}}
  <div class="success-box">
    <h3>✅ จัดตารางเรียนสำเร็จ</h3>
    <p>ตารางสอนสำหรับ "{{.Result.ConfigName}}" พร้อมดาวน์โหลดแล้ว</p>
    <div class="download-links">
      <a class="download-btn md" href="data:text/markdown;charset=utf-8,{{.Result.MDContent | urlencode}}" download="timetable.md">📄 ดาวน์โหลด timetable.md</a>
      <a class="download-btn txt" href="data:text/plain;charset=utf-8,{{.Result.TxtContent | urlencode}}" download="table.txt">📋 ดาวน์โหลด table.txt</a>
    </div>
  </div>
  {{end}}

  <form method="post" action="/schedule" enctype="multipart/form-data" id="scheduleForm">
    <div class="section">
      <h2>
        📄 ไฟล์ตั้งค่า config.conf
        <span class="badge required">จำเป็น</span>
      </h2>
      <div class="form-group">
        <label for="config_conf">เลือกไฟล์ config.conf</label>
        <input type="file" id="config_conf" name="config_conf" accept=".conf,.txt" required>
      </div>
    </div>

    <div class="section">
      <h2>
        📄 ไฟล์ตาราง predefined config.txt
        <span class="badge optional">ไม่จำเป็น</span>
      </h2>
      <div class="form-group">
        <label for="config_txt">เลือกไฟล์ config.txt (ถ้ามี)</label>
        <input type="file" id="config_txt" name="config_txt" accept=".txt">
      </div>
    </div>

    <div class="section">
      <h2>⚙️ ตั้งค่า</h2>
      <div class="form-group">
        <label for="attempts">จำนวนรอบในการจัดตาราง (Attempts)</label>
        <input type="number" id="attempts" name="attempts" value="{{.DefaultAttempts}}" min="1" max="10000">
      </div>
    </div>

    <button type="submit" class="submit-btn" id="submitBtn">🚀 เริ่มจัดตาราง</button>

    <div class="loading" id="loading">
      <div class="spinner"></div>
      <p>กำลังจัดตาราง กรุณารอสักครู่...</p>
    </div>
  </form>

  <div class="format-section">
    <h2>📖 โครงสร้างไฟล์ตั้งค่า</h2>

    <div class="format-block">
      <h3>📁 ไฟล์ config.conf</h3>
      <p style="color:var(--fg2);font-size:0.9rem;margin-bottom:0.6rem;">ไฟล์หลักสำหรับกำหนดข้อมูลอาจารย์ กลุ่มเรียน ห้องเรียน วิชา และรายวิชาที่เปิดสอน</p>
      <ul>
        <li><strong>[instructor]</strong> — กำหนดอาจารย์ผู้สอน <span class="code">&lt;id&gt; &lt;name&gt;</span></li>
        <li><strong>[groups]</strong> — กำหนดกลุ่มเรียน <span class="code">&lt;id&gt; &lt;term_type(n/s)&gt; &lt;name&gt;</span></li>
        <li><strong>[rooms]</strong> — กำหนดห้องเรียน <span class="code">&lt;id&gt; &lt;name&gt;</span></li>
        <li><strong>[courses]</strong> — กำหนดวิชา <span class="code">&lt;id&gt; &lt;name&gt;</span></li>
        <li><strong>[offering]</strong> — กำหนดรายวิชาที่เปิดสอน <span class="code">&lt;course_id&gt; &lt;theory&gt; &lt;lab&gt; &lt;groups&gt; &lt;main_inst&gt; &lt;co_inst&gt; &lt;theory_room&gt; &lt;lab_room&gt;</span></li>
        <li><strong>[groups_unavailable]</strong> — กำหนดช่วงเวลาที่กลุ่มไม่ว่าง</li>
        <li><strong>[instructor_unavailable]</strong> — กำหนดช่วงเวลาที่อาจารย์ไม่ว่าง</li>
        <li><strong>[instructor_unavailable_main]</strong> — กำหนดช่วงเวลาที่อาจารย์ไม่สามารถเป็นอาจารย์หลัก</li>
        <li><strong>[instructor_nolate]</strong> — กำหนดช่วงเวลาที่อาจารย์ไม่สามารถสอนในช่วงท้าย</li>
        <li><strong>[break]</strong> — กำหนดช่วงพัก (break)</li>
      </ul>
      <div class="example-label">ตัวอย่าง:</div>
<pre>[instructor]
I001 นายสมชาย ใจดี
I002 นางสาวสมหญิง รักเรียน

[groups]
G01 n ปวช.1/1
G02 s ปวส.พิเศษ/1

[rooms]
R101 ห้องเรียน 101
LAB1 ห้องปฏิบัติการคอมพิวเตอร์ 1

[courses]
ENG101 ภาษาอังกฤษธุรกิจ
PRO201 การเขียนโปรแกรมเบื้องต้น

[offering]
ENG101 3 0 G01 I001 x x x
PRO201 2 3 G01,G02 I001 I002 R101 LAB1

[break]
6
7</pre>
      <div style="color:var(--gray);font-size:0.85rem;margin-top:0.5rem;">
        💡 <strong>หมายเหตุ:</strong> ค่า <span class="code">x</span> หมายถึงไม่ได้ระบุ/ไม่มี | คอมเมนต์ใช้ <span class="code">#</span> | day codes: mo=จันทร์, tu=อังคาร, we=พุธ, th=พฤหัสบดี, fr=ศุกร์, sa=เสาร์, su=อาทิตย์ | มี 13 คาบต่อวัน
      </div>
    </div>

    <div class="format-block">
      <h3>📁 ไฟล์ config.txt (Optional)</h3>
      <p style="color:var(--fg2);font-size:0.9rem;margin-bottom:0.6rem;">ไฟล์สำหรับกำหนดตารางสอนล่วงหน้า (pre-defined schedule) ในรูปแบบ raw data</p>
      <ul>
        <li><strong>Assignment line:</strong> <span class="code">&lt;course_id&gt; &lt;theory&gt; &lt;lab&gt; &lt;groups&gt; &lt;day&gt; &lt;theory_start&gt; &lt;lab_start&gt; &lt;theory_room&gt; &lt;lab_room&gt; &lt;main_inst&gt; &lt;co_inst&gt;</span></li>
        <li><strong>GU line:</strong> <span class="code">GU &lt;group_id&gt; &lt;day&gt; &lt;start&gt; &lt;end&gt; &lt;room&gt; &lt;mode&gt;</span> — กำหนดช่วงที่กลุ่มไม่ว่าง</li>
        <li><strong>IU line:</strong> <span class="code">IU &lt;instructor_id&gt; &lt;day&gt; &lt;start&gt; &lt;end&gt; &lt;room&gt; &lt;mode&gt;</span> — กำหนดช่วงที่อาจารย์ไม่ว่าง</li>
      </ul>
      <div class="example-label">ตัวอย่าง:</div>
<pre>ENG101 3 0 G01 mo 1 0 202 x I001 x
PRO201 2 3 G01,G02 we 4 6 R101 LAB1 I001 I002
GU G01 mo 1 2 none "กิจกรรมหน้าเสาธง"
IU I001 we 1-4 none "ประชุม"</pre>
      <div style="color:var(--gray);font-size:0.85rem;margin-top:0.5rem;">
        💡 <strong>หมายเหตุ:</strong> ค่า <span class="code">$</span> ในช่องห้องเรียนหมายถึงให้ระบบเลือกห้องให้อัตโนมัติ | mode: <span class="code">hidden</span> = ซ่อน, <span class="code">"ข้อความ"</span> = แสดงข้อความ
      </div>
    </div>
  </div>

  <footer>
    TimetableX © 2026 — จัดตารางสอนอัตโนมัติ
  </footer>
</div>

<script>
document.getElementById('scheduleForm').addEventListener('submit', function() {
  document.getElementById('submitBtn').disabled = true;
  document.getElementById('submitBtn').textContent = '⏳ กำลังจัดตาราง...';
  document.getElementById('loading').style.display = 'block';
});
</script>
</body>
</html>`
