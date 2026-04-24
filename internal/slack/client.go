// Package slack provides an HTTP client and ingest logic for Slack's Web API,
// specifically the stars.* endpoints that back the "Saved items" / "Later" feature.
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultBaseURL is Slack's Web API root.
const defaultBaseURL = "https://slack.com/api"

// defaultTimeout is the per-request timeout used when Client.HTTP is unset.
const defaultTimeout = 10 * time.Second

// ErrMissingScope is returned by Client methods when Slack responds with
// missing_scope or invalid_auth. Callers use errors.Is to detect this and
// surface a "re-run monolog slack-login" remedy.
var ErrMissingScope = errors.New("slack: missing scope")

// Client calls Slack's Web API. Zero value is usable after Token is set, but
// callers typically construct via a literal.
type Client struct {
	// Token is the Slack user OAuth token ("xoxp-..."). Required.
	Token string
	// BaseURL overrides the default https://slack.com/api root. Used by tests.
	BaseURL string
	// Workspace is the workspace subdomain (e.g. "myteam") used when the
	// stars.list response omits a per-message permalink. Optional.
	Workspace string
	// HTTP overrides the default http.Client (10s timeout). Used by tests.
	HTTP *http.Client
}

// SavedItem is a parsed, filtered element from stars.list. Only type="message"
// items are represented; files/channels/etc. are dropped before this type is
// materialized. Channel and author names are resolved and fall back to IDs if
// the resolve API calls fail.
type SavedItem struct {
	Channel     string
	ChannelName string
	TS          string
	Text        string
	AuthorID    string
	AuthorName  string
	Permalink   string
	ThreadTS    string
	HasFiles    bool
}

// base returns the effective base URL, falling back to Slack's public endpoint.
func (c *Client) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return defaultBaseURL
}

// httpClient returns the effective HTTP client, constructing a default with a
// 10s timeout when unset.
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: defaultTimeout}
}

// apiResponse is the common envelope every Slack Web API method wraps its
// payload in.
type apiResponse struct {
	OK               bool             `json:"ok"`
	Error            string           `json:"error,omitempty"`
	ResponseMetadata responseMetadata `json:"response_metadata,omitempty"`
}

type responseMetadata struct {
	NextCursor string `json:"next_cursor,omitempty"`
}

// postForm issues a POST with form-encoded body and bearer auth, decoding the
// response into out. Returns a typed error for missing_scope / invalid_auth,
// and a descriptive error for all other ok=false responses. out must be a
// pointer to a struct that embeds or mirrors apiResponse.
func (c *Client) postForm(ctx context.Context, method string, form url.Values, out any) error {
	if c.Token == "" {
		return errors.New("slack: empty token")
	}
	endpoint := strings.TrimRight(c.base(), "/") + "/" + method

	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return fmt.Errorf("slack %s: build request: %w", method, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("slack %s: %w", method, err)
	}
	defer resp.Body.Close()

	// Handle 429 explicitly — no retry at this layer; caller (poll cadence)
	// is already slow enough.
	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := resp.Header.Get("Retry-After")
		if retryAfter != "" {
			return fmt.Errorf("slack %s: rate limited (retry after %ss)", method, retryAfter)
		}
		return fmt.Errorf("slack %s: rate limited", method)
	}

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("slack %s: read body: %w", method, err)
	}

	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("slack %s: decode response: %w", method, err)
	}

	// Pull the common envelope back out so we can classify errors uniformly.
	var env apiResponse
	if err := json.Unmarshal(payload, &env); err != nil {
		return fmt.Errorf("slack %s: decode envelope: %w", method, err)
	}
	if !env.OK {
		switch env.Error {
		case "missing_scope", "invalid_auth", "not_authed", "token_revoked", "account_inactive":
			return fmt.Errorf("%w (%s)", ErrMissingScope, env.Error)
		default:
			return fmt.Errorf("slack %s: %s", method, env.Error)
		}
	}
	return nil
}

// authTestResponse mirrors auth.test documented fields.
type authTestResponse struct {
	apiResponse
	URL  string `json:"url"`
	Team string `json:"team"`
}

// AuthTest calls auth.test and returns the workspace subdomain (e.g. "myteam"
// from "https://myteam.slack.com/"). Falls back to the team field when URL
// parsing yields no subdomain.
func (c *Client) AuthTest(ctx context.Context) (workspace string, err error) {
	var resp authTestResponse
	if err := c.postForm(ctx, "auth.test", nil, &resp); err != nil {
		return "", err
	}
	if sub := subdomainFromURL(resp.URL); sub != "" {
		return sub, nil
	}
	return resp.Team, nil
}

// subdomainFromURL extracts the leading hostname label (e.g. "myteam" from
// "https://myteam.slack.com/"). Returns empty string on parse failure or when
// the host has no subdomain component.
func subdomainFromURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	// "myteam.slack.com" -> "myteam"; "slack.com" -> "" (bare apex, no subdomain).
	labels := strings.Split(host, ".")
	if len(labels) < 3 {
		return ""
	}
	return labels[0]
}

// starsListItem mirrors a single element of the stars.list items array (only
// fields we use — Slack wraps file/channel/group items in the same array but
// we filter those out).
type starsListItem struct {
	Type    string           `json:"type"`
	Channel string           `json:"channel,omitempty"`
	Message *starsListMsg    `json:"message,omitempty"`
	// Other types (file, channel, im, group, …) are present but we ignore them.
}

