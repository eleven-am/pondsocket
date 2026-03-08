package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := flag.String("root", ".", "root directory to process")
	flag.Parse()

	var files []string
	filepath.WalkDir(*root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "vendor" || name == ".gocache" || name == "tools" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})

	fset := token.NewFileSet()
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			continue
		}

		var ranges [][2]token.Pos
		ast.Inspect(file, func(n ast.Node) bool {
			if fn, ok := n.(*ast.FuncDecl); ok && fn.Body != nil {
				ranges = append(ranges, [2]token.Pos{fn.Body.Pos(), fn.Body.End()})
			}
			return true
		})

		if len(file.Comments) > 0 && len(ranges) > 0 {
			var kept []*ast.CommentGroup
			for _, cg := range file.Comments {
				pos := cg.Pos()
				inBody := false
				for _, r := range ranges {
					if pos >= r[0] && pos <= r[1] {
						inBody = true
						break
					}
				}
				if !inBody {
					kept = append(kept, cg)
				}
			}
			file.Comments = kept
		}

		var out strings.Builder
		cfg := &printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}
		if err := cfg.Fprint(&out, fset, file); err != nil {
			fmt.Fprintf(os.Stderr, "print error for %s: %v\n", path, err)
			continue
		}
		_ = os.WriteFile(path, []byte(out.String()), 0644)
	}
}
