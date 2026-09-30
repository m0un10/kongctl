// Package gitlab posts merge-request notes through the GitLab REST API.
package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Client is a note provider for one merge request.
type Client struct {
	HTTP      *http.Client
	BaseURL   string // e.g. https://gitlab.example.com/api/v4
	ProjectID string
	MRIID     string
	Token     string
}

// New validates the required settings.
func New(httpClient *http.Client, baseURL, projectID, mrIID, token string) (*Client, error) {
	var missing []string
	if baseURL == "" {
		missing = append(missing, "gitlab.baseUrl (CI_API_V4_URL)")
	}
	if projectID == "" {
		missing = append(missing, "gitlab.projectId (CI_PROJECT_ID)")
	}
	if mrIID == "" {
		missing = append(missing, "gitlab.mrIid (CI_MERGE_REQUEST_IID)")
	}
	if token == "" {
		missing = append(missing, "gitlab.token (GITLAB_TOKEN)")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("posting a note needs: %s", strings.Join(missing, ", "))
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimSuffix(baseURL, "/"), ProjectID: projectID, MRIID: mrIID, Token: token}, nil
}

func (c *Client) notesURL() string {
	return fmt.Sprintf("%s/projects/%s/merge_requests/%s/notes", c.BaseURL, url.PathEscape(c.ProjectID), url.PathEscape(c.MRIID))
}

type note struct {
	ID     json.Number `json:"id"`
	Body   string      `json:"body"`
	System bool        `json:"system"`
}

func (c *Client) do(ctx context.Context, method, u string, form url.Values) ([]byte, http.Header, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("%s %s: HTTP %d: %s", method, u, resp.StatusCode, truncate(string(data), 200))
	}
	return data, resp.Header, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// FindNote scans every page of notes, newest first, for the marker. System
// notes are skipped.
func (c *Client) FindNote(ctx context.Context, marker string) (string, bool, error) {
	for page := 1; page <= 50; page++ {
		u := fmt.Sprintf("%s?per_page=100&order_by=created_at&sort=desc&page=%d", c.notesURL(), page)
		data, hdr, err := c.do(ctx, http.MethodGet, u, nil)
		if err != nil {
			return "", false, err
		}
		var notes []note
		if err := json.Unmarshal(data, &notes); err != nil {
			return "", false, fmt.Errorf("decoding notes: %w", err)
		}
		for _, n := range notes {
			if n.System {
				continue
			}
			if strings.Contains(n.Body, marker) {
				return n.ID.String(), true, nil
			}
		}
		next := hdr.Get("X-Next-Page")
		if next == "" || len(notes) == 0 {
			return "", false, nil
		}
		if np, err := strconv.Atoi(next); err != nil || np <= page {
			return "", false, nil
		}
	}
	return "", false, errors.New("gave up paging notes after 50 pages")
}

// CreateNote posts a new note.
func (c *Client) CreateNote(ctx context.Context, body string) error {
	_, _, err := c.do(ctx, http.MethodPost, c.notesURL(), url.Values{"body": {body}})
	return err
}

// UpdateNote edits an existing note.
func (c *Client) UpdateNote(ctx context.Context, id, body string) error {
	_, _, err := c.do(ctx, http.MethodPut, c.notesURL()+"/"+url.PathEscape(id), url.Values{"body": {body}})
	return err
}
