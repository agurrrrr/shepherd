package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Notifier sends messages to Discord via webhook.
type Notifier struct{}

// New creates a new Discord notifier.
func New() *Notifier {
	return &Notifier{}
}

// EmbedField is a single field in a Discord embed.
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

// Embed is a Discord embed object.
type Embed struct {
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color,omitempty"`
	Fields      []EmbedField `json:"fields,omitempty"`
	Footer      *EmbedFooter `json:"footer,omitempty"`
	Timestamp   string       `json:"timestamp,omitempty"`
}

// EmbedFooter is the footer of a Discord embed.
type EmbedFooter struct {
	Text string `json:"text"`
}

// WebhookPayload is the JSON body for a Discord webhook request.
type WebhookPayload struct {
	Content string  `json:"content,omitempty"`
	Embeds  []Embed `json:"embeds,omitempty"`
}

// Send sends a message to a Discord webhook URL.
func (n *Notifier) Send(webhookURL, content string, embeds []Embed) error {
	if webhookURL == "" {
		return nil
	}

	payload := WebhookPayload{
		Content: content,
		Embeds:  embeds,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode > 299 {
		return &WebhookError{StatusCode: resp.StatusCode}
	}

	return nil
}

// WebhookError is returned when Discord webhook request fails.
type WebhookError struct {
	StatusCode int
}

func (e *WebhookError) Error() string {
	return "discord webhook returned status " + string(rune('0'+e.StatusCode/100)) + string(rune('0'+(e.StatusCode/10)%10)) + string(rune('0'+e.StatusCode%10))
}
