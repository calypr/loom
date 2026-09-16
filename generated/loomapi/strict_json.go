package loomapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// strictDecodeGenerated keeps mutation requests closed at the generated HTTP
// boundary. The generated Fiber binder otherwise silently drops unknown JSON
// members before the authoring domain can enforce its semantics-version guard.
func strictDecodeGenerated(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func (value *ApplyCommandsRequest) UnmarshalJSON(raw []byte) error {
	type wire ApplyCommandsRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ApplyCommandsRequest(decoded)
	return nil
}

func (value *AuthoringCommand) UnmarshalJSON(raw []byte) error {
	type wire AuthoringCommand
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = AuthoringCommand(decoded)
	return nil
}

func (value *Column) UnmarshalJSON(raw []byte) error {
	type wire Column
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = Column(decoded)
	return nil
}

func (value *ColumnSource) UnmarshalJSON(raw []byte) error {
	type wire ColumnSource
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ColumnSource(decoded)
	return nil
}

func (value *FieldSource) UnmarshalJSON(raw []byte) error {
	type wire FieldSource
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = FieldSource(decoded)
	return nil
}

func (value *AggregateSource) UnmarshalJSON(raw []byte) error {
	type wire AggregateSource
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = AggregateSource(decoded)
	return nil
}

func (value *LookupSource) UnmarshalJSON(raw []byte) error {
	type wire LookupSource
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	if decoded.Binding != nil || decoded.Key != nil {
		if decoded.Binding == nil || decoded.Key == nil || decoded.Match != nil || decoded.Path != nil {
			return errors.New("correlated lookup requires only binding and key")
		}
	} else if decoded.Match == nil {
		return errors.New("legacy lookup requires match")
	}
	*value = LookupSource(decoded)
	return nil
}

func (value *SourceWhere) UnmarshalJSON(raw []byte) error {
	type wire SourceWhere
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = SourceWhere(decoded)
	return nil
}

func (value *RelatedSelection) UnmarshalJSON(raw []byte) error {
	type wire RelatedSelection
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = RelatedSelection(decoded)
	return nil
}

func (value *SelectionCreateRequest) UnmarshalJSON(raw []byte) error {
	type wire SelectionCreateRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	switch decoded.Source.Kind {
	case "resources":
		if decoded.Source.Resources == nil || decoded.Source.PublishedOutput != nil {
			return errors.New("resources selection requires only the resources payload")
		}
	case "publishedOutput":
		if decoded.Source.PublishedOutput == nil || decoded.Source.Resources != nil {
			return errors.New("publishedOutput selection requires only the publishedOutput payload")
		}
	default:
		return errors.New("unsupported selection source kind")
	}
	*value = SelectionCreateRequest(decoded)
	return nil
}
