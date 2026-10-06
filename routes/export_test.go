package routes

// Test hooks for the external routes_test package.

// SetAudienceMintBudget sets the per-IP device mint budget and clears the
// spent counters; it returns the old budget.
func SetAudienceMintBudget(n int) int { return audMint.setMax(n) }

// SetOpenEndpointBudgets lifts (or restores) the per-IP budgets of the
// screen register and client-log endpoints; returns the old values.
func SetOpenEndpointBudgets(register, clientLog int) (int, int) {
	return waitingLimit.setMax(register), clientLogLimit.setMax(clientLog)
}

// SetEventCreateBudget changes the per-IP event creation budget (BUGLOG
// RS7) and returns the old one.
func SetEventCreateBudget(n int) int { return eventCreateLimit.setMax(n) }
