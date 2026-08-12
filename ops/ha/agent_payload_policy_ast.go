//go:build ignore

// Structural policy analyzer for the apply-agent plaintext boundary.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var sensitiveTypes = map[string]bool{"MaterializedEntry": true, "MaterializedSnapshot": true}

type analyzer struct {
	imports       map[string]string
	helpers       map[string]bool
	violations    []string
	sensitive     map[string]bool
	selectors     map[string]bool
	jsonEncoders  map[string]bool
}

func typeName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return typeName(value.X)
	case *ast.ArrayType:
		return typeName(value.Elt)
	case *ast.SelectorExpr:
		return value.Sel.Name
	}
	return ""
}

func selectorKey(expr *ast.SelectorExpr) string {
	if ident, ok := expr.X.(*ast.Ident); ok {
		return ident.Name + "." + expr.Sel.Name
	}
	return ""
}

func (a *analyzer) exprSensitive(expr ast.Expr) bool {
	switch value := expr.(type) {
	case *ast.Ident:
		return a.sensitive[value.Name]
	case *ast.SelectorExpr:
		return value.Sel.Name == "Body" || a.selectors[selectorKey(value)] || a.exprSensitive(value.X)
	case *ast.ParenExpr:
		return a.exprSensitive(value.X)
	case *ast.StarExpr:
		return a.exprSensitive(value.X)
	case *ast.UnaryExpr:
		return a.exprSensitive(value.X)
	case *ast.IndexExpr:
		return a.exprSensitive(value.X) || a.exprSensitive(value.Index)
	case *ast.SliceExpr:
		return a.exprSensitive(value.X)
	case *ast.TypeAssertExpr:
		return a.exprSensitive(value.X)
	case *ast.CompositeLit:
		if sensitiveTypes[typeName(value.Type)] {
			return true
		}
		for _, element := range value.Elts {
			switch item := element.(type) {
			case *ast.KeyValueExpr:
				if a.exprSensitive(item.Value) { return true }
			case ast.Expr:
				if a.exprSensitive(item) { return true }
			}
		}
	case *ast.CallExpr:
		if ident, ok := value.Fun.(*ast.Ident); ok && a.helpers[ident.Name] {
			return true
		}
		for _, argument := range value.Args {
			if a.exprSensitive(argument) { return true }
		}
	case *ast.BinaryExpr:
		return a.exprSensitive(value.X) || a.exprSensitive(value.Y)
	}
	return false
}

func (a *analyzer) sink(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok { return false }
	if ident, ok := selector.X.(*ast.Ident); ok {
		path := a.imports[ident.Name]
		switch path {
		case "encoding/json":
			return selector.Sel.Name == "Marshal" || selector.Sel.Name == "MarshalIndent"
		case "fmt":
			return strings.HasPrefix(selector.Sel.Name, "Print") || strings.HasPrefix(selector.Sel.Name, "Sprint") || strings.HasPrefix(selector.Sel.Name, "Fprint")
		case "log":
			return strings.HasPrefix(selector.Sel.Name, "Print") || strings.HasPrefix(selector.Sel.Name, "Fatal") || strings.HasPrefix(selector.Sel.Name, "Panic")
		case "log/slog":
			return map[string]bool{"Debug":true,"DebugContext":true,"Error":true,"ErrorContext":true,"Info":true,"InfoContext":true,"Log":true,"LogAttrs":true,"Warn":true,"WarnContext":true}[selector.Sel.Name]
		}
		return selector.Sel.Name == "Encode" && a.jsonEncoders[ident.Name]
	}
	return false
}

func (a *analyzer) inspectCall(call *ast.CallExpr) {
	if !a.sink(call) { return }
	for _, argument := range call.Args {
		if a.exprSensitive(argument) {
			a.violations = append(a.violations, "materialized-body-sink")
			return
		}
	}
}

func (a *analyzer) assign(lhs, rhs ast.Expr) {
	sensitive := a.exprSensitive(rhs)
	switch target := lhs.(type) {
	case *ast.Ident:
		if sensitive { a.sensitive[target.Name] = true }
		if call, ok := rhs.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				if ident, ok := selector.X.(*ast.Ident); ok && a.imports[ident.Name] == "encoding/json" && selector.Sel.Name == "NewEncoder" {
					a.jsonEncoders[target.Name] = true
				}
			}
		}
	case *ast.SelectorExpr:
		if sensitive { a.selectors[selectorKey(target)] = true }
	}
}

