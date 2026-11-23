package expression

import (
	"fmt"
)

func (e Expression) Check(ctx TypeContext) (CheckedExpression, error) {
	if ctx.MaxDepth <= 0 {
		ctx.MaxDepth = 64
	}
	if ctx.MaxNodes <= 0 {
		ctx.MaxNodes = 4096
	}
	state := checkState{ctx: ctx}
	t, err := state.check(e, "expr", 0)
	if err != nil {
		return CheckedExpression{}, err
	}
	if e.Type.Valid() && e.Type != t {
		return CheckedExpression{}, fmt.Errorf("expr: declared type %s conflicts with inferred type %s", e.Type, t)
	}
	return CheckedExpression{Expression: e, Type: t}, nil
}

func (e Expression) Validate(ctx TypeContext) error {
	_, err := e.Check(ctx)
	return err
}
