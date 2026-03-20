package proxy

import (
	"encoding/json"
	"fmt"
	"strings"
)

// countryLocaleMap maps ISO country names to BCP-47 locale tags.
var countryLocaleMap = map[string]string{
	"United States":  "en-US",
	"United Kingdom": "en-GB",
	"Canada":         "en-CA",
	"Australia":      "en-AU",
	"Germany":        "de-DE",
	"France":         "fr-FR",
	"Spain":          "es-ES",
	"Italy":          "it-IT",
	"Brazil":         "pt-BR",
	"Portugal":       "pt-PT",
	"Japan":          "ja-JP",
	"South Korea":    "ko-KR",
	"China":          "zh-CN",
	"Taiwan":         "zh-TW",
	"Russia":         "ru-RU",
	"India":          "hi-IN",
	"Mexico":         "es-MX",
	"Netherlands":    "nl-NL",
	"Poland":         "pl-PL",
	"Turkey":         "tr-TR",
	"Sweden":         "sv-SE",
	"Norway":         "nb-NO",
	"Denmark":        "da-DK",
	"Finland":        "fi-FI",
	"Indonesia":      "id-ID",
	"Thailand":       "th-TH",
	"Vietnam":        "vi-VN",
	"Ukraine":        "uk-UA",
	"Czech Republic": "cs-CZ",
	"Romania":        "ro-RO",
	"Argentina":      "es-AR",
	"Colombia":       "es-CO",
}

// localeForCountry returns a BCP-47 locale for the given country name.
// Falls back to "en-US" if the country is not in the map.
func localeForCountry(country string) string {
	if loc, ok := countryLocaleMap[country]; ok {
		return loc
	}
	// Try case-insensitive match
	lower := strings.ToLower(country)
	for k, v := range countryLocaleMap {
		if strings.ToLower(k) == lower {
			return v
		}
	}
	return "en-US"
}

// SyncFingerprintToProxy takes an existing fingerprint JSON string and a GeoInfo,
// injects geolocation, timezone, WebRTC IP, and locale fields, returns updated JSON.
func SyncFingerprintToProxy(fpJSON string, geo *GeoInfo) (string, error) {
	if geo == nil {
		return "", fmt.Errorf("geo info is nil")
	}

	var fp map[string]interface{}
	if err := json.Unmarshal([]byte(fpJSON), &fp); err != nil {
		return "", fmt.Errorf("parse fingerprint JSON: %w", err)
	}

	fp["geolocation:latitude"] = geo.Lat
	fp["geolocation:longitude"] = geo.Lon
	fp["geolocation:accuracy"] = 50.0
	fp["timezone"] = geo.Timezone
	fp["webrtc:ipv4"] = geo.IP
	fp["navigator.language"] = localeForCountry(geo.Country)

	out, err := json.Marshal(fp)
	if err != nil {
		return "", fmt.Errorf("marshal fingerprint: %w", err)
	}
	return string(out), nil
}
