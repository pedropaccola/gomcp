package broken

// Bad exists so the sandbox always carries one type error.
func Bad() int {
	return "not an int"
}
