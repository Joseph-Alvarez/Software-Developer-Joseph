// emailparser/emailparser.go
package emailparser

import (
	"strings"
	"time"
)

type Email struct {
	MessageID  string    `json:"message_id"`
	Date       time.Time `json:"date"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Subject    string    `json:"subject"`
	Content    string    `json:"content"`
	FilePath   string    `json:"file_path"`
	FolderPath string    `json:"folder_path"`
}

func ParseEmail(content, filepath, basePath string) Email {
	lines := strings.Split(content, "\n")
	email := Email{
		FilePath:   filepath,
		FolderPath: strings.TrimPrefix(filepath, basePath),
	}

	var bodyLines []string
	inBody := false

	for _, line := range lines {
		if inBody {
			bodyLines = append(bodyLines, line)
			continue
		}

		if line == "" {
			inBody = true
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch strings.ToLower(key) {
		case "message-id":
			email.MessageID = value
		case "date":
			t, _ := time.Parse("Mon, 2 Jan 2006 15:04:05 -0700 (MST)", value)
			email.Date = t
		case "from":
			email.From = value
		case "to":
			email.To = value
		case "subject":
			email.Subject = value
		}
	}

	email.Content = strings.Join(bodyLines, "\n")
	return email
}
