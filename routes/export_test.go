package routes

// Test hooks for the external routes_test package.

// SetAudienceMintBudget sets the per-IP device mint budget and clears the
// spent counters; it returns the old budget.
func SetAudienceMintBudget(n int) int {
	audMint.Lock()
	defer audMint.Unlock()
	old := audMintPerIP
	audMintPerIP = n
	audMint.win = nil
	return old
}
