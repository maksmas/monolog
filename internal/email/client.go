// Package email provides Gmail integration for importing labeled emails as
// monolog tasks and archiving them when the corresponding task is completed.
//
// The package is decoupled from internal/config: callers pass config values
// (label, max-per-sync, paths, etc.) into email functions by value rather
// than the package importing config. This keeps email/ easy to unit-test
// with arbitrary settings.
package email

import (
	"context"
	"fmt"
	"net/http"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// Message is a neutral DTO for a Gmail message — it shields the rest of the
// codebase (and tests) from the Gmail API's heavyweight types.
type Message struct {
	ID      string
	Subject string
	From    string
	Snippet string
}

// Gmail is the small interface every other piece of the email package depends
// on. Tests pass a fake implementation; production code uses NewClient to get
// a real one wrapping *gmail.Service.
type Gmail interface {
	// ListLabeled returns Gmail message IDs carrying the given label, in
	// API order (newest-first). Implementations paginate exhaustively.
	ListLabeled(ctx context.Context, label string) ([]string, error)
	// Get fetches the metadata + snippet for a single message.
	Get(ctx context.Context, id string) (*Message, error)
	// ArchiveLabel removes the INBOX label from a message (Gmail's
	// definition of "archive"). The configured trigger label is left in
	// place — see archive-on-done semantics.
	ArchiveLabel(ctx context.Context, id string) error
}

// realGmail is the production Gmail implementation backed by *gmail.Service.
type realGmail struct {
	svc *gmailapi.Service
}

// NewClient constructs a Gmail client using an authenticated http.Client
// (typically produced by HTTPClient in oauth.go).
func NewClient(ctx context.Context, httpClient *http.Client) (Gmail, error) {
	if httpClient == nil {
		return nil, fmt.Errorf("email: nil http client")
	}
	svc, err := gmailapi.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("email: new gmail service: %w", err)
	}
	return &realGmail{svc: svc}, nil
}

// ListLabeled paginates through users.messages.list with q="label:<label>".
// The Gmail API returns results newest-first; we preserve that order.
func (g *realGmail) ListLabeled(ctx context.Context, label string) ([]string, error) {
	if label == "" {
		return nil, fmt.Errorf("email: empty label")
	}
	q := fmt.Sprintf("label:%s", label)
	var ids []string
	pageToken := ""
	for {
		call := g.svc.Users.Messages.List("me").Q(q).Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("email: list messages: %w", err)
		}
		for _, m := range resp.Messages {
			ids = append(ids, m.Id)
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return ids, nil
}

// Get fetches a single message in METADATA format and extracts the headers
// we care about plus Gmail's free snippet field.
func (g *realGmail) Get(ctx context.Context, id string) (*Message, error) {
	if id == "" {
		return nil, fmt.Errorf("email: empty message id")
	}
	msg, err := g.svc.Users.Messages.Get("me", id).
		Format("METADATA").
		MetadataHeaders("Subject", "From").
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("email: get message %s: %w", id, err)
	}
	out := &Message{ID: msg.Id, Snippet: msg.Snippet}
	if msg.Payload != nil {
		for _, h := range msg.Payload.Headers {
			switch h.Name {
			case "Subject":
				out.Subject = h.Value
			case "From":
				out.From = h.Value
			}
		}
	}
	return out, nil
}

// ArchiveLabel removes the INBOX label so the message no longer appears in
// the inbox. The trigger label (e.g. "monolog") is intentionally retained —
// archive-only semantics, option A from the plan.
func (g *realGmail) ArchiveLabel(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("email: empty message id")
	}
	_, err := g.svc.Users.Messages.Modify("me", id, &gmailapi.ModifyMessageRequest{
		RemoveLabelIds: []string{"INBOX"},
	}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("email: archive message %s: %w", id, err)
	}
	return nil
}
