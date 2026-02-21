package orchestrator

// SetForTest sets the global orchestrator for testing.
func SetForTest(o ContainerOrchestrator) {
	mu.Lock()
	defer mu.Unlock()
	current = o
}

// ResetForTest clears the global orchestrator.
func ResetForTest() {
	mu.Lock()
	defer mu.Unlock()
	current = nil
}
