package service

// SetBcryptCostForTest lowers the password hashing cost so the suite does not
// spend most of its time deliberately being slow. Production keeps cost 12.
func SetBcryptCostForTest(c int) func() {
	previous := bcryptCost
	bcryptCost = c
	return func() { bcryptCost = previous }
}
