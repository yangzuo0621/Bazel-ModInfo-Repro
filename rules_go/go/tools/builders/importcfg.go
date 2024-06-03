// Copyright 2019 The Bazel Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
)

type archive struct {
	label, importPath, packagePath, file string
	importPathAliases                    []string
}

// checkImports verifies that each import in files refers to a
// direct dependency in archives or to a standard library package
// listed in the file at stdPackageListPath. checkImports returns
// a map from source import paths to elements of archives or to nil
// for standard library packages.
func checkImports(files []fileInfo, archives []archive, stdPackageListPath string, importPath string, recompileInternalDeps []string) (map[string]*archive, error) {
	// Read the standard package list.
	packagesTxt, err := ioutil.ReadFile(stdPackageListPath)
	if err != nil {
		return nil, err
	}
	stdPkgs := make(map[string]bool)
	for len(packagesTxt) > 0 {
		n := bytes.IndexByte(packagesTxt, '\n')
		var line string
		if n < 0 {
			line = string(packagesTxt)
			packagesTxt = nil
		} else {
			line = string(packagesTxt[:n])
			packagesTxt = packagesTxt[n+1:]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		stdPkgs[line] = true
	}

	// Index the archives.
	importToArchive := make(map[string]*archive)
	importAliasToArchive := make(map[string]*archive)
	for i := range archives {
		arc := &archives[i]
		importToArchive[arc.importPath] = arc
		for _, imp := range arc.importPathAliases {
			importAliasToArchive[imp] = arc
		}
	}
	// Construct recompileInternalDeps as a map to check if there are imports that are disallowed.
	recompileInternalDepMap := make(map[string]struct{})
	for _, dep := range recompileInternalDeps {
		recompileInternalDepMap[dep] = struct{}{}
	}
	// Build the import map.
	imports := make(map[string]*archive)
	var derr depsError
	for _, f := range files {
		for _, imp := range f.imports {
			path := imp.path
			if _, ok := imports[path]; ok || path == "C" || isRelative(path) {
				// TODO(#1645): Support local (relative) import paths. We don't emit
				// errors for them here, but they will probably break something else.
				continue
			}
			if _, ok := recompileInternalDepMap[path]; ok {
				return nil, fmt.Errorf("dependency cycle detected between %q and %q in file %q", importPath, path, f.filename)
			}
			if stdPkgs[path] {
				imports[path] = nil
			} else if arc := importToArchive[path]; arc != nil {
				imports[path] = arc
			} else if arc := importAliasToArchive[path]; arc != nil {
				imports[path] = arc
			} else {
				derr.missing = append(derr.missing, missingDep{f.filename, path})
			}
		}
	}
	if len(derr.missing) > 0 {
		return nil, derr
	}
	return imports, nil
}

// buildImportcfgFileForCompile writes an importcfg file to be consumed by the
// compiler. The file is constructed from direct dependencies and std imports.
// The caller is responsible for deleting the importcfg file.
func buildImportcfgFileForCompile(imports map[string]*archive, installSuffix, dir string) (string, error) {
	buf := &bytes.Buffer{}
	goroot, ok := os.LookupEnv("GOROOT")
	if !ok {
		return "", errors.New("GOROOT not set")
	}
	goroot = abs(goroot)

	sortedImports := make([]string, 0, len(imports))
	for imp := range imports {
		sortedImports = append(sortedImports, imp)
	}
	sort.Strings(sortedImports)

	for _, imp := range sortedImports {
		if arc := imports[imp]; arc == nil {
			// std package
			path := filepath.Join(goroot, "pkg", installSuffix, filepath.FromSlash(imp))
			fmt.Fprintf(buf, "packagefile %s=%s.a\n", imp, path)
		} else {
			if imp != arc.packagePath {
				fmt.Fprintf(buf, "importmap %s=%s\n", imp, arc.packagePath)
			}
			fmt.Fprintf(buf, "packagefile %s=%s\n", arc.packagePath, arc.file)
		}
	}

	f, err := ioutil.TempFile(dir, "importcfg")
	if err != nil {
		return "", err
	}
	filename := f.Name()
	if _, err := io.Copy(f, buf); err != nil {
		f.Close()
		os.Remove(filename)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(filename)
		return "", err
	}
	return filename, nil
}

func buildImportcfgFileForLink(archives []archive, stdPackageListPath, installSuffix, dir string) (string, error) {
	buf := &bytes.Buffer{}
	goroot, ok := os.LookupEnv("GOROOT")
	if !ok {
		return "", errors.New("GOROOT not set")
	}
	prefix := abs(filepath.Join(goroot, "pkg", installSuffix))
	stdPackageListFile, err := os.Open(stdPackageListPath)
	if err != nil {
		return "", err
	}
	defer stdPackageListFile.Close()
	scanner := bufio.NewScanner(stdPackageListFile)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fmt.Fprintf(buf, "packagefile %s=%s.a\n", line, filepath.Join(prefix, filepath.FromSlash(line)))
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	depsSeen := map[string]string{}
	for _, arc := range archives {
		if _, ok := depsSeen[arc.packagePath]; ok {
			return "", fmt.Errorf("internal error: package %s provided multiple times. This should have been detected during analysis.", arc.packagePath)
		}
		depsSeen[arc.packagePath] = arc.label
		fmt.Fprintf(buf, "packagefile %s=%s\n", arc.packagePath, arc.file)
	}
	runfileDir, err := bazel.RunfilesPath()
	if err != nil {
		return "", err
	}

	fmt.Println("====================== runfileDir", runfileDir)

	goModFile := filepath.Join(runfileDir, "go.mod")
	goSumFile := filepath.Join(runfileDir, "go.sum")

	goMod, err := readGoModFile(goModFile)
	if err != nil {
		return "", err
	}
	goSum, err := readGoSumFile(goSumFile)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(buf, "modinfo %q\n", modInfoData(modInfo(goMod, goSum)))

	f, err := ioutil.TempFile(dir, "importcfg")
	if err != nil {
		return "", err
	}
	filename := f.Name()
	if _, err := io.Copy(f, buf); err != nil {
		f.Close()
		os.Remove(filename)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(filename)
		return "", err
	}
	return filename, nil
}

type depsError struct {
	missing []missingDep
	known   []string
}

type missingDep struct {
	filename, imp string
}

var _ error = depsError{}

func (e depsError) Error() string {
	buf := bytes.NewBuffer(nil)
	fmt.Fprintf(buf, "missing strict dependencies:\n")
	for _, dep := range e.missing {
		fmt.Fprintf(buf, "\t%s: import of %q\n", dep.filename, dep.imp)
	}
	if len(e.known) == 0 {
		fmt.Fprintln(buf, "No dependencies were provided.")
	} else {
		fmt.Fprintln(buf, "Known dependencies are:")
		for _, imp := range e.known {
			fmt.Fprintf(buf, "\t%s\n", imp)
		}
	}
	fmt.Fprint(buf, "Check that imports in Go sources match importpath attributes in deps.")
	return buf.String()
}

func isRelative(path string) bool {
	return strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../")
}

type archiveMultiFlag []archive

func (m *archiveMultiFlag) String() string {
	if m == nil || len(*m) == 0 {
		return ""
	}
	return fmt.Sprint(*m)
}

func (m *archiveMultiFlag) Set(v string) error {
	parts := strings.Split(v, "=")
	if len(parts) != 3 {
		return fmt.Errorf("badly formed -arc flag: %s", v)
	}
	importPaths := strings.Split(parts[0], ":")
	a := archive{
		importPath:        importPaths[0],
		importPathAliases: importPaths[1:],
		packagePath:       parts[1],
		file:              abs(parts[2]),
	}
	*m = append(*m, a)
	return nil
}

var (
	infoStart, _ = hex.DecodeString("3077af0c9274080241e1c107e6d618e6")
	infoEnd, _   = hex.DecodeString("f932433186182072008242104116d8f2")
)

func ModInfoData(info string) []byte {
	return []byte(string(infoStart) + info + string(infoEnd))
}

func ModInfo(goMod *modfile.File, goSum map[string][]string) string {
	buf := new(strings.Builder)
	for _, m := range goMod.Require {
		if sum, ok := goSum[m.Mod.String()]; ok {
			if len(sum) == 0 {
				continue
			}
			buf.WriteString("dep")
			buf.WriteByte('\t')
			buf.WriteString(m.Mod.Path)
			buf.WriteByte('\t')
			buf.WriteString(m.Mod.Version)
			buf.WriteByte('\t')
			buf.WriteString(sum[0])
			buf.WriteByte('\n')
		}
	}
	return buf.String()
}

// fmt.Fprintf(&icfg, "modinfo %q\n", modload.ModInfoData(info))

func readGoModFile(file string) (*modfile.File, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	result, err := modfile.Parse(file, data, nil)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func readGoSumFile(file string) (map[string][]string, error) {
	var (
		data []byte
		err  error
	)
	data, err = os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	result := make(map[string][]string)
	readGoSum(result, file, data)

	return result, nil
}

const emptyGoModHash = "h1:G7mAYYxgmS0lVkHyy2hEOLQCFB0DlQFTMLWggykrydY="

func readGoSum(dst map[string][]string, file string, data []byte) {
	lineno := 0
	for len(data) > 0 {
		var line []byte
		lineno++
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			line, data = data, nil
		} else {
			line, data = data[:i], data[i+1:]
		}
		f := strings.Fields(string(line))
		if len(f) == 0 {
			// blank line; skip it
			continue
		}
		if len(f) != 3 {
			log.Fatalf("malformed go.sum:\n%s:%d: wrong number of fields %v\n", file, lineno, len(f))
		}
		if f[2] == emptyGoModHash {
			// Old bug; drop it.
			continue
		}
		mod := fmt.Sprintf("%s@%s", f[0], f[1])
		dst[mod] = append(dst[mod], f[2])
	}
}
