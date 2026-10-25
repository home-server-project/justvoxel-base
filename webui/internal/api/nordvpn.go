package api

// ValidNordAccessToken checks input hygiene, not a vendor token format.
// Visible ASCII is valid UTF-8 and remains one unchanged CLI argument.
func ValidNordAccessToken(value string) bool {
	if len(value) == 0 || len(value) > 4096 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}
