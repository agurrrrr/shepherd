package push

import (
	"encoding/json"
	"log"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/agurrrrr/shepherd/internal/config"
)

const pushTTLSeconds = 3600

// sendFunc posts one Web Push message. Tests replace this.
type sendFunc func(sub Subscription, body []byte, highPriority bool) (status int, err error)

var sendOne sendFunc = defaultSend

func defaultSend(sub Subscription, body []byte, highPriority bool) (int, error) {
	pub, priv, err := EnsureVAPIDKeys()
	if err != nil {
		return 0, err
	}

	urgency := webpush.UrgencyNormal
	if highPriority {
		urgency = webpush.UrgencyHigh
	}

	resp, err := webpush.SendNotification(body, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			Auth:   sub.Keys.Auth,
			P256dh: sub.Keys.P256dh,
		},
	}, &webpush.Options{
		Subscriber:      vapidSubject(),
		TTL:             pushTTLSeconds,
		Urgency:         urgency,
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
	})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// NotifyComplete sends a Web Push when a task finishes successfully.
func NotifyComplete(taskID int, sheepName, projectName, summary string) {
	if !enabled() || !config.GetBool("webpush_notify_on_complete") {
		return
	}
	_ = sheepName
	if _, err := sendAll(CompletePayload(taskID, projectName, summary), false); err != nil {
		log.Printf("[push] complete notify: %v", err)
	}
}

// NotifyFail sends a Web Push when a task fails.
func NotifyFail(taskID int, sheepName, projectName, errMsg string) {
	if !enabled() || !config.GetBool("webpush_notify_on_fail") {
		return
	}
	_ = sheepName
	if _, err := sendAll(FailPayload(taskID, projectName, errMsg), true); err != nil {
		log.Printf("[push] fail notify: %v", err)
	}
}

// NotifyShepherdReport sends a Web Push for a finished shepherd (목자) task
// (#602). The per-report decision (shepherd_notify, quiet patrol) is made in
// manager before calling this, so only the webpush master switch is checked
// here — the report is its own notification class, not a task completion.
func NotifyShepherdReport(taskID int, kind, report string) {
	if !enabled() {
		return
	}
	if _, err := sendAll(ShepherdReportPayload(taskID, kind, report), false); err != nil {
		log.Printf("[push] shepherd report notify: %v", err)
	}
}

// SendResult is the outcome of a fan-out send.
type SendResult struct {
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
	Dropped int `json:"dropped"`
}

// SendToAll delivers payload to every stored subscription.
func SendToAll(payload Payload, highPriority bool) (SendResult, error) {
	return sendAll(payload, highPriority)
}

// SendToEndpoint delivers payload to one stored subscription. Unknown
// endpoint returns a zero result without error.
func SendToEndpoint(endpoint string, payload Payload, highPriority bool) (SendResult, error) {
	list, err := List()
	if err != nil {
		return SendResult{}, err
	}
	var match *Subscription
	for i := range list {
		if list[i].Endpoint == endpoint {
			match = &list[i]
			break
		}
	}
	if match == nil {
		return SendResult{}, nil
	}
	return sendSubset([]Subscription{*match}, payload, highPriority)
}

func enabled() bool {
	return config.GetBool("webpush_enabled")
}

func sendAll(payload Payload, highPriority bool) (SendResult, error) {
	list, err := List()
	if err != nil {
		return SendResult{}, err
	}
	return sendSubset(list, payload, highPriority)
}

func sendSubset(list []Subscription, payload Payload, highPriority bool) (SendResult, error) {
	if len(list) == 0 {
		return SendResult{}, nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return SendResult{}, err
	}

	var result SendResult
	for _, sub := range list {
		status, err := sendOne(sub, body, highPriority)
		if err != nil {
			result.Failed++
			log.Printf("[push] send %s: %v", truncateRunes(sub.Endpoint, 48), err)
			continue
		}
		if status == http.StatusNotFound || status == http.StatusGone {
			storeMu.Lock()
			_ = removeLocked(sub.Endpoint)
			storeMu.Unlock()
			result.Dropped++
			continue
		}
		if status >= 200 && status < 300 {
			result.Sent++
			continue
		}
		result.Failed++
		log.Printf("[push] send %s: HTTP %d", truncateRunes(sub.Endpoint, 48), status)
	}
	return result, nil
}
