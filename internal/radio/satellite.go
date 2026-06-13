package radio

import (
	"waveloggate/internal/config"
	"waveloggate/internal/debug"
)

// ApplySatOffsets applies transverter frequency offsets to a RigStatus.
// FreqA is the main/RX VFO, FreqB is the TX VFO in split mode.
// All frequencies are in Hz.
func ApplySatOffsets(status *RigStatus, cfg *config.Profile) {
	if cfg == nil || !cfg.SatEnabled {
		return
	}

	txOffsetHz := cfg.SatTxOffsetMHz * 1_000_000
	rxOffsetHz := cfg.SatRxOffsetMHz * 1_000_000

	if !status.Split {
		// Simplex: single VFO, use TX offset.
		status.FreqA += txOffsetHz
	} else {
		// Split: FreqA=RX, FreqB=TX.
		status.FreqA += rxOffsetHz
		status.FreqB += txOffsetHz
	}
	debug.Log("[SAT] radio offsets applied: FreqA=%.0f FreqB=%.0f split=%v", status.FreqA, status.FreqB, status.Split)
}

// ReverseSatOffset subtracts the satellite offset from a displayed frequency
// to recover the IF frequency for sending to the radio hardware.
func ReverseSatOffset(hz int64, offsetMHz float64) int64 {
	return hz - int64(offsetMHz*1_000_000)
}
