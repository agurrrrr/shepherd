package push

import (
	"path/filepath"
	"strconv"
	"testing"
)

func Test_Upsert_같은엔드포인트는교체(t *testing.T) {
	resetForTest()
	SetStorePath(filepath.Join(t.TempDir(), "subs.json"))

	a := Subscription{
		Endpoint: "https://push.example/a",
		Keys:     Keys{P256dh: "pk", Auth: "ak"},
	}
	if err := Upsert(a); err != nil {
		t.Fatal(err)
	}
	a.UserAgent = "device-2"
	if err := Upsert(a); err != nil {
		t.Fatal(err)
	}
	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if list[0].UserAgent != "device-2" {
		t.Errorf("user_agent = %q", list[0].UserAgent)
	}
}

func Test_Upsert_http비로컬_거부(t *testing.T) {
	resetForTest()
	SetStorePath(filepath.Join(t.TempDir(), "subs.json"))

	err := Upsert(Subscription{
		Endpoint: "http://evil.example/push",
		Keys:     Keys{P256dh: "pk", Auth: "ak"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func Test_Upsert_localhost_http허용(t *testing.T) {
	resetForTest()
	SetStorePath(filepath.Join(t.TempDir(), "subs.json"))

	err := Upsert(Subscription{
		Endpoint: "http://127.0.0.1:9/push",
		Keys:     Keys{P256dh: "pk", Auth: "ak"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func Test_Remove_없는엔드포인트는무시(t *testing.T) {
	resetForTest()
	SetStorePath(filepath.Join(t.TempDir(), "subs.json"))

	if err := Remove("https://push.example/missing"); err != nil {
		t.Fatal(err)
	}
}

func Test_Remove_구독삭제(t *testing.T) {
	resetForTest()
	SetStorePath(filepath.Join(t.TempDir(), "subs.json"))

	if err := Upsert(Subscription{
		Endpoint: "https://push.example/a",
		Keys:     Keys{P256dh: "pk", Auth: "ak"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Remove("https://push.example/a"); err != nil {
		t.Fatal(err)
	}
	n, err := Count()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

func Test_Upsert_한도초과(t *testing.T) {
	resetForTest()
	SetStorePath(filepath.Join(t.TempDir(), "subs.json"))

	for i := 0; i < maxSubscriptions; i++ {
		sub := Subscription{
			Endpoint: "https://push.example/" + strconv.Itoa(i),
			Keys:     Keys{P256dh: "pk", Auth: "ak"},
		}
		if err := Upsert(sub); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	err := Upsert(Subscription{
		Endpoint: "https://push.example/overflow",
		Keys:     Keys{P256dh: "pk", Auth: "ak"},
	})
	if err == nil {
		t.Fatal("expected too-many error")
	}
}
