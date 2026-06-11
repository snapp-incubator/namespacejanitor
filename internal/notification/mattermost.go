package notification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
)

// MattermostNotifier sends notifications to a Mattermost incoming webhook.
type MattermostNotifier struct {
	webhookURL string
	httpClient *http.Client
	logger     logr.Logger
}

// mattermostMessage is the payload format for Mattermost incoming webhooks.
type mattermostMessage struct {
	Text        string             `json:"text"`
	Username    string             `json:"username,omitempty"`
	IconEmoji   string             `json:"icon_emoji,omitempty"`
	Attachments []mattermostAttach `json:"attachments,omitempty"`
}

type mattermostAttach struct {
	Fallback string            `json:"fallback,omitempty"`
	Color    string            `json:"color,omitempty"`
	Title    string            `json:"title,omitempty"`
	Text     string            `json:"text,omitempty"`
	Fields   []mattermostField `json:"fields,omitempty"`
	Footer   string            `json:"footer,omitempty"`
}

type mattermostField struct {
	Short bool   `json:"short"`
	Title string `json:"title"`
	Value string `json:"value"`
}

// NewMattermostNotifier creates a notifier that posts to a Mattermost webhook.
func NewMattermostNotifier(webhookURL string, logger logr.Logger) (Notifier, error) {
	if webhookURL == "" {
		return nil, fmt.Errorf("mattermost webhook URL must not be empty")
	}

	return &MattermostNotifier{
		webhookURL: webhookURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     logger,
	}, nil
}

func (m *MattermostNotifier) Send(payload JanitorPayload) error {
	info := GetActionInfo(payload.ActionTaken)

	// Build the main text line with emoji and human-readable label
	mainText := fmt.Sprintf("%s **%s** — `%s`", info.Emoji, info.Label, payload.NamespaceName)

	// Build fields
	fields := []mattermostField{
		{Title: "🏷️ Namespace", Value: fmt.Sprintf("`%s`", payload.NamespaceName), Short: true},
		{Title: "📌 Flag", Value: formatFlag(payload.CurrentFlag), Short: true},
		{Title: "⏳ Age", Value: payload.Age, Short: true},
		{Title: "🎯 Severity", Value: fmt.Sprintf("**%s**", info.Severity), Short: true},
	}

	// Requester with @mention
	if payload.Requester != "" {
		fields = append(fields, mattermostField{
			Title: "👤 Requester",
			Value: fmt.Sprintf("@%s", payload.Requester),
			Short: true,
		})
	}

	// Additional recipients
	if len(payload.AdditionalRecipients) > 0 {
		recipients := ""
		for i, r := range payload.AdditionalRecipients {
			if i > 0 {
				recipients += ", "
			}
			recipients += fmt.Sprintf("@%s", r)
		}
		fields = append(fields, mattermostField{
			Title: "📧 CC",
			Value: recipients,
			Short: true,
		})
	}

	// Build the attachment
	attach := mattermostAttach{
		Fallback: fmt.Sprintf("[%s] %s — %s", info.Severity, payload.NamespaceName, info.Label),
		Color:    info.Color,
		Title:    fmt.Sprintf("%s %s", info.Emoji, info.Label),
		Fields:   fields,
		Footer:   "NamespaceJanitor Operator",
	}

	// Add guidance as the attachment text if available
	if info.Guidance != "" {
		attach.Text = info.Guidance
	}

	msg := mattermostMessage{
		Text:        mainText,
		Username:    "NamespaceJanitor",
		IconEmoji:   ":wastebasket:",
		Attachments: []mattermostAttach{attach},
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling mattermost message: %w", err)
	}

	m.logger.Info("Sending Mattermost notification",
		"namespace", payload.NamespaceName,
		"action", payload.ActionTaken,
		"severity", info.Severity,
	)

	resp, err := m.httpClient.Post(m.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sending mattermost webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mattermost webhook returned status %d", resp.StatusCode)
	}

	return nil
}

func (m *MattermostNotifier) Close() error {
	m.logger.Info("Mattermost notifier closed (no persistent connections)")
	return nil
}

// formatFlag returns a human-readable flag label with emoji.
func formatFlag(flag string) string {
	switch flag {
	case "yellow":
		return "🟡 Yellow"
	case "red":
		return "🔴 Red"
	case "":
		return "⚪ None"
	default:
		return flag
	}
}
