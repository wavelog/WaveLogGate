package adif

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var txPwrRe = regexp.MustCompile(`(?i)<TX_PWR:(\d+)>([^<]+)`)
var kIndexRe = regexp.MustCompile(`(?i)<K_INDEX:(\d+)>([^<]+)`)

// NormalizeTXPwr converts TX_PWR values to watts (handles kW and mW suffixes).
func NormalizeTXPwr(adif string) string {
	return txPwrRe.ReplaceAllStringFunc(adif, func(match string) string {
		sub := txPwrRe.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		raw := strings.TrimSpace(sub[2])
		lower := strings.ToLower(raw)

		var watts float64
		numStr := strings.TrimRight(lower, "kwm ")
		val, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return match
		}

		switch {
		case strings.Contains(lower, "kw"):
			watts = val * 1000
		case strings.Contains(lower, "mw"):
			watts = val * 0.001
		default:
			watts = val
		}

		result := strconv.FormatFloat(watts, 'f', -1, 64)
		return fmt.Sprintf("<TX_PWR:%d>%s", len(result), result)
	})
}

// NormalizeKIndex normalises K_INDEX: rounds to int, clamps 0-9, removes if NaN.
func NormalizeKIndex(adif string) string {
	return kIndexRe.ReplaceAllStringFunc(adif, func(match string) string {
		sub := kIndexRe.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		raw := strings.TrimSpace(sub[2])
		val, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(val) {
			return ""
		}
		rounded := int(math.Round(val))
		if rounded < 0 {
			rounded = 0
		}
		if rounded > 9 {
			rounded = 9
		}
		result := strconv.Itoa(rounded)
		return fmt.Sprintf("<K_INDEX:%d>%s", len(result), result)
	})
}

// bandMap maps band names to [lower, upper] frequency ranges in MHz.
var bandMap = []struct {
	name  string
	lower float64
	upper float64
}{
	{"2190m", 0.1357, 0.1378},
	{"630m", 0.472, 0.479},
	{"560m", 0.501, 0.504},
	{"160m", 1.8, 2.0},
	{"80m", 3.5, 4.0},
	{"60m", 5.06, 5.45},
	{"40m", 7.0, 7.3},
	{"30m", 10.1, 10.15},
	{"20m", 14.0, 14.35},
	{"17m", 18.068, 18.168},
	{"15m", 21.0, 21.45},
	{"12m", 24.890, 24.99},
	{"10m", 28.0, 29.7},
	{"6m", 50.0, 54.0},
	{"4m", 70.0, 71.0},
	{"2m", 144.0, 148.0},
	{"1.25m", 222.0, 225.0},
	{"70cm", 420.0, 450.0},
	{"33cm", 902.0, 928.0},
	{"23cm", 1240.0, 1300.0},
	{"13cm", 2300.0, 2450.0},
	{"9cm", 3300.0, 3500.0},
	{"6cm", 5650.0, 5925.0},
	{"3cm", 10000.0, 10500.0},
	{"1.25cm", 24000.0, 24050.0},
	{"6mm", 47000.0, 47200.0},
	{"4mm", 75500.0, 81000.0},
	{"2.5mm", 119980.0, 120020.0},
	{"2mm", 142000.0, 149000.0},
	{"1mm", 241000.0, 250000.0},
}

// FreqToBand returns the amateur band for a given frequency in MHz.
func FreqToBand(mhz float64) string {
	for _, b := range bandMap {
		if mhz >= b.lower && mhz <= b.upper {
			return b.name
		}
	}
	return ""
}
