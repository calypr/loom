package recipe

func (b Bundle) ExpandFragments() (Bundle, error) {
	if b.Fragments == nil {
		return b, nil
	}
	expanded, err := b.Fragments.ExpandBundle(b)
	if err != nil {
		return Bundle{}, err
	}
	expanded.Fragments = nil
	return expanded, nil
}
