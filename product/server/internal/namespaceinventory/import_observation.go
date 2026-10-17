package namespaceinventory

// InspectImportMongo is a pure factual decoder using the exact same classifier
// as the two-pass raw capture. It creates no private source capability, origin
// ownership, zero head, historical bound, Permit or Ready state.
func InspectImportMongo(row MongoRow) Observation {
	value := classify(row)
	value.Origins = append([]Origin(nil), value.Origins...)
	value.Reasons = append([]string(nil), value.Reasons...)
	if value.RawHead != nil {
		head := *value.RawHead
		value.RawHead = &head
	}
	return value
}