func (a *analyzer) inspectBlock(block *ast.BlockStmt) {
	if block == nil { return }
	for _, statement := range block.List {
		ast.Inspect(statement, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok { a.inspectCall(call) }
			return true
		})
		switch value := statement.(type) {
		case *ast.AssignStmt:
			for index, lhs := range value.Lhs {
				if index < len(value.Rhs) { a.assign(lhs, value.Rhs[index]) }
			}
		case *ast.DeclStmt:
			declaration, _ := value.Decl.(*ast.GenDecl)
			if declaration == nil { continue }
			for _, spec := range declaration.Specs {
				variable, _ := spec.(*ast.ValueSpec)
				if variable == nil { continue }
				for index, name := range variable.Names {
					if sensitiveTypes[typeName(variable.Type)] { a.sensitive[name.Name] = true }
					if index < len(variable.Values) { a.assign(name, variable.Values[index]) }
				}
			}
		case *ast.IfStmt:
			a.inspectBlock(value.Body); if branch, ok := value.Else.(*ast.BlockStmt); ok { a.inspectBlock(branch) }
		case *ast.ForStmt:
			a.inspectBlock(value.Body)
		case *ast.RangeStmt:
			a.inspectBlock(value.Body)
		case *ast.SwitchStmt:
			for _, clause := range value.Body.List { if item, ok := clause.(*ast.CaseClause); ok { a.inspectBlock(&ast.BlockStmt{List:item.Body}) } }
		}
	}
}

func paramsSensitive(function *ast.FuncDecl) map[string]bool {
	result := map[string]bool{}
	if function.Type.Params == nil { return result }
	for _, field := range function.Type.Params.List {
		if !sensitiveTypes[typeName(field.Type)] { continue }
		for _, name := range field.Names { result[name.Name] = true }
	}
	return result
}

func helperReturnsSensitive(function *ast.FuncDecl, helpers map[string]bool) bool {
	a := analyzer{sensitive: paramsSensitive(function), selectors:map[string]bool{}, helpers:helpers, imports:map[string]string{}, jsonEncoders:map[string]bool{}}
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if ret, ok := node.(*ast.ReturnStmt); ok {
			for _, value := range ret.Results { if a.exprSensitive(value) { found = true } }
		}
		return !found
	})
	return found
}

func analyzeFile(path string, production bool) ([]string, error) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, nil, parser.AllErrors)
	if err != nil { return nil, err }
	imports := map[string]string{}
	for _, item := range file.Imports {
		pathValue, _ := strconv.Unquote(item.Path.Value)
		name := filepath.Base(pathValue)
		if item.Name != nil { name = item.Name.Name }
		imports[name] = pathValue
	}
	violations := []string{}
	for _, declaration := range file.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok || generic.Tok != token.TYPE { continue }
		for _, spec := range generic.Specs {
			typeSpec, _ := spec.(*ast.TypeSpec); iface, _ := typeSpec.Type.(*ast.InterfaceType)
			if iface == nil { continue }
			for _, method := range iface.Methods.List {
				if len(method.Names) != 1 || (method.Names[0].Name != "Inspect" && method.Names[0].Name != "Prepare") { continue }
				function, _ := method.Type.(*ast.FuncType); if function == nil || function.Params == nil { continue }
				for _, parameter := range function.Params.List { if typeName(parameter.Type) == "DesiredSnapshot" { violations = append(violations, "legacy-driver-"+strings.ToLower(method.Names[0].Name)) } }
			}
		}
	}
	if !production { return violations, nil }
	functions := []*ast.FuncDecl{}
	for _, declaration := range file.Decls { if function, ok := declaration.(*ast.FuncDecl); ok { functions = append(functions, function) } }
	helpers := map[string]bool{}
	changed := true
	for changed {
		changed = false
		for _, function := range functions { if !helpers[function.Name.Name] && helperReturnsSensitive(function, helpers) { helpers[function.Name.Name] = true; changed = true } }
	}
	for _, function := range functions {
		a := analyzer{imports:imports, helpers:helpers, sensitive:paramsSensitive(function), selectors:map[string]bool{}, jsonEncoders:map[string]bool{}}
		a.inspectBlock(function.Body)
		violations = append(violations, a.violations...)
	}
	return violations, nil
}

func main() {
	root := flag.String("root", ".", "repository root")
	file := flag.String("file", "", "single fixture file")
	production := flag.Bool("production", false, "apply production sink rules")
	flag.Parse()
	paths := []string{}
	if *file != "" { paths = append(paths, *file) } else {
		for _, relative := range []string{"backend/internal/applyagent", "backend/internal/controlplane"} {
			err := filepath.Walk(filepath.Join(*root, relative), func(path string, info os.FileInfo, err error) error {
				if err != nil { return err }; if !info.IsDir() && strings.HasSuffix(path, ".go") { paths = append(paths, path) }; return nil
			}); if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
		}
	}
	sort.Strings(paths)
	found := []string{}
	for _, path := range paths {
		isProduction := *production || (strings.Contains(filepath.ToSlash(path), "/internal/applyagent/") && !strings.HasSuffix(path, "_test.go"))
		violations, err := analyzeFile(path, isProduction); if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
		for _, violation := range violations { found = append(found, filepath.ToSlash(path)+":"+violation) }
	}
	for _, violation := range found { fmt.Println(violation) }
	if len(found) > 0 { os.Exit(1) }
}
