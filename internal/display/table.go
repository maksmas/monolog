package display

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mmaksmas/monolog/internal/model"
)

// ShortID returns the first 8 characters of a task ID, or the full ID if shorter.
func ShortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// FormatTasks writes tasks as a clean terminal table to w.
// Each line shows: position indicator, short ID, title, schedule, dates, tags.
// The now parameter is used to compute compact relative dates.
func FormatTasks(w io.Writer, tasks []model.Task, now time.Time) {
	if len(tasks) == 0 {
		fmt.Fprintln(w, "No tasks.")
		return
	}

	for i, task := range tasks {
		marker := fmt.Sprintf("%d", i+1)
		if task.Status == "done" {
			marker = "x"
		}

		tags := ""
		if len(task.Tags) > 0 {
			tags = "[" + strings.Join(task.Tags, ", ") + "]"
		}

		dates := FormatTaskDates(now, task)

		fmt.Fprintf(w, "%-4s %-8s  %-40s %-10s %-8s %s\n",
			marker,
			ShortID(task.ID),
			task.Title,
			task.Schedule,
			dates,
			tags,
		)
	}
}
