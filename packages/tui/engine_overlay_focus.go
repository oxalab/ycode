package tui

// engine_overlay_focus.go ports the overlay focus-restore state machine of
// reference/tui/src/tui.ts (setFocusInternal + OverlayFocusRestoreState + the
// handleInput focus-redirect path). See mapping/02-engine.md + tests/overlay-focus.md.

// overlayResumeStatus selects what a blocked overlay restores to.
type overlayResumeStatus int

const (
	resumeRestoreOverlay overlayResumeStatus = iota
	resumeFocusTarget
)

type overlayResume struct {
	status overlayResumeStatus
	target Component // for resumeFocusTarget
}

// overlayRestoreStatus is the focus-restore state.
type overlayRestoreStatus int

const (
	restoreInactive overlayRestoreStatus = iota
	restoreEligible
	restoreBlocked
)

type overlayFocusRestoreState struct {
	status    overlayRestoreStatus
	overlay   *overlayEntry
	blockedBy Component
	resume    overlayResume
}

type overlayFocusPolicy int

const (
	policyClear overlayFocusPolicy = iota
	policyPreserve
)

func inactiveRestore() overlayFocusRestoreState { return overlayFocusRestoreState{status: restoreInactive} }

// setFocusInternalLocked is the focus core: applies the restore policy, flips
// Focusable flags, and records the resulting restore state. Ports setFocusInternal.
func (t *TUI) setFocusInternalLocked(component Component, policy overlayFocusPolicy) {
	previousFocus := t.focusedComponent
	nextFocus := component

	var previousFocusedOverlay *overlayEntry
	if previousFocus != nil {
		previousFocusedOverlay = t.findVisibleOverlayByComponentLocked(previousFocus)
	}
	nextFocusIsOverlay := nextFocus != nil && t.someOverlayByComponentLocked(nextFocus)
	restoreState := t.getVisibleOverlayFocusRestoreLocked()

	if nextFocus != nil && !nextFocusIsOverlay {
		if restoreState.status == restoreBlocked && restoreState.blockedBy == previousFocus {
			if restoreState.resume.status == resumeFocusTarget || !t.isComponentMountedLocked(restoreState.blockedBy) {
				nextFocus = t.resolveBlockedOverlayFocusResumeLocked(restoreState)
			} else {
				t.overlayFocusRestore = overlayFocusRestoreState{
					status:    restoreBlocked,
					overlay:   restoreState.overlay,
					blockedBy: nextFocus,
					resume:    restoreState.resume,
				}
			}
		} else if previousFocusedOverlay != nil && restoreState.status != restoreInactive &&
			restoreState.overlay == previousFocusedOverlay && !t.isOverlayFocusAncestorLocked(previousFocusedOverlay, nextFocus) {
			t.overlayFocusRestore = overlayFocusRestoreState{
				status:    restoreBlocked,
				overlay:   previousFocusedOverlay,
				blockedBy: nextFocus,
				resume:    overlayResume{status: resumeRestoreOverlay},
			}
		}
	} else if nextFocus == nil {
		if restoreState.status == restoreBlocked && restoreState.blockedBy == previousFocus {
			nextFocus = t.resolveBlockedOverlayFocusResumeLocked(restoreState)
		} else if policy == policyClear {
			t.clearOverlayFocusRestoreLocked()
		}
	}

	if f, ok := t.focusedComponent.(Focusable); ok {
		f.SetFocused(false)
	}
	t.focusedComponent = nextFocus
	if nextFocus != nil {
		if f, ok := nextFocus.(Focusable); ok {
			f.SetFocused(true)
		}
	}

	var focusedOverlay *overlayEntry
	if nextFocus != nil {
		focusedOverlay = t.findVisibleOverlayByComponentLocked(nextFocus)
	}
	if focusedOverlay != nil {
		t.overlayFocusRestore = overlayFocusRestoreState{status: restoreEligible, overlay: focusedOverlay}
	}
}

// ---------------------------------------------------------------------------
// FSM helpers
// ---------------------------------------------------------------------------

