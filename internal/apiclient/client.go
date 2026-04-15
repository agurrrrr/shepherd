package apiclient

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/internal/config"
)

// Client communicates with the Shepherd daemon REST API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New creates a new API client using config values.
func New() *Client {
	host := config.GetString("server_host")
	port := config.GetInt("server_port")
	// For TUI client connecting locally, use localhost instead of 0.0.0.0
	if host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return &Client{
		baseURL: fmt.Sprintf("http://%s:%d", host, port),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// --- Response types ---

type SheepInfo struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Provider string `json:"provider"`
	Project  string `json:"project,omitempty"`
}

type CommandResult struct {
	TaskID      int    `json:"task_id"`
	SheepName   string `json:"sheep_name"`
	ProjectName string `json:"project_name"`
	Reason      string `json:"reason,omitempty"`
}

type StatusInfo struct {
	Sheep struct {
		Total   int `json:"total"`
		Working int `json:"working"`
		Idle    int `json:"idle"`
		Error   int `json:"error"`
	} `json:"sheep"`
	Projects   int `json:"projects"`
	SSEClients int `json:"sse_clients"`
	Tasks      struct {
		Pending   int `json:"pending"`
		Running   int `json:"running"`
		Completed int `json:"completed"`
		Failed    int `json:"failed"`
	} `json:"tasks"`
}

type SSEEvent struct {
	Type string
	Data json.RawMessage
}

// --- Auth ---

// SetToken sets the access token for API requests.
func (c *Client) SetToken(token string) {
	c.token = token
}

// Login authenticates and stores the access token.
func (c *Client) Login(username, password string) error {
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})

	resp, err := c.http.Post(c.baseURL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("login failed (status %d)", resp.StatusCode)
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	c.token = result.AccessToken
	return nil
}

// --- API methods ---

// ListSheep returns the list of sheep from the daemon.
func (c *Client) ListSheep() ([]SheepInfo, error) {
	var apiResp struct {
		Success bool       `json:"success"`
		Data    []SheepInfo `json:"data"`
	}
	if err := c.get("/api/sheep", &apiResp); err != nil {
		return nil, err
	}
	return apiResp.Data, nil
}

// PostCommand sends a natural language command to the daemon.
func (c *Client) PostCommand(prompt string) (*CommandResult, error) {
	body, _ := json.Marshal(map[string]string{"prompt": prompt})

	var apiResp struct {
		Success bool          `json:"success"`
		Data    CommandResult `json:"data"`
		Message string        `json:"message,omitempty"`
	}
	if err := c.post("/api/command", body, &apiResp); err != nil {
		return nil, err
	}
	if !apiResp.Success {
		return nil, fmt.Errorf("%s", apiResp.Message)
	}
	return &apiResp.Data, nil
}

// CreateTask directly creates a task for a specific sheep/project.
func (c *Client) CreateTask(prompt, sheepName, projectName string) (*CommandResult, error) {
	body, _ := json.Marshal(map[string]string{
		"prompt":       prompt,
		"sheep_name":   sheepName,
		"project_name": projectName,
	})

	var apiResp struct {
		Success bool          `json:"success"`
		Data    CommandResult `json:"data"`
		Message string        `json:"message,omitempty"`
	}
	if err := c.post("/api/tasks", body, &apiResp); err != nil {
		return nil, err
	}
	if !apiResp.Success {
		return nil, fmt.Errorf("%s", apiResp.Message)
	}
	return &apiResp.Data, nil
}

// SystemStatus returns the system status from the daemon.
func (c *Client) SystemStatus() (*StatusInfo, error) {
	var apiResp struct {
		Success bool       `json:"success"`
		Data    StatusInfo `json:"data"`
	}
	if err := c.get("/api/system/status", &apiResp); err != nil {
		return nil, err
	}
	return &apiResp.Data, nil
}

// --- SSE ---

// ConnectSSE connects to the SSE event stream and returns an event channel.
// The channel is closed when the connection is lost and reconnection fails.
func (c *Client) ConnectSSE() (<-chan SSEEvent, error) {
	ch := make(chan SSEEvent, 256)

	go func() {
		defer close(ch)

		backoff := time.Second
		maxBackoff := 30 * time.Second

		for {
			err := c.streamSSE(ch)
			if err == nil {
				return // Channel was closed intentionally
			}

			// Exponential backoff for reconnection
			time.Sleep(backoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}()

	return ch, nil
}

func (c *Client) streamSSE(ch chan<- SSEEvent) error {
	url := c.baseURL + "/api/events"
	if c.token != "" {
		url += "?token=" + c.token
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// Use a separate client without timeout for SSE
	sseClient := &http.Client{}
	resp, err := sseClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("SSE connection failed (status %d)", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	var eventType string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if eventType != "" {
				ch <- SSEEvent{
					Type: eventType,
					Data: json.RawMessage(data),
				}
				eventType = ""
			}
		}
		// Empty line or comment lines are ignored
	}

	return scanner.Err()
}

// --- HTTP helpers ---

func (c *Client) get(path string, result interface{}) error {
	req, err := http.NewRequest("GET", c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, body)
	}

	return json.NewDecoder(resp.Body).Decode(result)
}

func (c *Client) post(path string, body []byte, result interface{}) error {
	req, err := http.NewRequest("POST", c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, respBody)
	}

	return json.NewDecoder(resp.Body).Decode(result)
}
