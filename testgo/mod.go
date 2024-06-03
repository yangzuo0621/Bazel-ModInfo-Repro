package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"

	"golang.org/x/mod/modfile"
)

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