func (t *TUI) findVisibleOverlayByComponentLocked(c Component) *overlayEntry {
	for _, e := range t.overlays {
		if e.component == c && t.isOverlayVisibleLocked(e) {
			return e
		}
	}
	return nil
}

func (t *TUI) someOverlayByComponentLocked(c Component) bool {
	for _, e := range t.overlays {
		if e.component == c {
			return true
		}
	}
	return false
}

func (t *TUI) getVisibleOverlayFocusRestoreLocked() overlayFocusRestoreState {
	s := t.overlayFocusRestore
	if s.status == restoreInactive {
		return s
	}
	if !containsOverlay(t.overlays, s.overlay) || !t.isOverlayVisibleLocked(s.overlay) {
		return inactiveRestore()
	}
	return s
}

func (t *TUI) resolveBlockedOverlayFocusResumeLocked(s overlayFocusRestoreState) Component {
	if s.resume.status == resumeRestoreOverlay {
		return s.overlay.component
	}
	t.clearOverlayFocusRestoreLocked()
	return s.resume.target
}

func (t *TUI) clearOverlayFocusRestoreLocked() {
	t.overlayFocusRestore = inactiveRestore()
}

func (t *TUI) clearOverlayFocusRestoreForLocked(overlay *overlayEntry) {
	if t.overlayFocusRestore.status != restoreInactive && t.overlayFocusRestore.overlay == overlay {
		t.clearOverlayFocusRestoreLocked()
	}
}

func (t *TUI) retargetOverlayPreFocusLocked(removed *overlayEntry) {
	for _, o := range t.overlays {
		if o != removed && o.preFocus == removed.component {
			o.preFocus = removed.preFocus
		}
	}
}

func (t *TUI) isComponentMountedLocked(c Component) bool {
	for _, child := range t.Children {
		if t.containsComponentLocked(child, c) {
			return true
		}
	}
	return false
}

func (t *TUI) containsComponentLocked(root, target Component) bool {
	if root == target {
		return true
	}
	if cont, ok := root.(*Container); ok {
		for _, ch := range cont.Children {
			if t.containsComponentLocked(ch, target) {
				return true
			}
		}
	}
	return false
}

func (t *TUI) isOverlayFocusAncestorLocked(entry *overlayEntry, component Component) bool {
	visited := map[Component]bool{}
	current := entry.preFocus
	for current != nil && !visited[current] {
		if current == component {
			return true
		}
		visited[current] = true
		var next Component
		for _, o := range t.overlays {
			if o.component == current {
				next = o.preFocus
				break
			}
		}
		current = next
	}
	return false
}

// focusRedirectLocked implements the handleInput focus-redirect/restore path: if
// the focused overlay became invisible, redirect; if focus left an overlay, restore.
func (t *TUI) focusRedirectLocked() {
	// Focused overlay visibility re-check.
	var focusedOverlay *overlayEntry
	for _, o := range t.overlays {
		if o.component == t.focusedComponent {
			focusedOverlay = o
			break
		}
	}
	if focusedOverlay != nil && !t.isOverlayVisibleLocked(focusedOverlay) {
		top := t.getTopmostVisibleOverlayLocked()
		if top != nil {
			t.setFocusInternalLocked(top.component, policyClear)
		} else {
			t.setFocusInternalLocked(focusedOverlay.preFocus, policyPreserve)
		}
	}

	focusIsOverlay := t.someOverlayByComponentLocked(t.focusedComponent)
	if !focusIsOverlay {
		restoreState := t.getVisibleOverlayFocusRestoreLocked()
		switch {
		case restoreState.status == restoreEligible:
			t.setFocusInternalLocked(restoreState.overlay.component, policyClear)
		case restoreState.status == restoreBlocked && restoreState.blockedBy != t.focusedComponent:
			if restoreState.resume.status == resumeRestoreOverlay {
				t.setFocusInternalLocked(restoreState.overlay.component, policyClear)
			} else {
				t.clearOverlayFocusRestoreLocked()
				t.setFocusInternalLocked(restoreState.resume.target, policyClear)
			}
		}
	}
}
