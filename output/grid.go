package output

import (
	"fmt"
	"html"
	"strings"

	"timetablex/types"
)

// Cell background colours that tell main and co-teaching apart.
const (
	mainColor  = "#dbeafe"
	coColor    = "#fed7aa"
	otherColor = "#f3f4f6"
)

// gridCell is one merged cell of the weekly grid: adjacent periods of the same
// subject and the same period type (theory or lab).
type gridCell struct {
	start, end int
	entry      *periodEntry
}

// renderGridTable renders a weekly timetable as an HTML table (embedded in markdown):
// the header row holds the period numbers, the first column holds the day, and every
// subject occupies a single cell spanning all of its adjacent periods.
// groupsLabel and roomLabel name the 3rd and 4th fields of a periodEntry for the cell text.
func (r *TimetableRenderer) renderGridTable(sb *strings.Builder, days []types.Day,
	entriesFor func(day types.Day) []*periodEntry, groupsLabel, roomLabel string) {

	sb.WriteString("<table border=\"1\" cellpadding=\"4\" cellspacing=\"0\">\n")
	sb.WriteString("<tr><th>วัน / คาบที่</th>")
	for p := 1; p <= types.MaxPeriodsPerDay; p++ {
		sb.WriteString(fmt.Sprintf("<th>%d<br>%s</th>", p, strings.ReplaceAll(types.PeriodTime(p), "-", "\u2011"))) // non-breaking hyphen keeps the time on one line
	}
	sb.WriteString("</tr>\n")

	for _, day := range days {
		cells := mergeSameSubject(entriesFor(day))

		sb.WriteString(fmt.Sprintf("<tr><th>%s</th>", html.EscapeString(types.DayFullName[day])))
		p := 1
		for _, c := range cells {
			for ; p < c.start; p++ {
				sb.WriteString("<td></td>")
			}
			if c.start < p {
				continue // overlapping entry, already covered
			}
			sb.WriteString(fmt.Sprintf("<td align=\"center\" colspan=\"%d\" bgcolor=\"%s\" style=\"background:%s;color:#000\">",
				c.end-c.start+1, c.color(), c.color()))
			sb.WriteString(renderCellText(c, groupsLabel, roomLabel))
			sb.WriteString("</td>")
			p = c.end + 1
		}
		for ; p <= types.MaxPeriodsPerDay; p++ {
			sb.WriteString("<td></td>")
		}
		sb.WriteString("</tr>\n")
	}
	sb.WriteString("</table>\n\n")
}

// color returns the background colour of the cell by teaching role.
func (c *gridCell) color() string {
	switch c.entry.role {
	case roleMain:
		return mainColor
	case roleCo:
		return coColor
	}
	return otherColor
}

// mergeSameSubject joins adjacent identical entries (same subject, period type, groups and
// room) into one cell; theory and lab periods of a course stay in separate cells.
func mergeSameSubject(entries []*periodEntry) []*gridCell {
	var cells []*gridCell
	for _, e := range entries {
		if n := len(cells); n > 0 {
			last := cells[n-1]
			// Merge only when every displayed field matches, so a different group,
			// instructor or room is never hidden inside another entry's cell.
			if last.entry.subject == e.subject && last.entry.periodType == e.periodType &&
				last.entry.groups == e.groups && last.entry.room == e.room && last.entry.role == e.role &&
				last.end+1 == e.periodStart {
				last.end = e.periodEnd
				continue
			}
		}
		cells = append(cells, &gridCell{start: e.periodStart, end: e.periodEnd, entry: e})
	}
	return cells
}

// renderCellText builds the HTML text of a cell: subject, period type, then groups / room.
func renderCellText(c *gridCell, groupsLabel, roomLabel string) string {
	e := c.entry
	lines := []string{"<b>" + html.EscapeString(e.subject) + "</b>"}

	if e.periodType != "x" {
		lines = append(lines, "<b>["+html.EscapeString(e.periodType)+"]</b>")
	}
	for _, f := range []struct{ label, value string }{{groupsLabel, e.groups}, {roomLabel, e.room}} {
		if f.value != "" && f.value != "x" {
			lines = append(lines, html.EscapeString(f.label+" "+f.value))
		}
	}
	return strings.Join(lines, "<br>")
}
