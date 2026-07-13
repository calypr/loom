package compiler

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func cloneRowIdentity(identity *RowIdentity) *RowIdentity {
	if identity == nil {
		return nil
	}
	copy := *identity
	copy.Fields = cloneStrings(identity.Fields)
	return &copy
}
