package driversdk

import "strings"

// LightTypeVariable is the variable a light driver publishes its LightType
// under, so the apps can draw a matching icon.
const LightTypeVariable = "light_type"

// LightType is what kind of light a device is.
//
// The list and the setting belong to the platform, not to a driver: the
// installer picks a light's type in the console, for any light, and the apps
// draw an icon for each of these and for nothing else. A driver need do
// nothing, and should not offer a light type in its config schema. It may
// suggest one, shown while the installer leaves the setting on Automatic, by
// publishing it under LightTypeVariable. A value that is not on the list is
// shown as Generic, and the controller says so in the driver's log.
//
// The controller keeps the same list (uimodel.LightTypes) and the UI model
// schema enumerates it. Adding a type is a platform change, made in all three.
type LightType = string

const (
	LightTypeGeneric    LightType = "Generic"
	LightTypeSconce     LightType = "Sconce"
	LightTypeFixture    LightType = "Fixture"
	LightTypeUplight    LightType = "Uplight"
	LightTypeDownlight  LightType = "Downlight"
	LightTypeLED        LightType = "LED"
	LightTypePendant    LightType = "Pendant"
	LightTypeLamp       LightType = "Lamp"
	LightTypeColor      LightType = "Color"
	LightTypeCCT        LightType = "CCT"
	LightTypeChandelier LightType = "Chandelier"
	LightTypeCabinet    LightType = "Cabinet"
	LightTypeStepLight  LightType = "Step Light"
)

// LightTypes is every light type, Generic first: the enum to offer in a
// driver's config schema.
var LightTypes = []LightType{
	LightTypeGeneric, LightTypeSconce, LightTypeFixture, LightTypeUplight,
	LightTypeDownlight, LightTypeLED, LightTypePendant, LightTypeLamp,
	LightTypeColor, LightTypeCCT, LightTypeChandelier, LightTypeCabinet,
	LightTypeStepLight,
}

// NormalizeLightType returns the LightTypes entry s names, in any letter case
// and whether a two-word type is spaced, hyphenated or underscored
// ("step_light" is "Step Light"), and true. A blank s, or one that names no
// light type, returns "" and false.
func NormalizeLightType(s string) (LightType, bool) {
	k := lightTypeKey(s)
	if k == "" {
		return "", false
	}
	for _, t := range LightTypes {
		if lightTypeKey(t) == k {
			return t, true
		}
	}
	return "", false
}

func lightTypeKey(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == ' ' || r == '_' || r == '-'
	}), " ")
}
