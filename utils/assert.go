package utils

// Assert panics if the condition is false
func Assert(condition bool, msg string) {
	if !condition {
		panic(msg)
	}
}
