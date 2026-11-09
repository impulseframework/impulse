// Package gomod reads the module path and root of the application being generated from the
// nearest go.mod.
package gomod

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-playground/errors/v5"
)

// Information holds information about the current package
type Information struct {
	AbsolutePath string
	PackageName  string
}

// Info returns Information about the current package. The current package is determined
// by searching the current path towards root until it finds the first go.mod file. It
// then uses the path and content of go.mod to populate Information
func Info() (*Information, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, errors.Wrap(err, "os.Getwd()")
	}

	var f *os.File
	for {
		f, err = os.Open(filepath.Join(cwd, "go.mod"))
		if err != nil && !os.IsNotExist(err) {
			return nil, errors.Wrap(err, "os.Open()")
		} else if os.IsNotExist(err) {
			if cwd == "/" {
				return nil, errors.New("gomod.Info(): reached root and did not find go.mod")
			}

			cwd = filepath.Dir(cwd)

			continue
		}

		break
	}
	defer f.Close()

	buff := bufio.NewScanner(f)
	for buff.Scan() {
		line := buff.Text()
		if strings.HasPrefix(line, "module") {
			parts := strings.Fields(line)
			if len(parts) < 2 {
				return nil, errors.New("gomod.Info(): failed to find module path in go.mod")
			}

			return &Information{
				AbsolutePath: cwd,
				PackageName:  parts[1],
			}, nil
		}
	}

	return nil, errors.New("gomod.Info(): failed to find module directive in go.mod")
}
