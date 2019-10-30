package suggest

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/stamblerre/gocode/internal/lookdot"
	"golang.org/x/tools/go/packages"
)

type Config struct {
	RequestContext context.Context

	Logf               func(fmt string, args ...interface{})
	Context            *PackedContext
	Builtin            bool
	IgnoreCase         bool
	UnimportedPackages bool
}

// PackedContext is copied from go/packages.Config.
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

	// Dir is the directory in which to run the build system's query tool
	// that provides information about the packages.
	// If Dir is empty, the tool is run in the current directory.
	Dir string

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

	fset, pos, pkg, imports := c.analyzePackage(filename, data, cursor)
	if pkg == nil {
		c.Logf("no package found for %s", filename)
		return nil, 0
	}
	scope := pkg.Scope().Innermost(pos)

	ctx, expr, partial := deduceCursorContext(data, cursor)
	b := candidateCollector{
		localpkg:   pkg,
		imports:    imports,
		partial:    partial,
		filter:     objectFilters[partial],
		builtin:    ctx != selectContext && c.Builtin,
		ignoreCase: c.IgnoreCase,
	}

	switch ctx {
	case emptyResultsContext:
		// don't show results in certain cases
		return nil, 0

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
	case unknownContext:
		c.scopeCandidates(scope, pos, &b)
	}

	res := b.getCandidates()
	if len(res) == 0 {
		return nil, 0
	}
	return res, len(partial)
}

func (c *Config) analyzePackage(filename string, data []byte, cursor int) (*token.FileSet, token.Pos, *types.Package, []*ast.ImportSpec) {
	var tags string
	parsed, _ := parser.ParseFile(token.NewFileSet(), filename, data, parser.ParseComments)
	if parsed != nil && len(parsed.Comments) > 0 {
		buildTagText := parsed.Comments[0].Text()
		if strings.HasPrefix(buildTagText, "+build ") {
			tags = strings.TrimPrefix(buildTagText, "+build ")
		}
	}
	if suffix := buildConstraint(filename); suffix != "" {
		tags = suffix
	}

	ctx := c.RequestContext
	if ctx == nil {
		ctx = context.Background()
	}

	var fileAST *ast.File
	var pos token.Pos
	var posMu sync.Mutex // guards pos and fileAST in ParseFile

	cfg := &packages.Config{
		Context: ctx,

		Mode:       packages.LoadSyntax,
		Env:        c.Context.Env,
		Dir:        c.Context.Dir,
		BuildFlags: append(c.Context.BuildFlags, fmt.Sprintf("-tags=%s", tags)),
		Tests:      true,
		Overlay: map[string][]byte{
			filename: data,
		},
		ParseFile: func(fset *token.FileSet, parseFilename string, _ []byte) (*ast.File, error) {
			var src interface{}
			mode := parser.DeclarationErrors
			if sameFile(filename, parseFilename) {
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
			var cursorPos token.Pos
			if sameFile(filename, parseFilename) {
				filePos := file.Pos()
				if !filePos.IsValid() {
					return nil, fmt.Errorf("invalid position: %v, %v", file.Name.Name, filePos)
				}
				tok := fset.File(filePos)
				if tok == nil {
					return nil, fmt.Errorf("no token.File for file %s", file.Name.Name)
				}
				cursorPos = tok.Pos(cursor)
				if !cursorPos.IsValid() || cursorPos == token.NoPos {
					return nil, fmt.Errorf("no position for cursor in %s", parseFilename)
				}
				posMu.Lock()
				if pos == token.NoPos {
					pos = cursorPos
				}
				fileAST = file
				posMu.Unlock()
			}
			for _, decl := range file.Decls {
				if fd, ok := decl.(*ast.FuncDecl); ok {
					if cursorPos == token.NoPos || (cursorPos < fd.Pos() || cursorPos >= fd.End()) {
						fd.Body = nil
					}
				}
			}
			return file, nil
		},
	}
	pkgs, err := packages.Load(cfg, fmt.Sprintf("file=%v", filename))
	if len(pkgs) <= 0 { // ignore errors
		c.Logf("no package found for %s: %v", filename, err)
		return nil, token.NoPos, nil, nil
	}
	pkg := pkgs[0]
	for _, err := range pkg.Errors {
		c.Logf("error in package %s: %s:%s", pkg.PkgPath, err.Pos, err.Msg)
	}
	if fileAST == nil {
		return nil, token.NoPos, nil, nil
	}
	return pkg.Fset, pos, pkg.Types, fileAST.Imports
}

func sameFile(filename1, filename2 string) bool {
	finfo1, err := os.Stat(filename1)
	if err != nil {
		return false
	}
	finfo2, err := os.Stat(filename2)
	if err != nil {
		return false
	}
	return os.SameFile(finfo1, finfo2)
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

// The constants and functions below were adapted from go/build.

const (
	GoosList   = "android darwin dragonfly freebsd js linux nacl netbsd openbsd plan9 solaris windows zos "
	GoarchList = "386 amd64 amd64p32 arm armbe arm64 arm64be ppc64 ppc64le mips mipsle mips64 mips64le mips64p32 mips64p32le ppc riscv riscv64 s390 s390x sparc sparc64 wasm "
)

var (
	KnownOS   = make(map[string]bool)
	KnownArch = make(map[string]bool)
)

// buildConstraint determines whether the file with the given name has
// build constraints. If it does, it returns the {$GOOS}_{$GOARCH}
// for the file, if it does have build constraints.
// It as an adapted version of the matchFile function from go/build.
func buildConstraint(filename string) (suffix string) {
	name := filepath.Base(filename)
	if strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
		return ""
	}
	i := strings.LastIndex(name, ".")
	if i < 0 {
		i = len(name)
	}
	os, arch, ok := goodOSArchFile(name)
	if !ok {
		return ""
	}
	if os == "" {
		return arch
	} else if arch == "" {
		return os
	}
	return os + "_" + arch
}

// goodOSArchFile returns the $GOOS and $GOARCH for a given filename,
// if they match accepted OSes and architectures.
// The recognized name formats are:
//
//     name_$(GOOS).*
//     name_$(GOARCH).*
//     name_$(GOOS)_$(GOARCH).*
//     name_$(GOOS)_test.*
//     name_$(GOARCH)_test.*
//     name_$(GOOS)_$(GOARCH)_test.*
//
// An exception: if GOOS=android, then files with GOOS=linux are also matched.
// This function is adapted from go/build.
func goodOSArchFile(name string) (os, arch string, match bool) {
	if dot := strings.Index(name, "."); dot != -1 {
		name = name[:dot]
	}
	// Cut everything in the name before the initial _.
	i := strings.Index(name, "_")
	if i < 0 {
		return "", "", false
	}
	name = name[i:] // ignore everything before first _

	l := strings.Split(name, "_")
	if n := len(l); n > 0 && l[n-1] == "test" {
		l = l[:n-1]
	}
	n := len(l)
	if n >= 2 && KnownOS[l[n-2]] && KnownArch[l[n-1]] {
		return l[n-2], l[n-1], true
	}
	if n >= 1 && KnownOS[l[n-1]] {
		return l[n-1], "", true
	}
	if n >= 1 && KnownArch[l[n-1]] {
		return "", l[n-1], true
	}
	return "", "", false
}
