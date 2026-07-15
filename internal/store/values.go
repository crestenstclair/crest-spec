package store

func stringVal(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func int64Val(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func float64Val(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
