package compiler

func appendUniqueRule(rules []string, rule string) []string {
	for _, existing := range rules {
		if existing == rule {
			return rules
		}
	}
	return append(rules, rule)
}
