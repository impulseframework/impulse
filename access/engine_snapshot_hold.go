package access

import "sync/atomic"

// heldStore is the store manager of one call of the user manager: the same
// store, where a write is recorded on the engine as it is made and its
// announcement to the other instances is held until the call ends. The type is
// how a call knows it runs inside another that already holds
// (userManager.holdAnnouncement).
type heldStore struct {
	*storeManager
}

// holdAnnouncement begins one public call of the user manager that writes
// policy. It returns the user manager the call goes on with and what the call
// defers:
//
//	u, announce := u.holdAnnouncement()
//	defer announce()
//
// Through the manager it returns, every write of the call is recorded on the
// engine the moment it is stored, so a check on this instance sees each row at
// once (snapshotEngine.policyWritten), and no write announces. announce then
// tells the other instances once, for the whole call, and only where the call
// wrote something: a call over thirty rows makes one announcement and not
// thirty, a call that fails part-way announces what it wrote, and a signal
// channel that is down costs the call one wait of the announcement's timeout
// and one report, not one for each row. The announcement is made before the
// call returns, as a single write's is (snapshotEngine.announce).
//
// The receiver is left as it was: the manager returned is a copy for the span
// of the one call, so two calls at once hold apart. A call made from inside a
// held one holds nothing of its own and announces nothing: the outer call
// announces for both. A user manager that is not wired to the engine (a store
// of a test's own, a store manager that reports its writes to nothing) is
// returned as it is.
func (u *userManager) holdAnnouncement() (held *userManager, announce func()) {
	manager, isManager := u.store.(*storeManager)
	engine, isEngine := u.evaluator.(*snapshotEngine)
	if !isManager || !isEngine || manager.onPolicyChange == nil {
		return u, announceNothing
	}

	var wrote atomic.Bool
	store := *manager
	store.onPolicyChange = func() {
		engine.policyWritten()
		wrote.Store(true)
	}
	call := *u
	call.store = &heldStore{storeManager: &store}

	return &call, func() {
		if wrote.Load() {
			engine.announce()
		}
	}
}

// announceNothing is what a call defers where nothing is held.
func announceNothing() {
}
