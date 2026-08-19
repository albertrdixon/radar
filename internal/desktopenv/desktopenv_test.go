package desktopenv

import "testing"

func TestGPUPolicyDefaultsToEmpty(t *testing.T) {
	t.Cleanup(func() { SetGPUPolicy("") })

	SetGPUPolicy("")
	if got := GPUPolicy(); got != "" {
		t.Errorf("GPUPolicy() = %q, want empty", got)
	}

	SetGPUPolicy("never")
	if got := GPUPolicy(); got != "never" {
		t.Errorf("GPUPolicy() = %q, want \"never\"", got)
	}
}
