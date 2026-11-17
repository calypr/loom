package server

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
)

// TestOpenAPIAdapterStatusesMatchDeclaredResponses links the status switches
// in each strict adapter to the same embedded OpenAPI operation that generated
// the response union. Authentication failures are handled by the shared HTTP
// middleware, so a declared 401 need not be repeated in every adapter switch.
func TestOpenAPIAdapterStatusesMatchDeclaredResponses(t *testing.T) {
	declared, err := declaredOpenAPIStatuses()
	if err != nil {
		t.Fatal(err)
	}
	handled, err := adapterOpenAPIStatuses()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOpenAPIStatusCoverage(declared, handled); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAPIStatusCoverageRejectsMissingAndUndeclaredStatuses(t *testing.T) {
	if err := validateOpenAPIStatusCoverage(
		map[string]map[int]struct{}{"getThing": map[int]struct{}{http.StatusOK: {}, http.StatusUnauthorized: {}, http.StatusServiceUnavailable: {}}},
		map[string]map[int]struct{}{"getThing": map[int]struct{}{http.StatusOK: {}}},
	); err == nil || !strings.Contains(err.Error(), "getThing missing adapter status 503") {
		t.Fatalf("missing status validation error = %v", err)
	}
	if err := validateOpenAPIStatusCoverage(
		map[string]map[int]struct{}{"getThing": map[int]struct{}{http.StatusOK: {}}},
		map[string]map[int]struct{}{"getThing": map[int]struct{}{http.StatusOK: {}, http.StatusServiceUnavailable: {}}},
	); err == nil || !strings.Contains(err.Error(), "getThing handles undeclared status 503") {
		t.Fatalf("undeclared status validation error = %v", err)
	}
}

func TestOpenAPIStatusCoverageFollowsActualFactoryReturns(t *testing.T) {
	for _, test := range []struct {
		name, adapter, factory, wantError string
	}{
		{
			name: "actual returned variants",
			factory: `func NewGetThingErrorResponse(status int) (any, error) {
				switch status { case 503: return GetThing503JSONResponse{}, nil }
				return nil, fmt.Errorf("unexpected")
			}`,
		},
		{
			name: "missing factory case", wantError: "getThing missing adapter status 503",
			factory: `func NewGetThingErrorResponse(status int) (any, error) {
				_ = GetThing503JSONResponse{}
				switch status { case 400: return GetThing400JSONResponse{}, nil; case 503: return nil, fmt.Errorf("unimplemented") }
				return nil, fmt.Errorf("unexpected")
			}`,
		},
		{name: "missing factory declaration", wantError: "has no generated declaration"},
		{
			name: "wrong operation factory", wantError: "factory for another operation",
			adapter: `func GetThing() { return loomapi.NewGetOtherErrorResponse(503) }`,
			factory: `func NewGetOtherErrorResponse(status int) (any, error) { return GetOther503JSONResponse{}, nil }`,
		},
		{
			name: "wrong operation variant", wantError: "returns no operation response variants",
			factory: `func NewGetThingErrorResponse(status int) (any, error) { return GetOther503JSONResponse{}, nil }`,
		},
		{
			name: "undeclared returned variant", wantError: "getThing handles undeclared status 502",
			factory: `func NewGetThingErrorResponse(status int) (any, error) {
				if status == 503 { return GetThing503JSONResponse{}, nil }
				return GetThing502JSONResponse{}, nil
			}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := test.adapter
			if adapter == "" {
				adapter = `func GetThing() { return loomapi.NewGetThingErrorResponse(503) }`
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), "fixture.go", "package fixture\n"+adapter+"\n"+test.factory, 0)
			if err != nil {
				t.Fatal(err)
			}
			factories := make(map[string]*ast.FuncDecl)
			var body *ast.BlockStmt
			for _, declaration := range parsed.Decls {
				function := declaration.(*ast.FuncDecl)
				if function.Name.Name == "GetThing" {
					body = function.Body
				} else {
					factories[function.Name.Name] = function
				}
			}
			statuses := map[int]struct{}{http.StatusOK: {}}
			err = collectAdapterStatuses(body, "GetThing", statuses, factories)
			if err == nil {
				err = validateOpenAPIStatusCoverage(
					map[string]map[int]struct{}{"getThing": {http.StatusOK: {}, http.StatusUnauthorized: {}, http.StatusServiceUnavailable: {}}},
					map[string]map[int]struct{}{"getThing": statuses},
				)
			}
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("coverage error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func declaredOpenAPIStatuses() (map[string]map[int]struct{}, error) {
	spec, err := loomapi.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("load embedded OpenAPI document: %w", err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("validate embedded OpenAPI document: %w", err)
	}
	declared := make(map[string]map[int]struct{})
	for path, item := range spec.Paths.Map() {
		for method, operation := range item.Operations() {
			if operation == nil || strings.TrimSpace(operation.OperationID) == "" {
				return nil, fmt.Errorf("%s %s has no operationId", strings.ToUpper(method), path)
			}
			statuses := make(map[int]struct{})
			for status := range operation.Responses.Map() {
				code, err := strconv.Atoi(status)
				if err != nil {
					return nil, fmt.Errorf("%s %s declares non-numeric response %q", operation.OperationID, path, status)
				}
				statuses[code] = struct{}{}
			}
			if _, exists := declared[operation.OperationID]; exists {
				return nil, fmt.Errorf("operationId %q is duplicated", operation.OperationID)
			}
			declared[operation.OperationID] = statuses
		}
	}
	return declared, nil
}

func adapterOpenAPIStatuses() (map[string]map[int]struct{}, error) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("locate status coverage test source")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(sourceFile), "openapi*.go"))
	if err != nil {
		return nil, fmt.Errorf("find OpenAPI route adapters: %w", err)
	}
	handled := make(map[string]map[int]struct{})
	fileSet := token.NewFileSet()
	generatedFiles, err := filepath.Glob(filepath.Join(filepath.Dir(sourceFile), "../../generated/loomapi/*.go"))
	if err != nil {
		return nil, err
	}
	factories := make(map[string]*ast.FuncDecl)
	for _, file := range generatedFiles {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, file, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse generated response factories %s: %w", file, err)
		}
		for _, declaration := range parsed.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil && function.Body != nil {
				factories[function.Name.Name] = function
			}
		}
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, file, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || function.Body == nil || function.Name == nil {
				continue
			}
			if !isHTTPRoutesReceiver(function.Recv) {
				continue
			}
			operationID := matchingOperationID(function.Name.Name)
			if operationID == "" {
				continue
			}
			if _, exists := handled[operationID]; exists {
				return nil, fmt.Errorf("adapter for operationId %q is duplicated", operationID)
			}
			statuses := make(map[int]struct{})
			if err := collectAdapterStatuses(function.Body, function.Name.Name, statuses, factories); err != nil {
				return nil, fmt.Errorf("%s: %w", operationID, err)
			}
			handled[operationID] = statuses
		}
	}
	return handled, nil
}

func isHTTPRoutesReceiver(fieldList *ast.FieldList) bool {
	if fieldList == nil || len(fieldList.List) != 1 {
		return false
	}
	expr := fieldList.List[0].Type
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	name, ok := star.X.(*ast.Ident)
	return ok && name.Name == "HTTPRoutes"
}

func matchingOperationID(functionName string) string {
	if functionName == "" || functionName[0] < 'A' || functionName[0] > 'Z' {
		return ""
	}
	return strings.ToLower(functionName[:1]) + functionName[1:]
}

func collectAdapterStatuses(body *ast.BlockStmt, operationName string, statuses map[int]struct{}, factories map[string]*ast.FuncDecl) error {
	var factoryErr error
	ast.Inspect(body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.SelectorExpr:
			if packageName, ok := value.X.(*ast.Ident); ok {
				if status, ok := httpStatusNames[value.Sel.Name]; ok && packageName.Name == "http" {
					statuses[status] = struct{}{}
				}
				if packageName.Name == "fiber" && value.Sel.Name == "ErrNotFound" {
					statuses[http.StatusNotFound] = struct{}{}
				}
				if packageName.Name == "loomapi" {
					collectGeneratedResponseStatus(value.Sel.Name, operationName, statuses)
				}
			}
		case *ast.CallExpr:
			if selector, ok := value.Fun.(*ast.SelectorExpr); ok {
				if packageName, ok := selector.X.(*ast.Ident); ok && packageName.Name == "loomapi" {
					name := selector.Sel.Name
					if strings.HasPrefix(name, "New") && strings.HasSuffix(name, "ErrorResponse") {
						if err := collectFactoryStatuses(name, operationName, statuses, factories); err != nil {
							factoryErr = err
						}
					} else {
						collectGeneratedResponseStatus(name, operationName, statuses)
					}
				}
			}
		case *ast.CaseClause:
			for _, expression := range value.List {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.INT {
					continue
				}
				if status, err := strconv.Atoi(literal.Value); err == nil && status >= 100 && status <= 599 {
					statuses[status] = struct{}{}
				}
			}
		}
		return true
	})
	return factoryErr
}

func collectFactoryStatuses(name, operationName string, statuses map[int]struct{}, factories map[string]*ast.FuncDecl) error {
	if name != "New"+operationName+"ErrorResponse" {
		return fmt.Errorf("%s uses response factory for another operation: %s", operationName, name)
	}
	factory := factories[name]
	if factory == nil || factory.Body == nil {
		return fmt.Errorf("response factory %s has no generated declaration", name)
	}
	returned := make(map[int]struct{})
	ast.Inspect(factory.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, result := range statement.Results {
			var typ ast.Expr
			switch value := result.(type) {
			case *ast.CompositeLit:
				typ = value.Type
			case *ast.CallExpr:
				typ = value.Fun
			}
			switch value := typ.(type) {
			case *ast.Ident:
				collectGeneratedResponseStatus(value.Name, operationName, returned)
			case *ast.SelectorExpr:
				collectGeneratedResponseStatus(value.Sel.Name, operationName, returned)
			}
		}
		return false
	})
	if len(returned) == 0 {
		return fmt.Errorf("response factory %s returns no operation response variants", name)
	}
	for status := range returned {
		statuses[status] = struct{}{}
	}
	return nil
}

func collectGeneratedResponseStatus(name, operationName string, statuses map[int]struct{}) {
	prefix := strings.ToUpper(operationName[:1]) + operationName[1:]
	if !strings.HasPrefix(name, prefix) {
		return
	}
	remainder := strings.TrimPrefix(name, prefix)
	if len(remainder) < 3 {
		return
	}
	status, err := strconv.Atoi(remainder[:3])
	if err == nil && status >= 100 && status <= 599 {
		statuses[status] = struct{}{}
	}
}

func validateOpenAPIStatusCoverage(declared, handled map[string]map[int]struct{}) error {
	for operationID, statuses := range declared {
		adapterOperationID, adapterStatuses, ok := lookupOperationStatuses(handled, operationID)
		if !ok {
			return fmt.Errorf("%s has no HTTPRoutes adapter", operationID)
		}
		for status := range statuses {
			if _, exists := adapterStatuses[status]; exists {
				continue
			}
			if status == http.StatusUnauthorized {
				continue
			}
			return fmt.Errorf("%s missing adapter status %d", operationID, status)
		}
		for status := range adapterStatuses {
			if _, exists := statuses[status]; !exists {
				return fmt.Errorf("%s handles undeclared status %d", operationID, status)
			}
		}
		delete(handled, adapterOperationID)
	}
	for operationID := range handled {
		if _, _, exists := lookupOperationStatuses(declared, operationID); !exists {
			return fmt.Errorf("adapter operation %s is absent from OpenAPI", operationID)
		}
	}
	return nil
}

func lookupOperationStatuses(operations map[string]map[int]struct{}, operationID string) (string, map[int]struct{}, bool) {
	if statuses, ok := operations[operationID]; ok {
		return operationID, statuses, true
	}
	for candidate, statuses := range operations {
		if strings.EqualFold(candidate, operationID) {
			return candidate, statuses, true
		}
	}
	return "", nil, false
}

var httpStatusNames = map[string]int{
	"StatusOK": http.StatusOK, "StatusBadRequest": http.StatusBadRequest,
	"StatusUnauthorized": http.StatusUnauthorized, "StatusForbidden": http.StatusForbidden,
	"StatusNotFound": http.StatusNotFound, "StatusConflict": http.StatusConflict,
	"StatusRequestEntityTooLarge": http.StatusRequestEntityTooLarge,
	"StatusTooManyRequests":       http.StatusTooManyRequests, "StatusUnsupportedMediaType": http.StatusUnsupportedMediaType,
	"StatusUnprocessableEntity": http.StatusUnprocessableEntity, "StatusInternalServerError": http.StatusInternalServerError,
	"StatusServiceUnavailable": http.StatusServiceUnavailable, "StatusGatewayTimeout": http.StatusGatewayTimeout,
}
