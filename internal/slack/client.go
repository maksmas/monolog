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

// ErrReauthRequired is returned by Client methods when Slack responds with
// any error that requires re-running the login flow to resolve: missing_scope,
// invalid_auth, not_authed, token_revoked, or account_inactive. Callers use
// errors.Is to detect this and surface a "re-run monolog slack-login" remedy.
var ErrReauthRequired = errors.New("slack: reauthentication required")

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
	AuthorName  string
	Permalink   string
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
	return c.postFormRaw(ctx, method, form, out, nil)
}

// postFormRaw is the shared transport: POST form-encoded, parse the response
// envelope once into out, then classify ok=false by looking at the embedded
// envelope's Error field. idempotentOK lets callers treat specific Slack
// error codes as success (e.g. Unsave maps not_starred to nil).
//
// out must be either *apiResponse or a pointer to a struct that embeds
// apiResponse as its first (unnamed) field so we can read the envelope back
// via type-assertion / reflection-lite.
func (c *Client) postFormRaw(ctx context.Context, method string, form url.Values, out any, idempotentOK map[string]bool) error {
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

	// Extract the embedded apiResponse from out — callers all use structs
	// that either are *apiResponse or embed it as a (first) field.
	env := extractEnvelope(out)
	if env == nil {
		// Should be unreachable given our types; fall back to a second
		// unmarshal so the classifier never silently accepts a bad response.
		var envCopy apiResponse
		if err := json.Unmarshal(payload, &envCopy); err != nil {
			return fmt.Errorf("slack %s: decode envelope: %w", method, err)
		}
		env = &envCopy
	}
	if !env.OK {
		if idempotentOK != nil && idempotentOK[env.Error] {
			return nil
		}
		switch env.Error {
		case "missing_scope", "invalid_auth", "not_authed", "token_revoked", "account_inactive":
			return fmt.Errorf("%w (%s)", ErrReauthRequired, env.Error)
		default:
			return fmt.Errorf("slack %s: %s", method, env.Error)
		}
	}
	return nil
}

// extractEnvelope returns a pointer to the apiResponse embedded in out. Works
// for *apiResponse directly (Unsave) and for structs that embed apiResponse
// as an unnamed field (authTestResponse, starsListResponse, etc.). Returns
// nil when out does not match either shape, in which case postFormRaw falls
// back to a fresh unmarshal of the payload.
func extractEnvelope(out any) *apiResponse {
	switch v := out.(type) {
	case *apiResponse:
		return v
	case *authTestResponse:
		return &v.apiResponse
	case *starsListResponse:
		return &v.apiResponse
	case *conversationsInfoResponse:
		return &v.apiResponse
	case *usersInfoResponse:
		return &v.apiResponse
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
	Type    string        `json:"type"`
	Channel string        `json:"channel,omitempty"`
	Message *starsListMsg `json:"message,omitempty"`
	// Other types (file, channel, im, group, …) are present but we ignore them.
}

type starsListMsg struct {
	TS        string `json:"ts"`
	Text      string `json:"text"`
	User      string `json:"user"`
	Permalink string `json:"permalink,omitempty"`
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
				AuthorName:  authorName,
				Permalink:   permalink,
			})
		}

		cursor = strings.TrimSpace(resp.ResponseMetadata.NextCursor)
		if cursor == "" {
			return out, nil
		}
	}
}

// unsaveIdempotentErrors are Slack error codes that indicate the target
// message is already not starred — these are mapped to success so stars.remove
// is safe to retry. Classified at the raw-envelope level in Unsave so we don't
// string-match on a wrapped error message (which could shift shape under
// future error-wrapping changes).
var unsaveIdempotentErrors = map[string]bool{
	"not_starred":       true,
	"already_unstarred": true,
	"message_not_found": true,
}

// Unsave calls stars.remove against the (channel, ts) pair of a saved
// message. Idempotent errors (not_starred, already_unstarred, message_not_found)
// are treated as success and return nil.
func (c *Client) Unsave(ctx context.Context, channel, ts string) error {
	form := url.Values{}
	form.Set("channel", channel)
	// Slack's stars.remove API uses the parameter name "timestamp". The
	// (undocumented) "channel_timestamp" alias does NOT work — sending only
	// channel + channel_timestamp returns no_item_specified. Keep "timestamp".
	form.Set("timestamp", ts)

	var resp apiResponse
	err := c.postFormRaw(ctx, "stars.remove", form, &resp, unsaveIdempotentErrors)
	return err
}
