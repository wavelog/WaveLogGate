// Package hamlib provides Hamlib/rigctld process management and model data.
//
// The model list below was hand-curated from the Hamlib model registry.
// To regenerate from a local rigctld installation run:
//
//	rigctld --list | awk 'NR>2 && $1~/^[0-9]+$/ {print $0}'
package hamlib

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"waveloggate/internal/debug"
)

// RadioModel is a single entry from the Hamlib rig model list.
type RadioModel struct {
	ID           int    `json:"id"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
}

// MarshalJSON implements custom JSON marshaling to include a display label.
func (rm RadioModel) MarshalJSON() ([]byte, error) {
	label := fmt.Sprintf("%d %s", rm.ID, rm.Model)
	return json.Marshal(struct {
		ID           int    `json:"id"`
		Manufacturer string `json:"manufacturer"`
		Model        string `json:"model"`
		Label        string `json:"label"`
		Value        string `json:"value"`
	}{rm.ID, rm.Manufacturer, rm.Model, label, label})
}

// Dynamic model list cache.
// modelCacheMu protects cachedOnce and cachedModels so that
// InvalidateModelCache can replace them safely while getModelList may be
// executing concurrently. We use *sync.Once (pointer) to avoid copying a
// sync.Once that has already been used.
var (
	modelCacheMu sync.Mutex
	cachedOnce   = new(sync.Once)
	cachedModels []RadioModel
)

// getDynamicModels attempts to get the actual model list from the installed rigctld.
// Returns (models, nil) on success, (nil, error) if rigctld not available.
func getDynamicModels() ([]RadioModel, error) {
	// Try to find rigctld binary
	rigctldPath, err := RigctldPath()
	if err != nil {
		// rigctld not found, use hardcoded fallback
		return nil, fmt.Errorf("rigctld not found: %w", err)
	}

	// Run rigctld --list to get actual model list
	cmd := exec.Command(rigctldPath, "--list")
	setCmdAttrs(cmd)
	output, err := cmd.Output()
	if err != nil {
		// rigctld exists but --list failed, use hardcoded fallback
		return nil, fmt.Errorf("rigctld --list failed: %w", err)
	}

	// Parse the output
	// Format: "ID  Manufacturer  Model"
	// Example: "1    Hamlib  Dummy"
	models := parseRigctldList(string(output))
	if len(models) == 0 {
		// Parsing failed or empty list, use fallback
		return nil, fmt.Errorf("no models found in rigctld --list output")
	}

	return models, nil
}

// parseRigctldList parses the output from "rigctld --list"
func parseRigctldList(output string) []RadioModel {
	var models []RadioModel

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip header lines and empty lines
		if line == "" || strings.HasPrefix(line, "Model") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "Rig") {
			continue
		}

		// Split by whitespace and handle variable column count
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue // Need at least ID, Manufacturer, Model
		}

		// First field is ID
		id, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}

		// Second field is Manufacturer (or sometimes Model if no Manufacturer)
		// Third field is Model (or might be more columns)

		// Heuristic: If field[1] looks like a model name (contains numbers/letters) and field[2] looks like version/date, then field[1] is likely Model
		// Otherwise field[1] is Manufacturer, field[2] is Model

		var manufacturer, model string

		// Check if we have a version-like string in fields[2] (contains date/version pattern)
		if len(fields) >= 4 && (strings.Contains(fields[2], ".") || strings.Contains(fields[2], "Stable") || strings.Contains(fields[2], "RIG_MODEL")) {
			// Format: ID Model version... (no manufacturer column)
			model = fields[1]
			manufacturer = "Unknown"
		} else {
			// Format: ID Manufacturer Model...
			manufacturer = fields[1]
			model = fields[2]
		}

		models = append(models, RadioModel{
			ID:           id,
			Manufacturer: manufacturer,
			Model:        model,
		})
	}

	return models
}

// getModelList returns the model list, trying dynamic first, then falling back to hardcoded.
func getModelList() []RadioModel {
	// Read the current once pointer under the lock, then call Do outside the
	// lock so that slow rigctld execution doesn't hold modelCacheMu.
	modelCacheMu.Lock()
	once := cachedOnce
	modelCacheMu.Unlock()

	once.Do(func() {
		models, err := getDynamicModels()
		modelCacheMu.Lock()
		defer modelCacheMu.Unlock()
		if err != nil {
			debug.Log("[HAMLIB] Using fallback model list: %v", err)
			cachedModels = allModels
		} else {
			debug.Log("[HAMLIB] Using dynamic model list from rigctld: %d models", len(models))
			cachedModels = models
		}
	})

	modelCacheMu.Lock()
	result := cachedModels
	modelCacheMu.Unlock()
	return result
}

// SearchModels returns all models whose manufacturer or model name contains q
// (case-insensitive). An empty query returns the full list (resets search).
func SearchModels(q string) []RadioModel {
	// Always get fresh model list for empty query (reset behavior)
	if q == "" {
		models := getModelList()
		result := make([]RadioModel, len(models))
		copy(result, models)
		return result
	}

	// Non-empty query: filter the model list
	q = strings.ToLower(q)
	models := getModelList()
	var out []RadioModel
	for _, m := range models {
		if strings.Contains(strings.ToLower(m.Manufacturer), q) ||
			strings.Contains(strings.ToLower(m.Model), q) {
			out = append(out, m)
		}
	}
	return out
}

// InvalidateModelCache clears the cached model list, forcing a refresh on next SearchModels call.
// Useful when rigctld is updated or reinstalled.
func InvalidateModelCache() {
	modelCacheMu.Lock()
	defer modelCacheMu.Unlock()
	cachedOnce = new(sync.Once)
	cachedModels = nil
	debug.Log("[HAMLIB] Model cache invalidated")
}

// allModels is the embedded Hamlib model list.
// IDs match the hamlib rig model numbering (as returned by rigctld --list).
var allModels = []RadioModel{
	// Special / virtual
	{1, "Hamlib", "Dummy"},
	{2, "Hamlib", "NET rigctl"},

	// Kenwood
	{101, "Kenwood", "TS-50S"},
	{102, "Kenwood", "TS-440S"},
	{103, "Kenwood", "TS-450S"},
	{104, "Kenwood", "TS-570D"},
	{105, "Kenwood", "TS-570S"},
	{106, "Kenwood", "TS-690S"},
	{107, "Kenwood", "TS-711"},
	{108, "Kenwood", "TS-790"},
	{109, "Kenwood", "TS-811"},
	{110, "Kenwood", "TS-850"},
	{111, "Kenwood", "TS-950S"},
	{112, "Kenwood", "TS-950SDX"},
	{113, "Kenwood", "TS-2000"},
	{114, "Kenwood", "R-5000"},
	{115, "Kenwood", "TS-870S"},
	{116, "Kenwood", "TM-D700"},
	{117, "Kenwood", "TS-940"},
	{118, "Kenwood", "TS-680S"},
	{119, "Kenwood", "TS-140S"},
	{120, "Kenwood", "TM-G707"},
	{121, "Kenwood", "TH-D7A"},
	{122, "Kenwood", "R-5000"},
	{123, "Kenwood", "TM-V7"},
	{124, "Kenwood", "TH-G71"},
	{125, "Kenwood", "TM-D710"},
	{126, "Kenwood", "TS-480SAT"},
	{127, "Kenwood", "TS-480HX"},
	{128, "Kenwood", "TS-590S"},
	{129, "Kenwood", "TS-590SG"},
	{130, "Kenwood", "TS-990S"},
	{131, "Kenwood", "TS-890S"},

	// Yaesu
	{201, "Yaesu", "FT-747GX"},
	{202, "Yaesu", "FT-757GX"},
	{203, "Yaesu", "FT-757GXII"},
	{204, "Yaesu", "FT-767GX"},
	{205, "Yaesu", "FT-840"},
	{206, "Yaesu", "FT-900"},
	{207, "Yaesu", "FT-920"},
	{208, "Yaesu", "FT-990"},
	{209, "Yaesu", "FT-1000D"},
	{210, "Yaesu", "FT-1000MP"},
	{211, "Yaesu", "FT-1000MP Mk-V"},
	{212, "Yaesu", "FT-100"},
	{213, "Yaesu", "VR-5000"},
	{214, "Yaesu", "FT-847"},
	{215, "Yaesu", "FT-736R"},
	{216, "Yaesu", "FRG-100"},
	{217, "Yaesu", "FRG-9600"},
	{218, "Yaesu", "FRG-8800"},
	{219, "Yaesu", "FT-817"},
	{221, "Yaesu", "FT-897"},
	{222, "Yaesu", "FT-857"},
	{223, "Yaesu", "FT-950"},
	{224, "Yaesu", "FT-2000"},
	{225, "Yaesu", "FT-450"},
	{226, "Yaesu", "FT-9000"},
	{227, "Yaesu", "FT-980"},
	{228, "Yaesu", "VX-1700"},
	{229, "Yaesu", "FT-450D"},
	{230, "Yaesu", "FT-891"},
	{231, "Yaesu", "FT-991"},
	{232, "Yaesu", "FT-817ND"},
	{233, "Yaesu", "FT-991A"},
	{234, "Yaesu", "FT-818"},
	{235, "Yaesu", "FTDX3000"},
	{236, "Yaesu", "FTDX5000"},
	{237, "Yaesu", "FTDX1200"},
	{238, "Yaesu", "FTDX101D"},
	{239, "Yaesu", "FTDX101MP"},
	{240, "Yaesu", "FTDX10"},
	{241, "Yaesu", "FT-710"},
	{242, "Yaesu", "FT-818ND"},

	// Icom (classic CI-V range)
	{301, "Icom", "IC-1275"},
	{302, "Icom", "IC-271"},
	{303, "Icom", "IC-275"},
	{304, "Icom", "IC-471"},
	{305, "Icom", "IC-475"},
	{306, "Icom", "IC-575"},
	{307, "Icom", "IC-703"},
	{308, "Icom", "IC-706"},
	{309, "Icom", "IC-706MkII"},
	{310, "Icom", "IC-706MkIIG"},
	{311, "Icom", "IC-707"},
	{312, "Icom", "IC-718"},
	{313, "Icom", "IC-725"},
	{314, "Icom", "IC-726"},
	{315, "Icom", "IC-728"},
	{316, "Icom", "IC-729"},
	{317, "Icom", "IC-735"},
	{318, "Icom", "IC-736"},
	{319, "Icom", "IC-737"},
	{320, "Icom", "IC-738"},
	{321, "Icom", "IC-746"},
	{322, "Icom", "IC-751"},
	{323, "Icom", "IC-756"},
	{324, "Icom", "IC-756Pro"},
	{325, "Icom", "IC-756ProII"},
	{326, "Icom", "IC-765"},
	{327, "Icom", "IC-775"},
	{328, "Icom", "IC-7800"},
	{329, "Icom", "IC-7000"},
	{330, "Icom", "IC-7200"},
	{331, "Icom", "IC-7600"},
	{332, "Icom", "IC-7700"},
	{333, "Icom", "IC-7410"},
	{334, "Icom", "IC-746Pro"},
	{335, "Icom", "IC-756ProIII"},
	{336, "Icom", "IC-7100"},
	{337, "Icom", "IC-910"},
	{338, "Icom", "IC-9100"},
	{339, "Icom", "IC-R10"},
	{340, "Icom", "IC-R20"},
	{341, "Icom", "IC-R6"},
	{342, "Icom", "IC-R71"},
	{343, "Icom", "IC-R72"},
	{344, "Icom", "IC-R75"},
	{345, "Icom", "IC-R7000"},
	{346, "Icom", "IC-R7100"},
	{347, "Icom", "IC-R8500"},
	{348, "Icom", "IC-R9000"},

	// Icom (newer USB/CI-V range, 3000+)
	{3001, "Icom", "IC-7610"},
	{3055, "Icom", "IC-7760"},
	{3060, "Icom", "IC-9700"},
	{3073, "Icom", "IC-7300"},
	{3074, "Icom", "IC-705"},
	{3075, "Icom", "IC-7851"},
	{3076, "Icom", "IC-R8600"},
	{3080, "Icom", "IC-7760"},
	{3085, "Icom", "IC-905"},

	// Elecraft
	{401, "Elecraft", "K2"},
	{402, "Elecraft", "K3"},
	{403, "Elecraft", "KX3"},
	{404, "Elecraft", "KX2"},
	{405, "Elecraft", "K3S"},
	{406, "Elecraft", "K4"},

	// FlexRadio
	{1035, "FlexRadio", "Flex-1500"},
	{1036, "FlexRadio", "Flex-3000"},
	{1037, "FlexRadio", "Flex-5000"},
	{1040, "FlexRadio", "Flex-6300"},
	{1041, "FlexRadio", "Flex-6500"},
	{1042, "FlexRadio", "Flex-6700"},

	// TenTec
	{601, "TenTec", "Argonaut VI"},
	{602, "TenTec", "Omni VII"},
	{603, "TenTec", "Eagle"},
	{604, "TenTec", "Jupiter"},
	{605, "TenTec", "Orion"},
	{606, "TenTec", "Orion II"},
	{607, "TenTec", "Paragon"},
	{608, "TenTec", "RX-340"},
	{609, "TenTec", "RX-350"},

	// Alinco
	{701, "Alinco", "DX-77T"},
	{702, "Alinco", "DX-70T"},
	{703, "Alinco", "DR-605T"},
	{704, "Alinco", "DR-135T"},
	{705, "Alinco", "DR-235T"},
	{706, "Alinco", "DR-435T"},
	{707, "Alinco", "DX-SR8T"},

	// Uniden / Bearcat / Drake
	{801, "Drake", "R8A"},
	{802, "Drake", "R8B"},

	// AOR
	{1201, "AOR", "AR-7030"},
	{1202, "AOR", "AR-3000A"},
	{1203, "AOR", "AR-5000"},
	{1204, "AOR", "AR-8000"},
	{1205, "AOR", "AR-8200"},
	{1206, "AOR", "AR-2700"},
	{1207, "AOR", "AR-8600"},

	// WinRadio / Winradio
	{1901, "WinRadio", "WR-1000i"},
	{1902, "WinRadio", "WR-1500i"},
	{1903, "WinRadio", "WR-3100i"},
	{1904, "WinRadio", "WR-3150i"},
	{1905, "WinRadio", "WR-3500i"},
	{1906, "WinRadio", "WR-3700i"},

	// ICOM PCR receivers
	{1801, "Icom", "PCR-100"},
	{1802, "Icom", "PCR-1000"},

	// Rohde & Schwarz
	{1501, "Rohde&Schwarz", "EK895"},

	// SDR / software defined
	{2901, "SoftRock", "SoftRock RXTX"},
	{2902, "KTH", "SDR-1000"},
}
