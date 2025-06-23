package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func dirTree(out io.Writer, path string, printFiles bool) error {
	return processDir(out, path, "", printFiles)
}

func calculateSize(size int) string {
	if size == 0 {
		return "(empty)"
	}
	return fmt.Sprintf("(%db)", size)
}

func skipFiles(entries []os.DirEntry, printFiles bool) []os.DirEntry {
	var filtered []os.DirEntry
	for _, entry := range entries {
		if !printFiles && !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func processDir(out io.Writer, path string, prefix string, printFiles bool) error {
	d, _ := os.Open(path)
	defer d.Close()

	entries, _ := d.ReadDir(-1)

	entries = skipFiles(entries, printFiles)

	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	for i, entry := range entries {
		newPrefix := ""
		isLast := i == len(entries)-1

		if isLast {
			fmt.Fprint(out, prefix+"└───")
			newPrefix = prefix + "\t"
		} else {
			fmt.Fprint(out, prefix+"├───")
			newPrefix = prefix + "│\t"
		}

		if !entry.IsDir() && printFiles {
			info, _ := entry.Info()

			size := calculateSize(int(info.Size()))
			fmt.Fprintln(out, entry.Name(), size)
		} else {
			fmt.Fprintln(out, entry.Name())
		}

		if entry.IsDir() {
			err := processDir(out, filepath.Join(path, entry.Name()), newPrefix, printFiles)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func main() {
	out := os.Stdout
	if !(len(os.Args) == 2 || len(os.Args) == 3) {
		panic("usage go run main.go . [-f]")
	}
	path := os.Args[1]
	printFiles := len(os.Args) == 3 && os.Args[2] == "-f"
	err := dirTree(out, path, printFiles)
	if err != nil {
		panic(err.Error())
	}
}
