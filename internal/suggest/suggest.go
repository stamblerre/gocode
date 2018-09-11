package suggest

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"

	"github.com/stamblerre/gocode/internal/lookdot"
	"golang.org/x/tools/go/packages"
)

type Config struct {
	Logf    func(fmt string, args ...interface{})
	Context *PackedContext
	Builtin bool
}

// Copied from go/packages.
type PackedContext struct {
	// Env is the environment to use when invoking the build system's query tool.
	// If Env is nil, the current environment is used.
	// As in os/exec's Cmd, only the last value in the slice for
	// each environment key is used. To specify the setting of only
	// a few variables, append to the current environment, as in:
	//
	//	opt.Env = append(os.Environ(), "GOOS=plan9", "GOARCH=386")
	//
	Env []string

	// BuildFlags is a list of command-line flags to be passed through to
	// the build system's query tool.
	BuildFlags []string
}

// Suggest returns a list of suggestion candidates and the length of
// the text that should be replaced, if any.
func (c *Config) Suggest(filename string, data []byte, cursor int) ([]Candidate, int) {
	if cursor < 0 {
		return nil, 0
	}

	fset, pos, pkg := c.analyzePackage(filename, data, cursor)
	if pkg == nil {
		return nil, 0
	}
	scope := pkg.Scope().Innermost(pos)

	ctx, expr, partial := deduceCursorContext(data, cursor)
	b := candidateCollector{
		localpkg: pkg,
		partial:  partial,
		filter:   objectFilters[partial],
		builtin:  ctx != selectContext && c.Builtin,
	}

	switch ctx {
	case selectContext:
		tv, _ := types.Eval(fset, pkg, pos, expr)
		if lookdot.Walk(&tv, b.appendObject) {
			break
		}

		_, obj := scope.LookupParent(expr, pos)
		if pkgName, isPkg := obj.(*types.PkgName); isPkg {
			c.packageCandidates(pkgName.Imported(), &b)
			break
		}

		return nil, 0

	case compositeLiteralContext:
		tv, _ := types.Eval(fset, pkg, pos, expr)
		if tv.IsType() {
			if _, isStruct := tv.Type.Underlying().(*types.Struct); isStruct {
				c.fieldNameCandidates(tv.Type, &b)
				break
			}
		}

		fallthrough
	default:
		c.scopeCandidates(scope, pos, &b)
	}

	res := b.getCandidates()
	if len(res) == 0 {
		return nil, 0
	}
	return res, len(partial)
}

func (c *Config) analyzePackage(filename string, data []byte, cursor int) (*token.FileSet, token.Pos, *types.Package) {
	var pos token.Pos

	cfg := &packages.Config{
		Mode:       packages.LoadSyntax,
		Env:        c.Context.Env,
		BuildFlags: c.Context.BuildFlags,
		ParseFile: func(fset *token.FileSet, parseFilename string) (*ast.File, error) {
			var src interface{}
			mode := parser.DeclarationErrors
			if filename == parseFilename {
				// If we're in trailing white space at the end of a scope,
				// sometimes go/types doesn't recognize that variables should
				// still be in scope there.
				src = bytes.Join([][]byte{data[:cursor], []byte(";"), data[cursor:]}, nil)
				mode = parser.AllErrors
			}
			file, err := parser.ParseFile(fset, parseFilename, src, mode)
			if file == nil {
				return nil, err
			}
			if filename == parseFilename {
				pos = fset.File(file.Pos()).Pos(cursor)
				if pos == token.NoPos {
					return nil, fmt.Errorf("no position for cursor in %s", parseFilename)
				}
			}
			for _, decl := range file.Decls {
				if fd, ok := decl.(*ast.FuncDecl); ok {
					if pos == token.NoPos || (pos < fd.Pos() || pos >= fd.End()) {
						fd.Body = nil
					}
				}
			}
			return file, nil
		},
	}
	pkgs, _ := packages.Load(cfg, fmt.Sprintf("contains:%v", filename))
	if len(pkgs) <= 0 { // ignore errors
		return nil, token.NoPos, nil
	}
	pkg := pkgs[0]

	return pkg.Fset, pos, pkg.Types
}

func (c *Config) fieldNameCandidates(typ types.Type, b *candidateCollector) {
	s := typ.Underlying().(*types.Struct)
	for i, n := 0, s.NumFields(); i < n; i++ {
		b.appendObject(s.Field(i))
	}
}

func (c *Config) packageCandidates(pkg *types.Package, b *candidateCollector) {
	c.scopeCandidates(pkg.Scope(), token.NoPos, b)
}

func (c *Config) scopeCandidates(scope *types.Scope, pos token.Pos, b *candidateCollector) {
	seen := make(map[string]bool)
	for scope != nil {
		for _, name := range scope.Names() {
			if seen[name] {
				continue
			}
			seen[name] = true
			_, obj := scope.LookupParent(name, pos)
			if obj != nil {
				b.appendObject(obj)
			}
		}
		scope = scope.Parent()
	}
}
