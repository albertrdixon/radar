//go:build !linux

package desktopenv

import "testing"

// The diagnostics section must stay absent on platforms with no host render
// knobs, rather than rendering an empty box in every macOS bug report.
func TestCollectIsNilOffLinux(t *testing.T) {
	SetGPUPolicy("on-demand")
	t.Cleanup(func() { SetGPUPolicy("") })

	if snap := Collect(); snap != nil {
		t.Errorf("Collect() = %+v, want nil", snap)
	}
}
