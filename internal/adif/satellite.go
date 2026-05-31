package adif

import (
	"fmt"
	"strconv"

	"waveloggate/internal/config"
	"waveloggate/internal/debug"
)

// ApplySatellite applies transverter frequency offsets and injects satellite
// ADIF fields (PROP_MODE, SAT_NAME, SAT_MODE) into the parsed ADIF field map.
// This is called after parsing and band enrichment, before serialisation.
func ApplySatellite(fields map[string]string, cfg *config.Profile) {
	if cfg == nil || !cfg.SatEnabled {
		return
	}

	origFreq := fields["FREQ"]

	// --- TX frequency offset ---
	if origFreq != "" && cfg.SatTxOffsetMHz != 0 {
		if freqMHz, err := strconv.ParseFloat(origFreq, 64); err == nil {
			corrected := freqMHz + cfg.SatTxOffsetMHz
			fields["FREQ"] = fmt.Sprintf("%.6f", corrected)
			if band := FreqToBand(corrected); band != "" {
				fields["BAND"] = band
			}
			debug.Log("[SAT] FREQ: %.6f → %.6f MHz (band=%s)", freqMHz, corrected, fields["BAND"])
		}
	}

	// --- RX frequency offset ---
	if cfg.SatRxOffsetMHz != 0 {
		// Use existing FREQ_RX if present, otherwise fall back to the
		// original IF frequency (before TX offset was applied).
		rxSource := fields["FREQ_RX"]
		if rxSource == "" && origFreq != "" && cfg.SatRxOffsetMHz != cfg.SatTxOffsetMHz {
			rxSource = origFreq
		}
		if rxSource != "" {
			if rxMHz, err := strconv.ParseFloat(rxSource, 64); err == nil {
				correctedRX := rxMHz + cfg.SatRxOffsetMHz
				fields["FREQ_RX"] = fmt.Sprintf("%.6f", correctedRX)
				debug.Log("[SAT] FREQ_RX: %.6f → %.6f MHz", rxMHz, correctedRX)
			}
		}
	}

	// --- Inject satellite ADIF fields ---
	fields["PROP_MODE"] = "SAT"
	if cfg.SatName != "" {
		fields["SAT_NAME"] = cfg.SatName
	}
	if cfg.SatMode != "" {
		fields["SAT_MODE"] = cfg.SatMode
	}
	debug.Log("[SAT] injected PROP_MODE=SAT SAT_NAME=%s SAT_MODE=%s", cfg.SatName, cfg.SatMode)
}
