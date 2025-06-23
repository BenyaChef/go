package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func dirTree(out io.Writer, path string, printFiles bool) error {
	return processDir(out, path, "", printFiles)
}

func shouldSkip(name string) bool {
	skipPrefixes := []string{
		".DS_Store",
		".git",
		".gitignore",
		".idea",
	}
	for _, prefix := range skipPrefixes {
		if strings.HasPrefix(filepath.Base(name), prefix) {
			return true
		}
	}
	return false
}

func processDir(out io.Writer, path string, prefix string, printFiles bool) error {
	d, _ := os.Open(path)
	defer d.Close()

	entries, _ := d.ReadDir(-1)
	for i, entry := range entries {
		isLast := i == len(entries)-1
		if isLast {
			fmt.Fprintln(out, prefix+"└───"+entry.Name())
		} else {
			fmt.Fprintln(out, prefix+"└───"+entry.Name())
		}
		if !entry.IsDir() {

		} else {
			var newPrefix string
			if isLast {
				newPrefix = prefix + "\t"
			} else {
				newPrefix = prefix + "│\t"
			}
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
