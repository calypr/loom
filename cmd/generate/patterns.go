package main

import "fmt"

const (
	fhirYearPattern         = "([0-9]([0-9]([0-9][1-9]|[1-9]0)|[1-9]00)|[1-9]000)"
	fhirMonthPattern        = "(0[1-9]|1[0-2])"
	fhirDayPattern          = "(0[1-9]|[1-2][0-9]|3[0-1])"
	fhirClockPattern        = `([01][0-9]|2[0-3]):[0-5][0-9]:([0-5][0-9]|60)(\.[0-9]{1,9})?`
	fhirZonePattern         = `(Z|(\+|-)((0[0-9]|1[0-3]):[0-5][0-9]|14:00))`
	fhirOptionalZonePattern = `(Z|(\+|-)((0[0-9]|1[0-3]):[0-5][0-9]|14:00)?)?`
)

func fastStringPattern(pattern, value string) string {
	switch pattern {
	case `^^[\s\S]+$$`, `^[\s\S]+$`, `[ \r\n\t\S]+`:
		return value + ` == ""`
	case `^\S*$`:
		return "hasFHIRSchemaSpace(" + value + ")"
	case `^[^\s]+( [^\s]+)*$`:
		return "!validFHIRCode(" + value + ")"
	case `^[A-Za-z0-9\-\.]{1,64}$`:
		return "!validFHIRID(" + value + ")"
	case "^" + fhirYearPattern + "(-" + fhirMonthPattern + "(-" + fhirDayPattern + ")?)?$":
		return "!validFHIRDate(" + value + ")"
	case "^" + fhirYearPattern + "(-" + fhirMonthPattern + "(-" + fhirDayPattern + "(T" + fhirClockPattern + ")?)?" + fhirOptionalZonePattern + ")?$":
		return "!validFHIRDateTime(" + value + ", false)"
	case "^" + fhirYearPattern + "-" + fhirMonthPattern + "-" + fhirDayPattern + "T" + fhirClockPattern + fhirZonePattern + "$":
		return "!validFHIRDateTime(" + value + ", true)"
	case "^" + fhirClockPattern + "$":
		return "!validFHIRTime(" + value + ")"
	case `^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$`:
		return "!validFHIRBase64(" + value + ")"
	case `^urn:oid:[0-2](\.(0|[1-9][0-9]*))+$`:
		return "!validFHIROID(" + value + ")"
	case `^urn:uuid:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`:
		return "!validFHIRUUID(" + value + ")"
	case `^[0]|[-+]?[1-9][0-9]*$`:
		return "!validFHIRInteger64(" + value + ")"
	default:
		return ""
	}
}

func invalidStringPattern(pattern, value string, regexNames map[string]string) string {
	if condition := fastStringPattern(pattern, value); condition != "" {
		return condition
	}
	return fmt.Sprintf("!%s.MatchString(%s)", regexNames[pattern], value)
}
