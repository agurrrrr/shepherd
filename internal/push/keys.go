package push

import (
	"log"
	"sync"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/agurrrrr/shepherd/internal/config"
)

const defaultVAPIDSubject = "shepherd@localhost"

var (
	keyMu      sync.Mutex
	cachedPub  string
	cachedPriv string

	// persistVAPID writes generated keys. Tests replace this so we never
	// touch ~/.shepherd/config.yaml.
	persistVAPID = defaultPersistVAPID
)

func defaultPersistVAPID(pub, priv string) error {
	if err := config.Set("webpush_vapid_public", pub); err != nil {
		return err
	}
	return config.Set("webpush_vapid_private", priv)
}

// EnsureVAPIDKeys returns the server VAPID pair, generating and persisting
// them on first use. The private key must never be sent to the browser.
func EnsureVAPIDKeys() (pub, priv string, err error) {
	keyMu.Lock()
	defer keyMu.Unlock()

	if cachedPub != "" && cachedPriv != "" {
		return cachedPub, cachedPriv, nil
	}

	pub = config.GetString("webpush_vapid_public")
	priv = config.GetString("webpush_vapid_private")
	if pub != "" && priv != "" {
		cachedPub, cachedPriv = pub, priv
		return pub, priv, nil
	}

	priv, pub, err = webpush.GenerateVAPIDKeys()
	if err != nil {
		return "", "", err
	}
	cachedPub, cachedPriv = pub, priv
	if err := persistVAPID(pub, priv); err != nil {
		log.Printf("[push] persist VAPID keys: %v", err)
	}
	return pub, priv, nil
}

func vapidSubject() string {
	s := config.GetString("webpush_vapid_subject")
	if s == "" {
		return defaultVAPIDSubject
	}
	return s
}
