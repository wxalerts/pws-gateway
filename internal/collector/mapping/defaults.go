package mapping

import "github.com/wxalerts/pws-gateway/internal/model"

// reliableFields lists, for models whose output is known to include fields
// that should not be stored by default, the canonical fields that are. A model
// that is not in this table has every field it reported selected by default.
// The owner can always tick or untick any field; this is only the starting
// point.
//
// Keep this table small and add a line only with a reason.
var reliableFields = map[string][]string{
	// The whole 5-in-1 message set is good: temperature, humidity, wind, wind
	// direction, rain and battery.
	"Acurite-5n1": nil, // nil means "everything seen"
	// The 609TXC is a temperature and humidity probe. Its battery flag and
	// status byte are not useful, so only those two are on by default.
	"Acurite-609TXC": {model.FieldTempC, model.FieldHumidityPct},
	// The WxAlerts screen node reports air quality and battery as raw fields
	// that have no canonical name. Its three canonical readings are the ones
	// worth keeping; battery_ok is derived from a millivolt count that is not
	// calibrated.
	"WxAlerts-WxLink": {model.FieldTempC, model.FieldHumidityPct, model.FieldPressureHPa},
}

// DefaultFields returns the canonical fields selected by default for a
// station of the given model, given the canonical fields it has reported so
// far (seen). The result is a subset of seen, in registry order. An unknown
// model gets everything seen.
func DefaultFields(modelName string, seen []string) []string {
	allow, restricted := reliableFields[modelName]
	if allow == nil {
		restricted = false
	}
	ok := map[string]bool{}
	for _, f := range allow {
		ok[f] = true
	}
	have := map[string]bool{}
	for _, f := range seen {
		have[f] = true
	}
	out := make([]string, 0, len(seen))
	for _, f := range model.Registry {
		if have[f.Name] && (!restricted || ok[f.Name]) {
			out = append(out, f.Name)
		}
	}
	return out
}
