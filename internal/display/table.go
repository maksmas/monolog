package display

import (
	"fmt"
	"io"
	"strings"

	"github.com/mmaksmas/monolog/internal/model"
)

// shortID returns the first 8 characters of a task ID, or the full ID if shorter.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// FormatTasks writes tasks as a clean terminal table to w.
// Each line shows: position indicator, short ID, title, schedule, tags.
func FormatTasks(w io.Writer, tasks []model.Task) {
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

		fmt.Fprintf(w, "%-4s %-8s  %-40s %-10s %s\n",
			marker,
			shortID(task.ID),
			task.Title,
			task.Schedule,
			tags,
		)
	}
}
