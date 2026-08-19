package push

func resetForTest() {
	keyMu.Lock()
	cachedPub, cachedPriv = "", ""
	keyMu.Unlock()
	storePathOverride = ""
	sendOne = defaultSend
	persistVAPID = func(pub, priv string) error {
		_ = pub
		_ = priv
		return nil
	}
}
