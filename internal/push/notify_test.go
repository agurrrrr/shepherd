package push

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func Test_NotifyComplete_비활성시전송안함(t *testing.T) {
	resetForTest()
	SetStorePath(filepath.Join(t.TempDir(), "subs.json"))
	viper.Set("webpush_enabled", false)
	viper.Set("webpush_notify_on_complete", true)

	called := 0
	sendOne = func(sub Subscription, body []byte, highPriority bool) (int, error) {
		called++
		return 201, nil
	}
	_ = Upsert(Subscription{
		Endpoint: "https://push.example/a",
		Keys:     Keys{P256dh: "pk", Auth: "ak"},
	})

	NotifyComplete(1, "양", "proj", "done")
	if called != 0 {
		t.Fatalf("called = %d, want 0", called)
	}
}

func Test_NotifyComplete_활성시모든구독에전송(t *testing.T) {
	resetForTest()
	SetStorePath(filepath.Join(t.TempDir(), "subs.json"))
	viper.Set("webpush_enabled", true)
	viper.Set("webpush_notify_on_complete", true)

	var got []Payload
	sendOne = func(sub Subscription, body []byte, highPriority bool) (int, error) {
		var p Payload
		if err := json.Unmarshal(body, &p); err != nil {
			t.Errorf("payload: %v", err)
		}
		got = append(got, p)
		if highPriority {
			t.Error("complete should not be high priority")
		}
		return 201, nil
	}
	_ = Upsert(Subscription{Endpoint: "https://push.example/a", Keys: Keys{P256dh: "pk", Auth: "ak"}})
	_ = Upsert(Subscription{Endpoint: "https://push.example/b", Keys: Keys{P256dh: "pk", Auth: "ak"}})

	NotifyComplete(7, "햄찌", "shepherd", "끝")
	if len(got) != 2 {
		t.Fatalf("sent = %d, want 2", len(got))
	}
	if got[0].Status != "completed" || got[0].URL != "/tasks/7" {
		t.Errorf("payload = %+v", got[0])
	}
}

func Test_NotifyFail_410이면구독삭제(t *testing.T) {
	resetForTest()
	SetStorePath(filepath.Join(t.TempDir(), "subs.json"))
	viper.Set("webpush_enabled", true)
	viper.Set("webpush_notify_on_fail", true)

	sendOne = func(sub Subscription, body []byte, highPriority bool) (int, error) {
		if !highPriority {
			t.Error("fail should be high priority")
		}
		return http.StatusGone, nil
	}
	_ = Upsert(Subscription{Endpoint: "https://push.example/dead", Keys: Keys{P256dh: "pk", Auth: "ak"}})

	NotifyFail(2, "양", "p", "err")
	n, err := Count()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0 after 410", n)
	}
}

func Test_sendAll_전송실패는다른구독에영향없음(t *testing.T) {
	resetForTest()
	SetStorePath(filepath.Join(t.TempDir(), "subs.json"))

	sendOne = func(sub Subscription, body []byte, highPriority bool) (int, error) {
		if sub.Endpoint == "https://push.example/bad" {
			return 0, errors.New("network")
		}
		return 201, nil
	}
	_ = Upsert(Subscription{Endpoint: "https://push.example/bad", Keys: Keys{P256dh: "pk", Auth: "ak"}})
	_ = Upsert(Subscription{Endpoint: "https://push.example/ok", Keys: Keys{P256dh: "pk", Auth: "ak"}})

	res, err := sendAll(TestPayload(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 1 || res.Failed != 1 {
		t.Errorf("result = %+v", res)
	}
}

func Test_EnsureVAPIDKeys_한번생성후재사용(t *testing.T) {
	resetForTest()
	viper.Set("webpush_vapid_public", "")
	viper.Set("webpush_vapid_private", "")

	pub1, priv1, err := EnsureVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	if pub1 == "" || priv1 == "" {
		t.Fatal("empty keys")
	}
	pub2, priv2, err := EnsureVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	if pub1 != pub2 || priv1 != priv2 {
		t.Fatal("keys rotated unexpectedly")
	}
}
