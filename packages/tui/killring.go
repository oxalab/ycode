package tui

// KillRing is an Emacs-style kill/yank ring. Consecutive kills can accumulate
// into a single entry. Matches reference kill-ring.ts.
type KillRing struct {
	ring []string
}

// NewKillRing returns an empty kill ring.
func NewKillRing() *KillRing { return &KillRing{} }

// Push adds text to the ring. When accumulate is true and the ring is non-empty,
// the text is merged into the most recent entry: prepended for backward deletions
// (Ctrl+W/Ctrl+U) or appended for forward deletions (Ctrl+K/Alt+D).
func (k *KillRing) Push(text string, prepend, accumulate bool) {
	if text == "" {
		return
	}
	if accumulate && len(k.ring) > 0 {
		last := k.ring[len(k.ring)-1]
		k.ring = k.ring[:len(k.ring)-1]
		if prepend {
			k.ring = append(k.ring, text+last)
		} else {
			k.ring = append(k.ring, last+text)
		}
		return
	}
	k.ring = append(k.ring, text)
}

// Peek returns the most recent entry without modifying the ring.
func (k *KillRing) Peek() (string, bool) {
	if len(k.ring) == 0 {
		return "", false
	}
	return k.ring[len(k.ring)-1], true
}

// Rotate moves the last entry to the front (for yank-pop cycling).
func (k *KillRing) Rotate() {
	if len(k.ring) <= 1 {
		return
	}
	last := k.ring[len(k.ring)-1]
	copy(k.ring[1:], k.ring[:len(k.ring)-1])
	k.ring[0] = last
}

// Len returns the number of entries.
func (k *KillRing) Len() int { return len(k.ring) }