type starsListMsg struct {
	TS        string              `json:"ts"`
	Text      string              `json:"text"`
	User      string              `json:"user"`
	ThreadTS  string              `json:"thread_ts,omitempty"`
	Permalink string              `json:"permalink,omitempty"`
	Files     []map[string]any    `json:"files,omitempty"`
}

type starsListResponse struct {
	apiResponse
	Items []starsListItem `json:"items"`
}

type conversationsInfoResponse struct {
	apiResponse
	Channel struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"channel"`
}

type usersInfoResponse struct {
	apiResponse
	User struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		RealName string `json:"real_name"`
		Profile  struct {
			DisplayName string `json:"display_name"`
			RealName    string `json:"real_name"`
		} `json:"profile"`
	} `json:"user"`
}

// resolveChannelName calls conversations.info and returns the channel name,
// falling back to the raw ID on channel_not_found / not_in_channel / other
// errors. Callers cache the result per cycle.
func (c *Client) resolveChannelName(ctx context.Context, channelID string) string {
	form := url.Values{}
	form.Set("channel", channelID)
	var resp conversationsInfoResponse
	if err := c.postForm(ctx, "conversations.info", form, &resp); err != nil {
		// Any error (not_found, not_in_channel, missing scope, network) -> use ID.
		return channelID
	}
	if resp.Channel.Name == "" {
		return channelID
	}
	return resp.Channel.Name
}

// resolveUserName calls users.info and returns the best display name
// (display_name > real_name > name), falling back to the raw ID on error.
func (c *Client) resolveUserName(ctx context.Context, userID string) string {
	form := url.Values{}
	form.Set("user", userID)
	var resp usersInfoResponse
	if err := c.postForm(ctx, "users.info", form, &resp); err != nil {
		return userID
	}
	switch {
	case resp.User.Profile.DisplayName != "":
		return resp.User.Profile.DisplayName
	case resp.User.RealName != "":
		return resp.User.RealName
	case resp.User.Name != "":
		return resp.User.Name
	default:
		return userID
	}
}

// permalinkFor constructs a Slack message permalink from workspace + channel +
// ts when the API response omits it. Format matches what Slack's own clients
// produce: https://<ws>.slack.com/archives/<channel>/p<ts-no-dot>.
func permalinkFor(workspace, channel, ts string) string {
	if workspace == "" || channel == "" || ts == "" {
		return ""
	}
	return fmt.Sprintf("https://%s.slack.com/archives/%s/p%s",
		workspace, channel, strings.ReplaceAll(ts, ".", ""))
}

// ListSaved returns all message-type saved items, paginating across
// response_metadata.next_cursor until empty. Channel and author names are
// resolved once per unique ID (cached across pages within this call).
//
// On a mid-pagination error, returns the items collected so far plus the error
// — the caller (Ingest) is free to use or skip the partial batch.
func (c *Client) ListSaved(ctx context.Context) ([]SavedItem, error) {
	channelCache := map[string]string{}
	userCache := map[string]string{}

	var out []SavedItem
	cursor := ""
	for {
		form := url.Values{}
		form.Set("limit", "100")
		if cursor != "" {
			form.Set("cursor", cursor)
		}

		var resp starsListResponse
		if err := c.postForm(ctx, "stars.list", form, &resp); err != nil {
			return out, err
		}

		for _, it := range resp.Items {
			if it.Type != "message" || it.Message == nil {
				continue
			}
			msg := it.Message

			channelName, ok := channelCache[it.Channel]
			if !ok {
				channelName = c.resolveChannelName(ctx, it.Channel)
				channelCache[it.Channel] = channelName
			}

			authorName := ""
			if msg.User != "" {
				var ok bool
				authorName, ok = userCache[msg.User]
				if !ok {
					authorName = c.resolveUserName(ctx, msg.User)
					userCache[msg.User] = authorName
				}
			}

			permalink := msg.Permalink
			if permalink == "" {
				permalink = permalinkFor(c.Workspace, it.Channel, msg.TS)
			}

			out = append(out, SavedItem{
				Channel:     it.Channel,
				ChannelName: channelName,
				TS:          msg.TS,
				Text:        msg.Text,
				AuthorID:    msg.User,
				AuthorName:  authorName,
				Permalink:   permalink,
				ThreadTS:    msg.ThreadTS,
				HasFiles:    len(msg.Files) > 0,
			})
		}

		cursor = strings.TrimSpace(resp.ResponseMetadata.NextCursor)
		if cursor == "" {
			return out, nil
		}
	}
}

// Unsave calls stars.remove against the (channel, ts) pair of a saved
// message. Idempotent errors (not_starred, already_unstarred, message_not_found)
// are treated as success and return nil.
func (c *Client) Unsave(ctx context.Context, channel, ts string) error {
	form := url.Values{}
	form.Set("channel", channel)
	form.Set("channel_timestamp", ts)

	var resp apiResponse
	err := c.postForm(ctx, "stars.remove", form, &resp)
	if err == nil {
		return nil
	}
	// Classify: idempotent-success errors become nil.
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not_starred"),
		strings.Contains(msg, "already_unstarred"),
		strings.Contains(msg, "message_not_found"):
		return nil
	}
	return err
}
